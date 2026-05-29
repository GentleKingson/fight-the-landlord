#!/usr/bin/env python3
"""
DouZero HTTP service — ONNX runtime edition.

Models are downloaded from HuggingFace on first startup.
No torch required.
"""

import os
import json
import traceback
import urllib.request
from collections import Counter
from http.server import HTTPServer, BaseHTTPRequestHandler

import numpy as np
import onnxruntime as ort

from src import move_detector as md, move_selector as ms
from src.move_generator import MovesGener
from src.game import InfoSet
from src.env import get_obs

MODEL_DIR = os.environ.get("DOUZERO_MODELS", os.path.join(os.path.dirname(__file__), "models"))
PORT = int(os.environ.get("PORT", 2021))
HF_BASE = "https://huggingface.co/palemoky/douzero-baselines/resolve/main/models_onnx/douzero_WP"

POSITIONS = ["landlord", "landlord_down", "landlord_up"]

sessions: dict[str, ort.InferenceSession] = {}


def download_models() -> None:
    os.makedirs(MODEL_DIR, exist_ok=True)
    for pos in POSITIONS:
        path = os.path.join(MODEL_DIR, f"{pos}.onnx")
        if os.path.exists(path):
            print(f"  Found   {pos}.onnx")
            continue
        url = f"{HF_BASE}/{pos}.onnx"
        print(f"  Downloading {pos}.onnx ...")
        urllib.request.urlretrieve(url, path)
        print(f"  Saved → {path}")


def load_sessions() -> None:
    providers = ["CPUExecutionProvider"]
    for pos in POSITIONS:
        path = os.path.join(MODEL_DIR, f"{pos}.onnx")
        sessions[pos] = ort.InferenceSession(path, providers=providers)
        print(f"  Loaded  {pos}")


def _act(position: str, infoset) -> list:
    if len(infoset.legal_actions) == 1:
        return infoset.legal_actions[0]

    obs = get_obs(infoset)
    z_batch = obs["z_batch"].astype(np.float32)
    x_batch = obs["x_batch"].astype(np.float32)

    y_pred = sessions[position].run(["values"], {"z": z_batch, "x": x_batch})[0]
    best_idx = int(np.argmax(y_pred[:, 0]))
    return infoset.legal_actions[best_idx]


# ---------------------------------------------------------------------------
# Legal-action generation
# ---------------------------------------------------------------------------

def get_legal_card_play_actions(hand: list, rival_move: list) -> list:
    mg = MovesGener(hand)
    rival_type = md.get_move_type(rival_move)
    rival_move_type = rival_type["type"]
    rival_move_len = rival_type.get("len", 1)
    moves = []

    if rival_move_type == md.TYPE_0_PASS:
        moves = mg.gen_moves()
    elif rival_move_type == md.TYPE_1_SINGLE:
        moves = ms.filter_type_1_single(mg.gen_type_1_single(), rival_move)
    elif rival_move_type == md.TYPE_2_PAIR:
        moves = ms.filter_type_2_pair(mg.gen_type_2_pair(), rival_move)
    elif rival_move_type == md.TYPE_3_TRIPLE:
        moves = ms.filter_type_3_triple(mg.gen_type_3_triple(), rival_move)
    elif rival_move_type == md.TYPE_4_BOMB:
        moves = ms.filter_type_4_bomb(mg.gen_type_4_bomb() + mg.gen_type_5_king_bomb(), rival_move)
    elif rival_move_type == md.TYPE_5_KING_BOMB:
        moves = []
    elif rival_move_type == md.TYPE_6_3_1:
        moves = ms.filter_type_6_3_1(mg.gen_type_6_3_1(), rival_move)
    elif rival_move_type == md.TYPE_7_3_2:
        moves = ms.filter_type_7_3_2(mg.gen_type_7_3_2(), rival_move)
    elif rival_move_type == md.TYPE_8_SERIAL_SINGLE:
        moves = ms.filter_type_8_serial_single(mg.gen_type_8_serial_single(repeat_num=rival_move_len), rival_move)
    elif rival_move_type == md.TYPE_9_SERIAL_PAIR:
        moves = ms.filter_type_9_serial_pair(mg.gen_type_9_serial_pair(repeat_num=rival_move_len), rival_move)
    elif rival_move_type == md.TYPE_10_SERIAL_TRIPLE:
        moves = ms.filter_type_10_serial_triple(mg.gen_type_10_serial_triple(repeat_num=rival_move_len), rival_move)
    elif rival_move_type == md.TYPE_11_SERIAL_3_1:
        moves = ms.filter_type_11_serial_3_1(mg.gen_type_11_serial_3_1(repeat_num=rival_move_len), rival_move)
    elif rival_move_type == md.TYPE_12_SERIAL_3_2:
        moves = ms.filter_type_12_serial_3_2(mg.gen_type_12_serial_3_2(repeat_num=rival_move_len), rival_move)
    elif rival_move_type == md.TYPE_13_4_2:
        moves = ms.filter_type_13_4_2(mg.gen_type_13_4_2(), rival_move)
    elif rival_move_type == md.TYPE_14_4_22:
        moves = ms.filter_type_14_4_22(mg.gen_type_14_4_22(), rival_move)

    if rival_move_type not in (md.TYPE_0_PASS, md.TYPE_4_BOMB, md.TYPE_5_KING_BOMB):
        moves = moves + mg.gen_type_4_bomb() + mg.gen_type_5_king_bomb()

    if len(rival_move) != 0:
        moves = moves + [[]]

    for m in moves:
        m.sort()
    return moves


