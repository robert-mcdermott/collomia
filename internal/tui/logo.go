package tui

import "strings"

// The wordmark and the mark are drawn in the same double rule the panels and
// the compact banner already use, so the splash reads as part of the interface
// rather than as a piece of art borrowed from somewhere else.

// compactLogoArt is the three-row wordmark. It heads the transcript once a
// session has started, where the logo is a label rather than a title page, and
// it stands in for the full splash on a terminal too narrow for it.
const compactLogoArt = `╔═╗╔═╗╦  ╦  ╔═╗╔╦╗╦╔═╗
║  ║ ║║  ║  ║ ║║║║║╠═╣
╚═╝╚═╝╩═╝╩═╝╚═╝╩ ╩╩╩ ╩`

// blossomArt is Collomia's mark: four petals around a shaded centre. It sits
// to the left of the wordmark rather than above it. A mark to the left is how
// the name is already written everywhere else in the interface — "✿ collo" in
// the status line, "✿ COLLOMIA" on every reply — and stacking it above would
// have pushed the card and the openers a further six rows down the first
// screen for no gain.
var blossomArt = padBlock(`   ╔══╗
╔══╝  ╚══╗
║   ▒▒   ║
╚══╗  ╔══╝
   ╚══╝`)

// wordmarkArt is the splash wordmark: the same letterforms as the compact
// banner, drawn at five rows so the first screen has a title rather than a
// caption.
var wordmarkArt = padBlock(`╔════ ╔═══╗ ║     ║     ╔═══╗ ╔╗   ╔╗ ═╦═ ╔═══╗
║     ║   ║ ║     ║     ║   ║ ║╚╗ ╔╝║  ║  ║   ║
║     ║   ║ ║     ║     ║   ║ ║ ╚═╝ ║  ║  ╠═══╣
║     ║   ║ ║     ║     ║   ║ ║     ║  ║  ║   ║
╚════ ╚═══╝ ╚════ ╚════ ╚═══╝ ║     ║ ═╩═ ║   ║`)

// splashLogoGap separates the mark from the wordmark.
const splashLogoGap = 2

// splashLogoWidth is what the mark, the gap, and the wordmark occupy together.
var splashLogoWidth = blockWidth(blossomArt) + splashLogoGap + blockWidth(wordmarkArt)

// padBlock squares a block off by padding every line to the widest. gradient
// blends across each line independently, so ragged lines finish the blend at
// different columns and the block comes out striped instead of raked.
func padBlock(art string) string {
	lines := strings.Split(art, "\n")
	width := 0
	for _, line := range lines {
		width = max(width, len([]rune(line)))
	}
	for i, line := range lines {
		lines[i] = line + strings.Repeat(" ", width-len([]rune(line)))
	}
	return strings.Join(lines, "\n")
}

// blockWidth is the column count of a squared-off block. The art is
// box-drawing and blocks only, one column per rune.
func blockWidth(art string) int {
	width := 0
	for _, line := range strings.Split(art, "\n") {
		width = max(width, len([]rune(line)))
	}
	return width
}

// joinBlocks sets squared-off blocks side by side, top aligned. The result is
// plain text so a gradient can still be raked across the whole assembly rather
// than restarted for each piece.
func joinBlocks(gap int, blocks ...string) string {
	split := make([][]string, len(blocks))
	height := 0
	for i, b := range blocks {
		split[i] = strings.Split(b, "\n")
		height = max(height, len(split[i]))
	}
	rows := make([]string, height)
	for r := range rows {
		var row strings.Builder
		for i, lines := range split {
			if i > 0 {
				row.WriteString(strings.Repeat(" ", gap))
			}
			if r < len(lines) {
				row.WriteString(lines[r])
				continue
			}
			row.WriteString(strings.Repeat(" ", blockWidth(blocks[i])))
		}
		rows[r] = row.String()
	}
	return strings.Join(rows, "\n")
}
