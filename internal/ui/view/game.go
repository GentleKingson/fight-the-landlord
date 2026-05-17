// Package view provides UI rendering functions.
package view

import (
	"charm.land/lipgloss/v2"

	"github.com/palemoky/fight-the-landlord/internal/ui/common"
)

// Re-export styles for use in this package
var (
	BoxStyle    = common.BoxStyle
	RedStyle    = common.RedStyle
	BlackStyle  = common.BlackStyle
	TitleStyle  = common.TitleStyle
	PromptStyle = common.PromptStyle
)

// Icons
const (
	LandlordIcon = common.LandlordIcon
	FarmerIcon   = common.FarmerIcon
)

// RenderGameRules renders the game rules.
func RenderGameRules() string {
	var sb string

	sb += "【游戏目标】\n"
	sb += "地主：先出完手中所有牌\n"
	sb += "农民：任意一个农民先出完牌，则农民方获胜\n\n"

	sb += "【牌型说明】\n"
	sb += "• 单牌：任意一张牌\n"
	sb += "• 对子：两张点数相同的牌\n"
	sb += "• 三张：三张点数相同的牌\n"
	sb += "• 三带一：三张 + 单牌\n"
	sb += "• 三带二：三张 + 对子\n"
	sb += "• 顺子：五张或更多连续的牌（2和王不能在顺子中）\n"
	sb += "• 连对：三对或更多连续的对子\n"
	sb += "• 飞机：两个或更多连续的三张\n"
	sb += "• 四带二：四张 + 两张单牌或两个对子\n"
	sb += "• 炸弹：四张点数相同的牌（可炸任何牌型）\n"
	sb += "• 王炸：大王 + 小王（最大的牌型）\n\n"

	sb += "【叫地主规则】\n"
	sb += "1. 发牌后每位玩家依次选择是否叫地主\n"
	sb += "2. 如果有人叫地主，该玩家成为地主\n"
	sb += "3. 地主获得3张底牌，共20张牌\n"
	sb += "4. 农民各17张牌\n\n"

	sb += "【出牌规则】\n"
	sb += "1. 地主先出牌\n"
	sb += "2. 后续玩家必须出相同牌型且更大的牌，或选择PASS\n"
	sb += "3. 如果都PASS，则最后出牌的玩家可以出任意牌型\n"
	sb += "4. 炸弹和王炸可以压任何牌型\n\n"

	sb += "【快捷键】\n"
	sb += "• C：切换记牌器（游戏中）\n"
	sb += "• T：切换快捷消息（游戏中）\n"
	sb += "• H：显示/隐藏帮助（游戏中）\n"
	sb += "• ESC：返回上一级或退出\n"

	return BoxStyle.Render(sb)
}

// RulesView renders the full rules view.
func RulesView(width, height int) string {
	var sb string

	title := TitleStyle("📖 游戏规则")
	sb += lipgloss.PlaceHorizontal(width, lipgloss.Center, title)
	sb += "\n\n"

	rules := RenderGameRules()
	sb += lipgloss.PlaceHorizontal(width, lipgloss.Center, rules)
	sb += "\n\n"

	return sb
}
