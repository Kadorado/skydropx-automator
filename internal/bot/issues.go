package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"skydropx-automator/internal/skydropx"
)

// issueDays is how many days back open shipments are checked with /novedades.
const issueDays = 45

// maxMessage leaves room under Telegram's 4096-character message limit.
const maxMessage = 3800

// issueGroups are the blocks of the /novedades report, in order of urgency.
var issueGroups = []struct {
	kind, title, act string
}{
	{"devolucion", "❌ <b>En devolución o sin cobrar</b>", "Llama al cliente: a veces se puede reprogramar antes de que llegue de vuelta."},
	{"intento", "⚠️ <b>No pudieron entregar</b>", "Llama al cliente para que esté pendiente del reintento."},
	{"oficina", "🏢 <b>Para reclamar en oficina</b>", "Mira el historial: si ya llegó a la oficina de destino, avísale al cliente que lo recoja y pague ahí."},
	{"novedad", "🚨 <b>Con novedad</b>", "Revisa con el cliente o con la transportadora."},
}

// issuesCommand handles /novedades: checks open shipments and sends the ones that need attention.
func (b *Bot) issuesCommand(ctx context.Context, chatID int64) {
	b.say(ctx, chatID, "⏳ Revisando el rastreo de los envíos abiertos… puede tardar un minuto.")
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	since := time.Now().In(colombiaTZ).AddDate(0, 0, -issueDays)
	problems, checked, err := b.sky.Problems(ctx, since)
	if err != nil {
		if errors.Is(err, skydropx.ErrMockMode) {
			b.say(ctx, chatID, "Modo simulación: no hay envíos reales que revisar.")
			return
		}
		if len(problems) == 0 {
			b.say(ctx, chatID, "No se pudo revisar el rastreo: "+err.Error())
			return
		}
	}
	for _, msg := range issueMessages(problems, checked) {
		b.say(ctx, chatID, msg)
	}
}

// issueMessages builds the grouped report, split into messages that fit in Telegram.
func issueMessages(problems []skydropx.Problem, checked int) []string {
	if len(problems) == 0 {
		return []string{fmt.Sprintf("✅ <b>Sin novedades.</b> Revisé %d envíos abiertos de los últimos %d días y ninguno tiene problemas.", checked, issueDays)}
	}
	msgs := []string{fmt.Sprintf("📋 <b>Novedades para gestionar:</b> %d de %d envíos abiertos (últimos %d días)", len(problems), checked, issueDays)}
	for _, g := range issueGroups {
		var items []string
		for _, p := range problems {
			if p.Kind == g.kind {
				items = append(items, issueItem(p))
			}
		}
		if len(items) == 0 {
			continue
		}
		current := fmt.Sprintf("%s (%d)\n<i>%s</i>", g.title, len(items), g.act)
		for _, it := range items {
			if len(current)+len(it)+2 > maxMessage {
				msgs = append(msgs, current)
				current = g.title + " (continuación)"
			}
			current += "\n\n" + it
		}
		msgs = append(msgs, current)
	}
	return msgs
}

func issueItem(p skydropx.Problem) string {
	cust := p.Customer
	if cust == "" {
		cust = "Sin nombre"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "👤 <b>%s</b>", html.EscapeString(cust))
	if p.Phone != "" {
		fmt.Fprintf(&b, " · 📞 %s", html.EscapeString(p.Phone))
	}
	if p.City != "" {
		fmt.Fprintf(&b, " · 📍 %s", html.EscapeString(p.City))
	}
	fmt.Fprintf(&b, "\n📦 %s", html.EscapeString(p.Tracking))
	if len(p.History) > 0 {
		b.WriteString("\n🕒 <b>Últimos eventos:</b>")
		for _, ev := range p.History {
			desc := strings.TrimSpace(ev.Description)
			if desc == "" {
				desc = ev.Status
			}
			fmt.Fprintf(&b, "\n   • %s — %s", dayTime(ev.Date.In(colombiaTZ)), html.EscapeString(desc))
		}
	} else if ev := strings.TrimSpace(p.LastEvent); ev != "" {
		fmt.Fprintf(&b, "\n📝 %s", html.EscapeString(ev))
		if t, err := time.Parse(time.RFC3339Nano, p.LastDate); err == nil {
			fmt.Fprintf(&b, " (%s)", shortDay(t.In(colombiaTZ)))
		}
	}
	return b.String()
}

func dayTime(t time.Time) string {
	ampm := "a. m."
	if t.Hour() >= 12 {
		ampm = "p. m."
	}
	return fmt.Sprintf("%s %s %s", shortDay(t), t.Format("3:04"), ampm)
}
