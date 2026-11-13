package parser

import (
	"regexp"
	"strings"
	"unicode"
)

// Marcas de formato de WhatsApp: *negrita*, _cursiva_, ~tachado~ y `código`.
var reWhatsAppMarks = regexp.MustCompile("[*_~`]")

// Líneas decorativas (━━━, ───, ===, ---) que no llevan datos.
var reDecorativeLine = regexp.MustCompile(`^[\s━─═\-=_.]+$`)

// normalizeWhatsApp quita el formato y los adornos de WhatsApp para que las
// reglas de lectura vean líneas como "Receptor: NOMBRE" aunque vengan como
// "*Receptor:* NOMBRE" o "📦 *Receptor:* NOMBRE".
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