# ---------------------------------------------------------------------------
# InfoSet construction
# ---------------------------------------------------------------------------

def _all_54_cards() -> list:
    cards = []
    for rank in range(3, 15):
        cards.extend([rank] * 4)
    cards.extend([17] * 4)
    cards.append(20)
    cards.append(30)
    return cards


def _multiset_subtract(base: list, remove: list) -> list:
    c = Counter(base)
    for v in remove:
        if c[v] > 0:
            c[v] -= 1
    result = []
    for v, count in c.items():
        result.extend([v] * count)
    result.sort()
    return result


def build_infoset(data: dict) -> InfoSet:
    position = data["position"]
    hand = sorted(data["hand"])
    bottom_cards = sorted(data.get("bottom_cards") or [])
    must_play: bool = data.get("must_play", True)
    last_move: list = sorted(data.get("last_move") or [])
    last_move_pos: str = data.get("last_move_position") or ""
    num_cards_left: dict = data.get("num_cards_left") or {}

    played = {
        "landlord":      sorted(data.get("played_cards_landlord") or []),
        "landlord_down": sorted(data.get("played_cards_landlord_down") or []),
        "landlord_up":   sorted(data.get("played_cards_landlord_up") or []),
    }

    raw_seq = data.get("card_play_action_seq") or []
    action_seq = [sorted(m) if m else [] for m in raw_seq]

    all_played = played["landlord"] + played["landlord_down"] + played["landlord_up"]
    other_hand_cards = _multiset_subtract(
        _multiset_subtract(_all_54_cards(), hand),
        all_played,
    )

    non_empty = [m for m in action_seq if m]
    last_two = [non_empty[-1] if len(non_empty) >= 1 else [],
                non_empty[-2] if len(non_empty) >= 2 else []]

    rival_move = [] if must_play else last_move
    legal = get_legal_card_play_actions(hand, rival_move)
    if not must_play and [] not in legal:
        legal.append([])

    infoset = InfoSet(position)
    infoset.player_hand_cards = hand
    infoset.num_cards_left_dict = num_cards_left
    infoset.three_landlord_cards = bottom_cards
    infoset.card_play_action_seq = action_seq
    infoset.other_hand_cards = other_hand_cards
    infoset.legal_actions = legal
    infoset.played_cards = played
    infoset.last_move = last_move
    infoset.last_two_moves = last_two
    infoset.num_cards_left = num_cards_left
    infoset.bomb_num = 0
    infoset.last_pid = last_move_pos
    infoset.last_move_dict = {p: [] for p in POSITIONS}
    infoset.all_handcards = {position: hand}

    if last_move_pos and last_move:
        infoset.last_move_dict[last_move_pos] = last_move

    return infoset


# ---------------------------------------------------------------------------
# HTTP server
# ---------------------------------------------------------------------------

class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass

    def do_GET(self):
        if self.path == "/health":
            self._json(200, {"status": "ok", "agents": list(sessions.keys())})
        else:
            self.send_error(404)

    def do_POST(self):
        if self.path != "/decide_play":
            self.send_error(404)
            return
        try:
            length = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(length))
            infoset = build_infoset(body)
            position = body["position"]

            if not infoset.legal_actions:
                action = []
            else:
                action = _act(position, infoset)
                action = list(action) if action is not None else []

            self._json(200, {"action": action})
        except Exception:
            traceback.print_exc()
            err = traceback.format_exc().splitlines()[-1]
            self._json(500, {"action": [], "error": err})

    def _json(self, code: int, obj: dict) -> None:
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    print("Downloading ONNX models from HuggingFace (if needed) ...")
    download_models()
    print(f"\nLoading ONNX sessions ...")
    load_sessions()
    server = HTTPServer(("0.0.0.0", PORT), Handler)
    print(f"\nDouZero service listening on port {PORT}")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nDouZero service stopped.")
        server.server_close()
