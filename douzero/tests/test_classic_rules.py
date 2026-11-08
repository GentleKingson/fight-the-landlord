import json
from collections import Counter
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from server import get_legal_card_play_actions
from src.move_detector import get_move_type
from src.move_generator import MovesGener

FIXTURES = json.loads((Path(__file__).resolve().parents[2] / 'testdata/classic-rules.json').read_text())
TYPES = ['不出', '单张', '对子', '三张', '炸弹', '王炸', '三带一', '三带二',
         '顺子', '连对', '飞机', '飞机带单', '飞机带对', '四带二', '四带两对', '无效']


def rank(value):
    return {15: 17, 16: 20, 17: 30}.get(value, value)


def cards(values):
    return [rank(value) for value in values]


HANDS = {fixture['id']: cards(fixture['cards']) for fixture in FIXTURES['hands']}


class ClassicRuleTests(unittest.TestCase):
    def test_shared_classification(self):
        for fixture in FIXTURES['hands']:
            with self.subTest(fixture['id']):
                for hand in (cards(fixture['cards']), cards(fixture['cards'])[::-1]):
                    result = get_move_type(hand)
                    # Python represents [] as pass; Go/Web reject it as a played hand.
                    expected = fixture['type'] if hand else '不出'
                    self.assertEqual(TYPES[result['type']], expected)
                    if expected not in ('无效', '不出', '王炸'):
                        self.assertEqual(result['rank'], rank(fixture['keyRank']))
                        self.assertEqual(result.get('len', 0), fixture['length'])

    def test_shared_comparisons_through_legal_actions(self):
        for fixture in FIXTURES['comparisons']:
            with self.subTest(fixture):
                hand, target = HANDS[fixture['play']], HANDS[fixture['target']]
                if not hand or not target:
                    continue  # pass and fresh-trick semantics differ from hand comparison
                actions = get_legal_card_play_actions(hand, target)
                self.assertEqual(sorted(hand) in actions, fixture['canBeat'])

    def test_shared_replies_and_generator(self):
        for fixture in FIXTURES['replies']:
            with self.subTest(fixture['id']):
                hand, target = cards(fixture['hand']), HANDS[fixture['target']]
                actions = get_legal_card_play_actions(hand, target)
                self.assertEqual(any(actions), fixture['canBeat'])
                if fixture['canBeat']:
                    self.assertTrue(any(TYPES[get_move_type(action)['type']] == fixture['type'] for action in actions))
                for action in actions + MovesGener(hand).gen_moves():
                    self.assertNotEqual(TYPES[get_move_type(action)['type']], '无效', action)
                    self.assertFalse(Counter(action) - Counter(hand))

    def test_generator_excludes_unconfirmed_wing_extensions(self):
        for name in ('two_quads', 'quad_pair_wings', 'body_fourth_wing', 'triple_single_wings', 'quad_single_wings'):
            with self.subTest(name):
                hand = HANDS[name]
                self.assertNotIn(hand, get_legal_card_play_actions(hand, []))
                self.assertNotIn(hand, [sorted(move) for move in MovesGener(hand).gen_moves()])


if __name__ == '__main__':
    unittest.main()
