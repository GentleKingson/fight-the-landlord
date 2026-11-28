"""Classic-rule legal actions shared by HTTP inference and regression tests."""

from . import move_detector as md, move_selector as ms, utils
from .move_generator import MovesGener


def get_legal_card_play_actions(hand: list, rival_move: list) -> list:
    mg = MovesGener(hand)
    rival_type = md.get_move_type(rival_move)
    rival_move_type = rival_type["type"]
    rival_move_len = rival_type.get("len", 1)
    moves = []

    if rival_move_type == utils.TYPE_0_PASS:
        moves = mg.gen_moves()
    elif rival_move_type == utils.TYPE_1_SINGLE:
        moves = ms.filter_by_rank(mg.gen_type_1_single(), rival_move)
    elif rival_move_type == utils.TYPE_2_PAIR:
        moves = ms.filter_by_rank(mg.gen_type_2_pair(), rival_move)
    elif rival_move_type == utils.TYPE_3_TRIPLE:
        moves = ms.filter_by_rank(mg.gen_type_3_triple(), rival_move)
    elif rival_move_type == utils.TYPE_4_BOMB:
        moves = ms.filter_by_rank(
            mg.gen_type_4_bomb() + mg.gen_type_5_king_bomb(), rival_move
        )
    elif rival_move_type == utils.TYPE_5_KING_BOMB:
        moves = []
    elif rival_move_type == utils.TYPE_6_3_1:
        moves = ms.filter_by_sorted_rank(mg.gen_type_6_3_1(), rival_move, 1)
    elif rival_move_type == utils.TYPE_7_3_2:
        moves = ms.filter_by_sorted_rank(mg.gen_type_7_3_2(), rival_move, 2)
    elif rival_move_type == utils.TYPE_8_SERIAL_SINGLE:
        moves = ms.filter_by_rank(
            mg.gen_type_8_serial_single(repeat_num=rival_move_len), rival_move
        )
    elif rival_move_type == utils.TYPE_9_SERIAL_PAIR:
        moves = ms.filter_by_rank(
            mg.gen_type_9_serial_pair(repeat_num=rival_move_len), rival_move
        )
    elif rival_move_type == utils.TYPE_10_SERIAL_TRIPLE:
        moves = ms.filter_by_rank(
            mg.gen_type_10_serial_triple(repeat_num=rival_move_len), rival_move
        )
    elif rival_move_type == utils.TYPE_11_SERIAL_3_1:
        moves = ms.filter_triple_wings(
            mg.gen_type_11_serial_3_1(repeat_num=rival_move_len), rival_move
        )
    elif rival_move_type == utils.TYPE_12_SERIAL_3_2:
        moves = ms.filter_triple_wings(
            mg.gen_type_12_serial_3_2(repeat_num=rival_move_len), rival_move
        )
    elif rival_move_type == utils.TYPE_13_4_2:
        moves = ms.filter_by_sorted_rank(mg.gen_type_13_4_2(), rival_move, 2)
    elif rival_move_type == utils.TYPE_14_4_22:
        moves = ms.filter_type_14_4_22(mg.gen_type_14_4_22(), rival_move)

    if rival_move_type not in (
        utils.TYPE_0_PASS,
        utils.TYPE_4_BOMB,
        utils.TYPE_5_KING_BOMB,
    ):
        moves = moves + mg.gen_type_4_bomb() + mg.gen_type_5_king_bomb()

    if len(rival_move) != 0:
        moves = moves + [[]]

    for m in moves:
        m.sort()
    return moves


def legal_actions_for_request(data: dict) -> list:
    hand = sorted(data["hand"])
    must_play = data.get("must_play", True)
    rival_move = [] if must_play else sorted(data.get("last_move") or [])
    legal = get_legal_card_play_actions(hand, rival_move)
    if not must_play and [] not in legal:
        legal.append([])
    return legal
