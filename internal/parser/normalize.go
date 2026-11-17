package parser

import (
	"regexp"
	"strings"
	"unicode"
)

// WhatsApp formatting marks: *bold*, _italic_, ~strikethrough~ and `code`.
var reWhatsAppMarks = regexp.MustCompile("[*_~`]")

// Decorative lines (━━━, ───, ===, ---) that carry no data.
var reDecorativeLine = regexp.MustCompile(`^[\s━─═\-=_.]+$`)

// normalizeWhatsApp strips WhatsApp formatting and decorations so the
// parsing rules see lines like "Receptor: NOMBRE" even if they come as
// "*Receptor:* NOMBRE" or "📦 *Receptor:* NOMBRE".
func normalizeWhatsApp(raw string) string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = reWhatsAppMarks.ReplaceAllString(line, "")
		line = strings.TrimLeftFunc(line, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		line = strings.TrimSpace(line)
		if line == "" || reDecorativeLine.MatchString(line) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
