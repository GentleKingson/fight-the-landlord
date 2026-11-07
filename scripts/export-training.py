#!/usr/bin/env python3
"""Export validated completed games as public observations + human actions.

Output uses the same request fields as the AI service, so downstream trainers can
generate legal actions / MARVEL infosets without access to opponent private hands.
"""
import argparse
from collections import Counter
import json
from pathlib import Path
import sys

POSITIONS = ("landlord", "landlord_down", "landlord_up")


def samples(path):
    records = [json.loads(line) for line in path.read_text().splitlines()]
    if not records or records[-1].get("status") != "completed":
        return []
    deal, end = records[0], records[-1]
    if deal["type"] != "deal" or end["type"] != "end":
        raise ValueError("missing deal/end record")
    for seq, record in enumerate(records):
        if record["schema_version"] != 1 or record["seq"] != seq or record["game_id"] != deal["game_id"]:
            raise ValueError("record version, sequence or game ID mismatch")
    hands = [Counter(p["hand"]) for p in deal["players"]]
    bottom = deal["bottom_cards"]
    full_deck = sum(hands, Counter()) + Counter(bottom)
    if len(hands) != 3 or full_deck != Counter([r for r in range(3, 15) for _ in range(4)] + [17]*4 + [20, 30]):
        raise ValueError("invalid deal")
    landlord = None
    history, played, output = [], [[], [], []], []
    for record in records[1:-1]:
        kind = record["type"]
        if kind == "landlord":
            if landlord is not None: raise ValueError("duplicate landlord")
            landlord = record["seat"]
            hands[landlord].update(bottom)
            continue
        if kind == "bid": continue
        if kind not in ("play", "pass") or landlord is None:
            raise ValueError("unexpected event order")
        seat = record["seat"]
        relative = (seat - landlord) % 3
        action = record["action"]
        if relative != len(history) % 3 or Counter(record["hand"]) != +hands[seat]:
            raise ValueError("turn/hand mismatch")
        if Counter(action) - hands[seat]: raise ValueError("action not in hand")
        if kind == "pass" and (action or record["must_play"]): raise ValueError("invalid pass")
        if kind == "play" and not action: raise ValueError("empty play")
        if record["source"] == "human":
            if deal["players"][seat]["is_bot"]: raise ValueError("bot labelled as human")
            observation = {
                "position": POSITIONS[relative], "hand": sorted(hands[seat].elements()),
                "bottom_cards": bottom, "card_play_action_seq": list(history),
                "num_cards_left": {p: sum(hands[(landlord+i)%3].values()) for i, p in enumerate(POSITIONS)},
                "last_move": record["last_move"], "must_play": record["must_play"],
                "last_move_position": POSITIONS[(record["last_seat"]-landlord)%3],
            }
            for i, p in enumerate(POSITIONS): observation[f"played_cards_{p}"] = list(played[i])
            output.append({"schema_version": 1, "game_id": deal["game_id"], "seq": record["seq"],
                           "observation": observation, "action": sorted(action),
                           "reward": end["rewards"][seat], "score": end["scores"][seat],
                           "source": "human", "elapsed_ms": record["elapsed_ms"]})
        hands[seat].subtract(action)
        played[relative].extend(action)
        history.append(action)
    if landlord != end["landlord_seat"] or not history or sum(hands[end["winner_seat"]].values()) != 0:
        raise ValueError("invalid terminal result")
    return output


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if not args.directory.is_dir(): parser.error("input directory does not exist")
    paths = sorted(args.directory.rglob("*.jsonl"))
    if args.output.resolve() in [p.resolve() for p in paths]: parser.error("output must not overwrite an input game")
    count = skipped = 0
    with args.output.open("w") as out:
        for path in paths:
            try: rows = samples(path)
            except (ValueError, KeyError, IndexError, TypeError) as exc:
                skipped += 1
                print(f"Skipping invalid game {path.name}: {exc}", file=sys.stderr)
                continue
            for row in rows:
                out.write(json.dumps(row, separators=(",", ":")) + "\n")
                count += 1
    print(f"Exported {count} human decisions; {skipped} invalid games skipped", file=sys.stderr)


if __name__ == "__main__":
    main()
