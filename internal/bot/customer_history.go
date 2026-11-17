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

// slowOfficeDays is how many days waiting at the office make a customer count as slow.
const slowOfficeDays = 3

// customerHistory is a customer's past shipments (by ID document or phone) according to Skydropx.
// Nothing is stored: it is looked up every time.
type customerHistory struct {
	badOnes []skydropx.PastIssue // the ones not paid or slow to pick up at the office
	total   int                  // past shipments found (collected, to collect or not collected)
}

// withProblems reports whether a warning is needed.
func (h customerHistory) withProblems() bool { return len(h.badOnes) > 0 }

// checkCustomer looks up a customer's history in Skydropx.
func (b *Bot) checkCustomer(ctx context.Context, idNumber, phoneNum string) (customerHistory, error) {
	var h customerHistory
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var err error
	h.badOnes, h.total, err = b.sky.CustomerHistory(ctx, idNumber, phoneNum, slowOfficeDays)
	if errors.Is(err, skydropx.ErrMockMode) {
		err = nil
	}
	return h, err
}

// pastIssueLine describes a past shipment that went wrong.
func pastIssueLine(a skydropx.PastIssue) string {
	when := ""
	if t, err := time.Parse(time.RFC3339, a.Date); err == nil {
		when = " · " + shortDay(t.In(colombiaTZ))
	}
	if a.Kind == "oficina" {
		return fmt.Sprintf("🐢 Tardó %d días en recoger en la oficina · guía %s%s", a.DaysAtOffice, html.EscapeString(a.Tracking), when)
	}
	return fmt.Sprintf("❌ No pagó (devolución o sin cobrar) · guía %s%s", html.EscapeString(a.Tracking), when)
}

// opsWarning is the block above the order in the ops group ("" if there are no problems).
func opsWarning(h customerHistory) string {
	if !h.withProblems() {
		return ""
	}
	var b strings.Builder
	b.WriteString("⚠️ <b>CLIENTE CON HISTORIAL</b>\n")
	for _, a := range h.badOnes {
		b.WriteString(pastIssueLine(a) + "\n")
	}
	if h.total > 0 {
		fmt.Fprintf(&b, "📦 De %s anteriores\n", orderCount(h.total))
	}
	b.WriteString("<i>Revisa antes de generar la guía.</i>\n━━━━━━━━━━━━━━━\n\n")
	return b.String()
}

// sellerWarning is the short line the seller sees ("" if there are no problems).
func sellerWarning(h customerHistory) string {
	if !h.withProblems() {
		return ""
	}
	var reasons []string
	var unpaid, office int
	for _, a := range h.badOnes {
		if a.Kind == "oficina" {
			office++
		} else {
			unpaid++
		}
	}
	if unpaid > 0 {
		reasons = append(reasons, fmt.Sprintf("%d sin pagar", unpaid))
	}
	if office > 0 {
		reasons = append(reasons, fmt.Sprintf("%d demora en oficina", office))
	}
	return "\n\n⚠️ <b>Este cliente tiene historial</b> (" + strings.Join(reasons, ", ") +
		"). Se revisará antes de generar la guía; si puedes, confírmale el pedido o pídele que pague antes."
}

// customerCommand handles /cliente <ID document or phone>: that person's full history.
func (b *Bot) customerCommand(ctx context.Context, chatID int64, text string) {
	if strings.HasPrefix(text, "/clientes") {
		b.say(ctx, chatID, "Este comando solo funciona en el grupo de gestión.")
		return
	}
	f := strings.Fields(strings.TrimPrefix(text, "/cliente"))
	if len(f) != 1 || (skydropx.DocumentKey(f[0]) == "" && skydropx.PhoneKey(f[0]) == "") {
		b.say(ctx, chatID, "Escribe la cédula o el teléfono. Ejemplo: <code>/cliente 1234567890</code>")
		return
	}
	h, err := b.checkCustomer(ctx, f[0], f[0])
	if err != nil {
		b.say(ctx, chatID, "No se pudo consultar Skydropx: "+html.EscapeString(err.Error()))
		return
	}
	b.say(ctx, chatID, customerText(f[0], h))
}

// customerText builds the /cliente answer.
func customerText(id string, h customerHistory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "👤 <b>CLIENTE %s</b>\n", html.EscapeString(id))
	if h.total == 0 {
		b.WriteString("\nNo tiene envíos anteriores en la cuenta.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\n📦 %s anteriores\n", orderCount(h.total))
	if len(h.badOnes) == 0 {
		b.WriteString("✅ Todos pagaron sin problema (o siguen en camino).\n")
	} else {
		for _, a := range h.badOnes {
			b.WriteString(pastIssueLine(a) + "\n")
		}
	}
	if h.withProblems() {
		b.WriteString("\n⚠️ Sus pedidos llegan con advertencia.\n")
	}
	return b.String()
}
