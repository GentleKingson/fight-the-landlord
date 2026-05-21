package ai

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
)

func buildBasicPrompt() string {
	return `# Role
你是一位精通概率学与博弈论的斗地主顶尖大师。你的目标是根据当前局势，做出最优的出牌决策，带领你的阵营（地主或农民）取得最终胜利。

# 基础规则与牌型定义
牌面大小严格按照：3 < 4 < 5 < 6 < 7 < 8 < 9 < T < J < Q < K < A < 2 < B < R （B=小王，R=大王，T=10）
你可以选择【不出 (PASS)】，或者打出以下合法牌型（必须与当前场上的牌型一致且刚好大于对方，或者用炸弹/王炸压制）：
- 单/对/三：1/2/3张同点数牌。
- 三带一/三带对：3张同点数 + 1张单牌或1对。
- 顺子：≥5张连续单牌，不含2和王。
- 连对：≥3组连续对子，不含2和王。
- 飞机：≥2组连续三张（如333444），可不带，或带同等数量的单牌/对子，不含2和王。
- 四带二：4张同点数 + 2张单牌或2组对子（注意：这不是炸弹，不能压制其他牌型）。
- 炸弹：4张同点数（可压制除更大炸弹和王炸外的所有牌）。
- 王炸：B+R（小王+大王，绝对的绝对最大）。

# 阵营与配合策略
- 【地主】：孤独求败。策略是隐藏弱点，消耗农民的大牌，掌控出牌权，优先打出容易被管上的小牌、长牌（顺子/飞机），最后留大牌或炸弹收尾。
- 【农民-地主上家（地主右侧）】：辅助与防守。策略是“顶牌”，哪怕拆散手牌也要用大牌（通常是A或K以上）阻止地主溜小牌，寻找机会把出牌权传给队友。
- 【农民-地主下家（地主左侧）】：主攻手。策略是尽量顺着队友的牌跑完手牌；如果队友打出大牌且地主不要，切忌管队友的牌。

# 高级出牌原则
1. **诚实出牌**：【绝对禁忌】你只能使用当前手牌（Hand）中真实拥有的牌，数量和点数必须严丝合缝，绝不能凭空创造牌！
2. **手牌整理原则**：出牌不是看单一牌型优先级，而是看**“打出这手牌后，剩下的牌是否更整齐”**。尽量减少单牌和散牌的产生。
3. **控场与大牌使用**：
   - 首发或掌握主动权时：优先出自己没有大牌回收的弱势牌型，或者出极长、极具破坏力的牌型（如长顺子）破坏对手手牌结构。
   - 炸弹与王炸：是战略核武器。在对手剩余牌数≤3张时必须果断使用拦截；在自己能“一波流”跑完手牌时果断使用；否则应尽量保留以威慑对手。
4. **灵活变通**：为了长远胜利，可以故意“不出(PASS)”让对手出牌，不要为了管牌而把自己的牌拆得稀烂（除非必须要阻止地主出完）。

# 输入输出规范
每次你需要决策时，我会提供当前对局状态。请你严格遵循以下【思维链】进行思考，最后按指定格式输出：
1. **局势分析**：我是什么身份？队友和对手还剩几张牌？当前场上需要管的牌是什么？（如果是主动出牌，目标是什么？）
2. **手牌解构**：我目前真实拥有的手牌是什么？可以组成哪些合理的牌型组合？
3. **策略抉择**：根据局势，我应该PASS、顶牌、顺牌，还是炸？列出2-3种可选方案，并选出最优解。
4. **最终输出**：只输出符合【牌型】之一的牌面，空格分隔如"3 3 K"，pass=不出，10写成T，禁止任何解释、标点或思考过程
`
}

