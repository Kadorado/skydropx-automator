package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"sort"
	"strings"
	"time"

	"skydropx-automator/internal/skydropx"
)

// pendingGroups are the blocks of the /pendientes report, most urgent first; the key is what
// PendingCODState answers.
var pendingGroups = []struct {
	state, title, act string
}{
	{"oficina", "🏢 <b>En oficina</b>", "Avísale al cliente que lo recoja y pague."},
	{"novedad", "🚨 <b>Con novedad</b>", "Revisa con el cliente o la transportadora antes de que se devuelva."},
	{"camino", "🚚 <b>En camino</b>", "Todavía no hay que hacer nada."},
}

// pendingCommand handles /pendientes: the shipments still to collect, grouped by where they are.
func (b *Bot) pendingCommand(ctx context.Context, chatID int64) {
	b.say(ctx, chatID, "⏳ Buscando lo que falta por cobrar y revisando el rastreo… puede tardar un minuto.")
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	now := time.Now().In(colombiaTZ)
	p, err := skydropx.NewPeriod(now.AddDate(0, 0, -issueDays).Format("2006-01-02"), now.Format("2006-01-02"))
	if err != nil {
		b.say(ctx, chatID, "No se pudo revisar: "+err.Error())
		return
	}
	f, err := b.sky.Finance(ctx, p, b.commissionRate())
	if err != nil {
		if errors.Is(err, skydropx.ErrMockMode) {
			b.say(ctx, chatID, "Modo simulación: no hay envíos reales que revisar.")
			return
		}
		b.say(ctx, chatID, "No se pudo revisar: "+err.Error())
		return
	}
	var pending []skydropx.ShipmentMoney
	states := map[string]string{}
	for _, r := range f.PerShipment {
		if r.Status == "pending" {
			pending = append(pending, r)
			states[r.Tracking] = b.sky.PendingCODState(ctx, r.Tracking, r.Carrier)
		}
	}
	for _, msg := range pendingMessages(pending, states, now) {
		b.say(ctx, chatID, msg)
	}
}

// pendingMessages builds the grouped report, oldest first in each group, split into messages that fit in Telegram.
func pendingMessages(pending []skydropx.ShipmentMoney, states map[string]string, now time.Time) []string {
	if len(pending) == 0 {
		return []string{fmt.Sprintf("✅ <b>No hay nada por cobrar</b> en los últimos %d días.", issueDays)}
	}
	var total float64
	for _, r := range pending {
		total += r.CustomerPays
	}
	sort.SliceStable(pending, func(i, j int) bool { return pending[i].Date < pending[j].Date })
	msgs := []string{fmt.Sprintf("⏳ <b>POR COBRAR:</b> %d envíos · %s\n<i>Envíos de los últimos %d días que la transportadora todavía no ha cobrado.</i>",
		len(pending), formatCOP(total), issueDays)}
	for _, g := range pendingGroups {
		var items []string
		var sum float64
		for _, r := range pending {
			st := states[r.Tracking]
			if st == "" {
				st = "camino"
			}
			if st == g.state {
				items = append(items, pendingItem(r, now))
				sum += r.CustomerPays
			}
		}
		if len(items) == 0 {
			continue
		}
		current := fmt.Sprintf("%s (%d · %s)\n<i>%s</i>", g.title, len(items), formatCOP(sum), g.act)
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

func pendingItem(r skydropx.ShipmentMoney, now time.Time) string {
	cust := strings.TrimSpace(r.Customer)
	if cust == "" {
		cust = "Sin nombre"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "👤 <b>%s</b>", html.EscapeString(cust))
	if r.Phone != "" {
		fmt.Fprintf(&b, " · 📞 %s", html.EscapeString(r.Phone))
	}
	if r.City != "" {
		fmt.Fprintf(&b, " · 📍 %s", html.EscapeString(r.City))
	}
	fmt.Fprintf(&b, "\n📦 %s · 💵 %s", html.EscapeString(r.Tracking), formatCOP(r.CustomerPays))
	if t, err := time.Parse(time.RFC3339, r.Date); err == nil {
		days := int(now.Sub(t).Hours() / 24)
		switch days {
		case 0:
			b.WriteString(" · salió hoy")
		case 1:
			b.WriteString(" · hace 1 día")
		default:
			fmt.Fprintf(&b, " · hace %d días", days)
		}
	}
	return b.String()
}
