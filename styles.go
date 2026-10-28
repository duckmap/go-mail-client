package main

import "github.com/charmbracelet/lipgloss"

// Палитра: нейтральный терминал + один тёплый акцент — как у агентных CLI.
var (
	cAccent = lipgloss.AdaptiveColor{Light: "#b45309", Dark: "#dfa172"}
	cText   = lipgloss.AdaptiveColor{Light: "#1f2328", Dark: "#d6d6d6"}
	cDim    = lipgloss.AdaptiveColor{Light: "#5c636a", Dark: "#909090"}
	cFaint  = lipgloss.AdaptiveColor{Light: "#78818a", Dark: "#6b6b6b"}
	cRule   = lipgloss.AdaptiveColor{Light: "#c9d1d9", Dark: "#3a3a3a"}
	cGreen  = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#7ee787"}
	cYellow = lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#e3b341"}
	cRed    = lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#ff7b72"}
	cSelBg  = lipgloss.AdaptiveColor{Light: "#efe9df", Dark: "#2c2c2c"}
)

var (
	stText   = lipgloss.NewStyle().Foreground(cText)
	stBold   = lipgloss.NewStyle().Foreground(cText).Bold(true)
	stDim    = lipgloss.NewStyle().Foreground(cDim)
	stFaint  = lipgloss.NewStyle().Foreground(cFaint)
	stRule   = lipgloss.NewStyle().Foreground(cRule)
	stAccent = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	stAcc    = lipgloss.NewStyle().Foreground(cAccent)

	stError  = lipgloss.NewStyle().Foreground(cRed).Bold(true)
	stStatus = lipgloss.NewStyle().Foreground(cGreen)
	stOK     = lipgloss.NewStyle().Foreground(cGreen)
	stStar   = lipgloss.NewStyle().Foreground(cYellow)
	stWarn   = lipgloss.NewStyle().Foreground(cYellow)

	// выбранная строка
	stSel     = lipgloss.NewStyle().Background(cSelBg).Foreground(cText).Bold(true)
	stSelSoft = lipgloss.NewStyle().Background(cSelBg).Foreground(cText)

	// заголовки колонок
	stColTitle      = lipgloss.NewStyle().Foreground(cDim).Bold(true)
	stColTitleFocus = lipgloss.NewStyle().Foreground(cAccent).Bold(true)

	// диалоги
	stOverlay = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cRule).
			Padding(1, 2).
			Foreground(cText)

	stOverlayTitle = lipgloss.NewStyle().
			Foreground(cAccent).
			Bold(true).
			MarginBottom(1)

	// значения полей форм
	stFieldVal = lipgloss.NewStyle().
			Foreground(cText).
			PaddingLeft(1).
			PaddingRight(1)

	stFieldValFocused = lipgloss.NewStyle().
				Background(cSelBg).
				Foreground(cText).
				Bold(true).
				PaddingLeft(1).
				PaddingRight(1)
)

// truncate сокращает строку до width видимых символов с многоточием.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}

// padRight дополняет строку пробелами до ширины width.
func padRight(s string, width int) string {
	n := lipgloss.Width(s)
	if n >= width {
		return s
	}
	return s + spaces(width-n)
}

// padLeft выравнивает строку по правому краю.
func padLeft(s string, width int) string {
	n := lipgloss.Width(s)
	if n >= width {
		return s
	}
	return spaces(width-n) + s
}

// fitLine обрезает строку до width и добивает пробелами (ANSI-безопасно).
func fitLine(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) > width {
		s = lipgloss.NewStyle().MaxWidth(width).Render(s)
	}
	return padRight(s, width)
}

// lineAt возвращает i-ю строку колонки, дополненную до width.
func lineAt(lines []string, i, width int) string {
	if i < 0 || i >= len(lines) {
		return spaces(width)
	}
	return fitLine(lines[i], width)
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}
