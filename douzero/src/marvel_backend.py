"""Adapter for GentleKingson/doudizhu MARVEL v1.3 role checkpoints.

Uses the original model, nanopokerlib encoder and direct-policy decoder. No
observation tensors are guessed from weight shapes. MARVEL_ROOT must point to
the pinned source installed by scripts/setup-marvel.sh or Dockerfile.marvel.
"""
from collections import Counter
import hashlib
import os
from pathlib import Path
import sys

POSITIONS = ("landlord", "landlord_down", "landlord_up")
CODES = ("L", "P1", "P2")
RANKS = dict(zip([*range(3, 15), 17, 20, 30], "3456789TJQKA2BR"))


def cards_to_string(cards):
    return "".join(RANKS[c] for c in sorted(cards)) or "PASS"


def build_infoset_string(data):
    """Only own private cards and public history; never sample opponent hands."""
    position = POSITIONS.index(data["position"])
    history = data.get("card_play_action_seq") or []
    if len(history) % 3 != position:
        raise ValueError("history does not end at the requested player's turn")
    bottom = data.get("bottom_cards") or []
    if len(bottom) != 3:
        raise ValueError("MARVEL requires all three public landlord cards")
    initial = Counter(data["hand"])
    for move in history[position::3]:
        initial.update(move)
    if position == 0:
        initial.subtract(bottom)
    if any(n < 0 for n in initial.values()) or sum(initial.values()) != 17:
        raise ValueError("inconsistent hand, bottom cards or action history")
    segments = [f"{CODES[position]}+{cards_to_string(list(initial.elements()))}",
                f"L+{cards_to_string(bottom)}"]
    segments.extend(f"{CODES[i % 3]}-{cards_to_string(move or [])}" for i, move in enumerate(history))
    return "|".join(segments)


class MarvelBackend:
    def __init__(self, model_dir):
        root = Path(os.environ.get("MARVEL_ROOT", Path(__file__).resolve().parents[1] / "vendor" / "doudizhu"))
        if not (root / "marvel" / "model").is_dir():
            raise RuntimeError("MARVEL source missing; run scripts/setup-marvel.sh or use Dockerfile.marvel")
        sys.path.insert(0, str(root))
        import torch
        from marvel.config import deserialize_train_config
        from marvel.model import Agent
        from kagami.connectors.marvel import MarvelModel
        from kagami.games import DoudizhuInferenceEngine, DoudizhuInferenceEngineConfig

        torch.set_num_threads(max(1, int(os.environ.get("TORCH_NUM_THREADS", "2"))))
        self.engines = {}
        self.metadata = {}
        for role in ("landlord", "farmers"):
            path = Path(model_dir) / f"{role}.pt"
            checkpoint = torch.load(path, map_location="cpu", weights_only=True)
            cfg = deserialize_train_config(checkpoint["train_config"])
            if cfg is None or cfg.game_name != "doudizhu" or cfg.model.architecture != "v1.3":
                raise ValueError(f"Unsupported checkpoint configuration: {path.name}")
            agent = Agent(cfg)
            agent.load_state_dict(checkpoint["agent_state_dict"], strict=True)
            agent.eval().requires_grad_(False)
            model = MarvelModel(_agent=agent, _game=cfg.game, _device=torch.device("cpu"))
            self.engines[role] = DoudizhuInferenceEngine(
                policy_model=model, game=cfg.game,
                cfg=DoudizhuInferenceEngineConfig(action_temp=0.0))
            with path.open("rb") as weight_file:
                digest = hashlib.file_digest(weight_file, "sha256").hexdigest()
            self.metadata[role] = {"file": path.name, "sha256": digest,
                                   "architecture": "v1.3", "iteration": checkpoint.get("iteration")}

    def act(self, data):
        role = "landlord" if data["position"] == "landlord" else "farmers"
        action = self.engines[role].act_batch(infoset_strs=[build_infoset_string(data)])[0]
        if action == "PASS":
            return []
        reverse = {symbol: rank for rank, symbol in RANKS.items()}
        return sorted(reverse[symbol] for symbol in action)