func buildPlayPrompt(gctx GameContext) string {
	var sb strings.Builder

	if gctx.MustPlay {
		fmt.Fprintf(&sb, "现在轮到你自由出牌\n")
	}

	role := "农民"
	if gctx.IsLandlord {
		role = "地主"
	}

	fmt.Fprintf(&sb, "当前状态:\n角色:%s\n手牌(%d张):%s\n", role, len(gctx.Hand), cardsToStr(gctx.Hand))

	writePlayRecord(&sb, "上家", gctx.RecentPlays[0], gctx.IsLandlord, false)
	writePlayRecord(&sb, "上上家", gctx.RecentPlays[1], gctx.IsLandlord, true)

	fmt.Fprintf(&sb, "上家（%s）剩余：%d张，下家（%s）剩余：%d张\n",
		playerRoleLabel(gctx.PlayerRoles[0]), gctx.PlayerCounts[0],
		playerRoleLabel(gctx.PlayerRoles[1]), gctx.PlayerCounts[1])

	if len(gctx.RemainingCards) > 0 {
		type rankCount struct {
			rank  card.Rank
			count int
		}
		entries := make([]rankCount, 0, len(gctx.RemainingCards))
		for rank, count := range gctx.RemainingCards {
			entries = append(entries, rankCount{rank, count})
		}
		slices.SortFunc(entries, func(a, b rankCount) int { return cmp.Compare(a.rank, b.rank) })
		remParts := make([]string, len(entries))
		for i, e := range entries {
			remParts[i] = fmt.Sprintf("%s×%d", e.rank, e.count)
		}
		fmt.Fprintf(&sb, "底牌:%s\n", cardsToStr(gctx.BottomCards))
		fmt.Fprintf(&sb, "记牌器:%s\n", strings.Join(remParts, " "))
	}

	var actionStr string
	switch {
	case gctx.MustPlay:
		actionStr = "出什么?(自由出牌，禁止大牌/炸弹开局)"
	case gctx.CanBeat:
		if gctx.RecentPlays[0].Played.IsEmpty() {
			actionStr = "出什么?(有牌可打，压过上家)"
		} else {
			actionStr = fmt.Sprintf("出什么?(有牌可打，压过上家 %s)", cardsToStr(gctx.RecentPlays[0].Played.Cards))
		}
	default:
		actionStr = "出什么?(无牌可打，只能pass)"
	}
	fmt.Fprintf(&sb, "%s\n", actionStr)

	fmt.Fprintf(&sb, "只输出符合【牌型】之一的牌面，空格分隔如3 3 3 K，pass=不出，10写成T，禁止任何解释、标点或思考过程\n")

	return sb.String()
}

func writePlayRecord(sb *strings.Builder, label string, record PlayRecord, selfIsLandlord, lineBreak bool) {
	if record.Played.IsEmpty() {
		return
	}

	format := "%s(%s)出:%s"
	if lineBreak {
		format += "\n"
	} else {
		format = " " + format
	}

	fmt.Fprintf(sb, format,
		label,
		roleLabel(record, selfIsLandlord),
		cardsToStr(record.Played.Cards),
	)
}

func buildBidBasicPrompt() string {
	return `你是一位精通斗地主的牌手。现在处于叫地主阶段，请根据手牌强弱决定是否叫/抢地主。手牌越强（大牌多、炸弹多、对子多、顺子结构好）越应该叫。只回答 true 或 false，true=叫/抢，false=不叫/不抢，禁止任何解释。`
}

func buildBidPrompt(hand []card.Card, prevBid *bool) string {
	var prev string
	switch {
	case prevBid == nil:
		prev = "你是第一个叫地主的玩家"
	case *prevBid:
		prev = "上家已叫地主"
	default:
		prev = "上家没有叫地主"
	}
	return fmt.Sprintf("%s，你的手牌(%d张):%s\n是否叫/抢地主吗？直接回答true或false，不要任何解释和思考过程",
		prev, len(hand), cardsToStr(hand))
}

func playerRoleLabel(isLandlord bool) string {
	if isLandlord {
		return "地主"
	}
	return "农民"
}

// roleLabel 从当前机器人视角返回出牌者的角色标签
func roleLabel(p PlayRecord, selfIsLandlord bool) string {
	if p.IsLandlord {
		return "地主"
	}
	if selfIsLandlord {
		return "农民"
	}
	return "队友"
}

func cardsToStr(cards []card.Card) string {
	parts := make([]string, len(cards))
	for i, c := range cards {
		parts[i] = c.Rank.String()
	}
	return strings.Join(parts, " ")
}
