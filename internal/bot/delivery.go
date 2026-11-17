package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"net/url"
	"strings"
	"time"
	"unicode"

	"skydropx-automator/internal/skydropx"
)

// findShipment looks up the shipment in the account by ID or tracking number, with customer, phone and city.
// ok is false if it isn't in the account; err is ErrMockMode if there is no real connection to Skydropx.
func (b *Bot) findShipment(ctx context.Context, id, tracking string) (e skydropx.CODShipment, ok bool, err error) {
	shps, err := b.sky.CODShipments(ctx)
	if err != nil {
		return e, false, err
	}
	for _, x := range shps {
		if (id != "" && x.ID == id) || (tracking != "" && x.Tracking == tracking) {
			return x, true, nil
		}
	}
	return e, false, nil
}

// labelState translates the technical state of the last tracking event into something clear for the customer.
func labelState(st skydropx.Status) string {
	switch st.Raw {
	case "created":
		return "📄 Guía creada; la transportadora aún no recoge el paquete"
	case "picked_up":
		return "📥 La transportadora recibió el paquete"
	case "in_transit":
		return "🚚 En camino"
	case "last_mile":
		return "🛵 En ruta de entrega: llega hoy"
	case "delivery_attempt":
		return "⚠️ Intentaron entregar y no pudieron"
	case "delivered_to_branch":
		return "🏢 En oficina: el cliente debe reclamarlo"
	case "delivered":
		return "✅ Entregado"
	case "exception":
		return "🚨 Con novedad"
	case "in_return":
		return "↩️ En devolución"
	}
	if st.Label != "" {
		return st.Label
	}
	return "Sin eventos de rastreo todavía"
}

// waLink builds a WhatsApp link to the customer's phone with the message already written ("" if the phone
// is unusable). 10-digit Colombian mobiles get the 57 country code.
func waLink(phone, msg string) string {
	var d strings.Builder
	for _, r := range phone {
		if unicode.IsDigit(r) {
			d.WriteRune(r)
		}
	}
	n := d.String()
	if len(n) == 10 && strings.HasPrefix(n, "3") {
		n = "57" + n
	}
	if len(n) < 11 {
		return ""
	}
	return "https://wa.me/" + n + "?text=" + url.QueryEscape(msg)
}

// inTransitMessage is the WhatsApp message telling the customer their order arrives today.
func inTransitMessage(e skydropx.CODShipment, cod float64) string {
	fullName := strings.TrimSpace(e.Customer)
	if f := strings.Fields(fullName); len(f) > 0 {
		fullName = " " + f[0]
	}
	msg := fmt.Sprintf("Hola%s 👋 Tu pedido va hoy en ruta de entrega con Interrapidísimo (guía %s).", fullName, e.Tracking)
	if cod > 0 {
		msg += fmt.Sprintf(" Ten listos %s para pagar al recibir.", formatCOP(cod))
	}
	return msg + " Si no vas a estar, avísanos por aquí. ¡Gracias!"
}

// inTransitText is the notice to the ops group that a shipment went out for delivery, with the link to
// message the customer.
func inTransitText(e skydropx.CODShipment, cod float64, detail string) string {
	cust := e.Customer
	if cust == "" {
		cust = "Sin nombre"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "🛵 <b>En ruta de entrega: llega hoy</b>\n\n👤 <b>Cliente:</b> %s\n", html.EscapeString(cust))
	if e.Phone != "" {
		fmt.Fprintf(&b, "📞 <b>Teléfono:</b> %s\n", html.EscapeString(e.Phone))
	}
	if e.City != "" {
		fmt.Fprintf(&b, "📍 <b>Ciudad:</b> %s\n", html.EscapeString(e.City))
	}
	fmt.Fprintf(&b, "📦 <b>Guía:</b> %s\n", html.EscapeString(e.Tracking))
	if cod > 0 {
		fmt.Fprintf(&b, "💵 <b>Cobra al entregar:</b> %s\n", formatCOP(cod))
	}
	if d := strings.TrimSpace(detail); d != "" {
		fmt.Fprintf(&b, "📝 %s\n", html.EscapeString(d))
	}
	if link := waLink(e.Phone, inTransitMessage(e, cod)); link != "" {
		fmt.Fprintf(&b, "\n👉 <a href=\"%s\">Avisarle al cliente por WhatsApp</a> (el mensaje va listo)\n", html.EscapeString(link))
	} else {
		b.WriteString("\nAvísale al cliente que esté pendiente y tenga el dinero listo.\n")
	}
	return b.String()
}

