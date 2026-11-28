"""Filter generated moves against a rival of the same hand type."""

from collections import Counter


def filter_by_rank(moves, rival_move):
    return [move for move in moves if move[0] > rival_move[0]]


def filter_by_sorted_rank(moves, rival_move, rank_index):
    rival_move.sort()
    rival_rank = rival_move[rank_index]
    result = []
    for move in moves:
        move.sort()
        if move[rank_index] > rival_rank:
            result.append(move)
    return result


def filter_triple_wings(moves, rival_move):
    rival_rank = max(rank for rank, count in Counter(rival_move).items() if count == 3)
    return [
        move
        for move in moves
        if max(rank for rank, count in Counter(move).items() if count == 3) > rival_rank
    ]


def filter_type_14_4_22(moves, rival_move):
    rival = Counter(rival_move)
    rival_rank = my_rank = 0
    for k, v in rival.items():
        if v == 4:
            rival_rank = k
    new_moves = list()
    for move in moves:
        mymove = Counter(move)
        for k, v in mymove.items():
            if v == 4:
                my_rank = k
        if my_rank > rival_rank:
            new_moves.append(move)
    return new_moves
