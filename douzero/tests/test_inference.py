import os
from pathlib import Path
import random
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from server import build_infoset, get_legal_card_play_actions, _all_54_cards
from src.marvel_backend import build_infoset_string, MarvelBackend, POSITIONS


def opening():
    cards = _all_54_cards()
    random.Random(42).shuffle(cards)
    return cards, {"position": "landlord", "hand": sorted(cards[:20]), "bottom_cards": cards[17:20],
                   "card_play_action_seq": [], "num_cards_left": dict(zip(POSITIONS, [20, 17, 17])),
                   "must_play": True}


class ObservationTests(unittest.TestCase):
    def test_infoset_initial_hand_excludes_bottom_and_opponents(self):
        _, data = opening()
        text = build_infoset_string(data)
        private, public = text.split("|")
        self.assertEqual(len(private.split("+")[1]), 17)
        self.assertEqual(len(public.split("+")[1]), 3)

    def test_rejects_wrong_turn_and_missing_history(self):
        _, data = opening()
        data["position"] = "landlord_up"
        with self.assertRaises(ValueError): build_infoset_string(data)
        data["position"] = "landlord"
        data["hand"].pop()
        with self.assertRaises(ValueError): build_infoset_string(data)

    def test_pass_history_and_bombs_are_preserved_for_onnx(self):
        _, data = opening()
        data["card_play_action_seq"] = [[3, 3, 3, 3], [], []]
        info = build_infoset(data)
        self.assertEqual(info.bomb_num, 1)
        self.assertEqual(info.last_two_moves, [[], []])
        self.assertEqual(info.last_move_dict["landlord"], [3]*4)
        self.assertEqual(info.last_move_dict["landlord_down"], [])

    def test_farmers_share_policy(self):
        class Engine:
            def act_batch(self, **kwargs): return ["PASS"]
        model = MarvelBackend.__new__(MarvelBackend)
        model.engines = {"farmers": Engine()}
        cards, data = opening()
        data.update(position="landlord_down", hand=cards[20:37], card_play_action_seq=[[cards[0]]])
        self.assertEqual(model.act(data), [])
        data.update(position="landlord_up", hand=cards[37:], card_play_action_seq=[[cards[0]], []])
        self.assertEqual(model.act(data), [])


@unittest.skipUnless(os.environ.get("MARVEL_INTEGRATION") == "1", "requires pinned source and role weights")
class RealCheckpointTests(unittest.TestCase):
    def test_complete_game_with_both_uploaded_policies(self):
        model = MarvelBackend(Path(__file__).resolve().parents[1] / "models")
        cards, data = opening()
        hands = [sorted(cards[:20]), sorted(cards[20:37]), sorted(cards[37:])]
        history, played, last, last_seat = [], [[], [], []], [], 0
        used_roles = set()
        for turn in range(180):
            seat = turn % 3
            must_play = not last or last_seat == seat or (len(history) >= 2 and history[-2:] == [[], []])
            data.update(position=POSITIONS[seat], hand=hands[seat], card_play_action_seq=history,
                        num_cards_left={p: len(hands[i]) for i, p in enumerate(POSITIONS)},
                        last_move=last, last_move_position=POSITIONS[last_seat], must_play=must_play)
            data.update({f"played_cards_{p}": played[i] for i, p in enumerate(POSITIONS)})
            action = model.act(data)
            self.assertIn(action, get_legal_card_play_actions(hands[seat], [] if must_play else last))
            used_roles.add(POSITIONS[seat])
            for card in action: hands[seat].remove(card)
            played[seat].extend(action)
            history.append(action)
            if action: last, last_seat = action, seat
            if not hands[seat]: break
        else: self.fail("game did not finish")
        self.assertEqual(used_roles, set(POSITIONS))


if __name__ == "__main__": unittest.main()
