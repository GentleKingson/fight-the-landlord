// Package common provides shared utilities for the UI.
package common

import (
	"fmt"
	"strings"
	"unicode"
)

// TruncateName truncates a player name to the specified maximum length.
func TruncateName(name string, maxLen int) string {
	runes := []rune(name)
	if len(runes) > maxLen {
		return string(runes[:maxLen-1]) + "…"
	}
	return name
}

// EscapeTerminalText makes untrusted control and bidirectional formatting
// characters visible instead of allowing them to alter terminal state.
func EscapeTerminalText(value string) string {
	var escaped strings.Builder
	escaped.Grow(len(value))
	for _, r := range value {
		if unicode.IsControl(r) || isTerminalBidiControl(r) {
			_, _ = fmt.Fprintf(&escaped, `\u{%X}`, r)
			continue
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}

func isTerminalBidiControl(r rune) bool {
	switch r {
	case '\u061C', '\u200E', '\u200F',
		'\u202A', '\u202B', '\u202C', '\u202D', '\u202E',
		'\u2066', '\u2067', '\u2068', '\u2069',
		'\u206A', '\u206B', '\u206C', '\u206D', '\u206E', '\u206F':
		return true
	default:
		return false
	}
}
