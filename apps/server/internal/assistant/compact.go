package assistant

import (
	"strings"
	"unicode/utf8"
)

// truncateTitle makes a conversation's title from its first question.
func truncateTitle(text string) string {
	t := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(t) <= titleFromQuestion {
		return t
	}
	cut := []rune(t)[:titleFromQuestion]
	i := len(cut)
	for i > titleFromQuestion/2 && cut[i-1] != ' ' {
		i--
	}
	return strings.TrimSpace(string(cut[:i])) + "…"
}
