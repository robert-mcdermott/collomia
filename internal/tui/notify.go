package tui

import (
	"strings"
	"unicode/utf8"
)

// alert gets the user's attention for an approval, a question, or a finished
// long turn, honoring options.notifications:
//
//	"on" (default) — terminal bell plus a desktop notification (OSC 9)
//	"bell"         — terminal bell only
//	"off"          — silent
//
// Terminals decide how each surfaces: most only show OSC 9 notifications when
// the window is unfocused, and map the bell to their configured sound/badge.
func (m *Model) alert(text string) {
	switch strings.ToLower(m.runtime.Config.Options.Notifications) {
	case "off":
	case "bell":
		ring()
	default:
		ring()
		notify(text)
	}
}

// notify posts a desktop notification through the hosting terminal via OSC 9
// (supported by iTerm2, WezTerm, Ghostty, kitty, Windows Terminal, and
// others; unsupported terminals ignore the sequence). emitOSC handles the
// tmux passthrough envelope and skips non-terminal stdout.
func notify(text string) {
	emitOSC("\x1b]9;" + notifyText(text) + "\x07")
}

// notifyText strips control characters (which would terminate or corrupt the
// OSC sequence) and bounds the message length.
func notifyText(text string) string {
	text = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, text)
	const limit = 120
	if utf8.RuneCountInString(text) > limit {
		runes := []rune(text)
		text = string(runes[:limit-1]) + "…"
	}
	return text
}