// inTransitNotice tells the ops group and the seller's labels group, once per day and shipment,
// that the package went out for delivery, so either of them can tell the customer.
func (b *Bot) inTransitNotice(shipmentID, tracking, detail string) {
	if shipmentID == "" && tracking == "" {
		return
	}
	day := time.Now().In(colombiaTZ).Format("2006-01-02")
	fresh, err := b.st.markNotice(shipmentID+tracking+":last_mile:"+day, day)
	if err != nil {
		log.Printf("bot: no se pudo guardar el aviso de reparto: %v", err)
	}
	if !fresh {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e, _, err := b.findShipment(ctx, shipmentID, tracking)
	if errors.Is(err, skydropx.ErrMockMode) {
		return
	}
	if e.Tracking == "" {
		e.Tracking = tracking
	}
	var cod float64
	if e.ID != "" {
		cod, _ = b.sky.CODAmounts(ctx, e)
	}
	msg := inTransitText(e, cod, detail)
	for _, id := range b.inTransitChats() {
		b.say(ctx, id, msg)
	}
}

// inTransitChats are the ops and labels groups, without repeats (if there is no ops group they are the same).
// They receive the notices where either of them can contact the customer.
func (b *Bot) inTransitChats() []int64 {
	var out []int64
	seen := map[int64]bool{}
	for _, id := range append(append([]int64(nil), b.opsChats()...), b.opts.AdminChatIDs...) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// labelCommand handles /guia <number>: shipment state, latest events and collection.
func (b *Bot) labelCommand(ctx context.Context, chatID int64, text string) {
	tracking := strings.TrimSpace(strings.TrimPrefix(text, "/guia"))
	if f := strings.Fields(tracking); len(f) == 1 {
		tracking = f[0]
	} else {
		b.say(ctx, chatID, "Escribe el número de guía. Ejemplo: <code>/guia 240012345678</code>")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	e, ok, err := b.findShipment(ctx, "", tracking)
	switch {
	case errors.Is(err, skydropx.ErrMockMode):
		b.say(ctx, chatID, "El bot no está conectado a Skydropx (modo de prueba).")
		return
	case err != nil:
		b.say(ctx, chatID, "No se pudo consultar Skydropx: "+err.Error())
		return
	case !ok:
		b.say(ctx, chatID, fmt.Sprintf("No encontré la guía <code>%s</code> en la cuenta. Revisa el número.", html.EscapeString(tracking)))
		return
	}
	st, err := b.sky.TrackStatus(ctx, e.Tracking, e.Carrier)
	if err != nil {
		b.say(ctx, chatID, "No se pudo consultar el rastreo: "+err.Error())
		return
	}
	var cod float64
	if e.Status == "pending" {
		cod, _ = b.sky.CODAmounts(ctx, e)
	}
	b.say(ctx, chatID, labelText(e, st, cod))
}

// labelText builds the /guia answer.
func labelText(e skydropx.CODShipment, st skydropx.Status, cod float64) string {
	cust := e.Customer
	if cust == "" {
		cust = "Sin nombre"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "📦 <b>Guía %s</b>", html.EscapeString(e.Tracking))
	if e.Carrier != "" {
		fmt.Fprintf(&b, " · %s", html.EscapeString(e.Carrier))
	}
	fmt.Fprintf(&b, "\n\n<b>%s</b>\n\n👤 %s", labelState(st), html.EscapeString(cust))
	if e.Phone != "" {
		fmt.Fprintf(&b, " · 📞 %s", html.EscapeString(e.Phone))
	}
	if e.City != "" {
		fmt.Fprintf(&b, " · 📍 %s", html.EscapeString(e.City))
	}
	switch e.Status {
	case "collected":
		b.WriteString("\n💵 Cobro: ✅ ya pagó")
	case "pending":
		if cod > 0 {
			fmt.Fprintf(&b, "\n💵 Cobro: por cobrar %s", formatCOP(cod))
		} else {
			b.WriteString("\n💵 Cobro: por cobrar")
		}
	case "failed":
		b.WriteString("\n💵 Cobro: ❌ no se pudo cobrar")
	case "cancelled":
		b.WriteString("\n💵 Envío cancelado")
	}
	if len(st.History) > 0 {
		b.WriteString("\n\n🕒 <b>Últimos eventos:</b>")
		for _, ev := range st.History {
			desc := strings.TrimSpace(ev.Description)
			if desc == "" {
				desc = ev.Status
			}
			fmt.Fprintf(&b, "\n   • %s — %s", dayTime(ev.Date.In(colombiaTZ)), html.EscapeString(desc))
		}
	}
	if st.Raw == "last_mile" {
		if link := waLink(e.Phone, inTransitMessage(e, cod)); link != "" {
			fmt.Fprintf(&b, "\n\n👉 <a href=\"%s\">Avisarle al cliente por WhatsApp</a>", html.EscapeString(link))
		}
	}
	return b.String()
}
