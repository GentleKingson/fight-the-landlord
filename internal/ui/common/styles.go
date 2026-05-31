// Package common provides shared styles and utilities for the UI.
package common

import (
	"charm.land/lipgloss/v2"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
)

// Icon constants
const (
	LandlordIcon = "👑"
	// FarmerIcon 必须用单码点 emoji：ZWJ 序列（如 🧑‍🌾）会被部分终端渲染成 4 格宽，
	// 而 lipgloss 按 2 格计算，导致固定宽度信息框被顶破、角色图标错位甚至显示异常。
	FarmerIcon = "🌾"

	TopBorderStart    = "┌──"
	TopBorderEnd      = "┌──┐"
	SideBorder        = "│"
	BottomBorderStart = "└──"
	BottomBorderEnd   = "└──┘"
)

// Lipgloss Styles - shared across local and online modes
var (
	DocStyle     = lipgloss.NewStyle().Margin(1, 2)
	RedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#CD0000")).Background(lipgloss.Color("#FFFFFF")).Bold(true)
	BlackStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("#FFFFFF")).Bold(true)
	GrayStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Background(lipgloss.Color("#FFFFFF")).Bold(true)
	TitleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("228")).Bold(true).Render
	BoxStyle     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	PromptStyle  = lipgloss.NewStyle().MarginTop(1)
	ErrorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	DisplayOrder = []card.Rank{card.RankRedJoker, card.RankBlackJoker, card.Rank2, card.RankA, card.RankK, card.RankQ, card.RankJ, card.Rank10, card.Rank9, card.Rank8, card.Rank7, card.Rank6, card.Rank5, card.Rank4, card.Rank3}
)
