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

// buscarEnvio busca en la cuenta el envío por ID o por número de guía, con cliente, teléfono y ciudad.
// ok es false si no está en la cuenta; err es ErrMockMode si no hay conexión real con Skydropx.
func (b *Bot) buscarEnvio(ctx context.Context, id, tracking string) (e skydropx.CODShipment, ok bool, err error) {
	envios, err := b.sky.CODShipments(ctx)
	if err != nil {
		return e, false, err
	}
	for _, x := range envios {
		if (id != "" && x.ID == id) || (tracking != "" && x.Tracking == tracking) {
			return x, true, nil
		}
	}
	return e, false, nil
}

// estadoGuia traduce el estado técnico del último evento de rastreo a algo claro para el cliente.
func estadoGuia(st skydropx.Status) string {
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

// waLink arma un enlace de WhatsApp al teléfono del cliente con el mensaje ya escrito ("" si el teléfono
// no sirve). Los celulares colombianos de 10 dígitos llevan el indicativo 57.
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

// mensajeEnRuta es el WhatsApp para avisarle al cliente que su pedido llega hoy.
func mensajeEnRuta(e skydropx.CODShipment, cobro float64) string {
	nombre := strings.TrimSpace(e.Customer)
	if f := strings.Fields(nombre); len(f) > 0 {
		nombre = " " + f[0]
	}
	msg := fmt.Sprintf("Hola%s 👋 Tu pedido va hoy en ruta de entrega con Interrapidísimo (guía %s).", nombre, e.Tracking)
	if cobro > 0 {
		msg += fmt.Sprintf(" Ten listos %s para pagar al recibir.", formatCOP(cobro))
	}
	return msg + " Si no vas a estar, avísanos por aquí. ¡Gracias!"
}

// enRutaTexto es el aviso al grupo de gestión de que un envío salió a reparto, con el enlace para
// escribirle al cliente.
func enRutaTexto(e skydropx.CODShipment, cobro float64, detalle string) string {
	cliente := e.Customer
	if cliente == "" {
		cliente = "Sin nombre"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "🛵 <b>En ruta de entrega: llega hoy</b>\n\n👤 <b>Cliente:</b> %s\n", html.EscapeString(cliente))
	if e.Phone != "" {
		fmt.Fprintf(&b, "📞 <b>Teléfono:</b> %s\n", html.EscapeString(e.Phone))
	}
	if e.City != "" {
		fmt.Fprintf(&b, "📍 <b>Ciudad:</b> %s\n", html.EscapeString(e.City))
	}
	fmt.Fprintf(&b, "📦 <b>Guía:</b> %s\n", html.EscapeString(e.Tracking))
	if cobro > 0 {
		fmt.Fprintf(&b, "💵 <b>Cobra al entregar:</b> %s\n", formatCOP(cobro))
	}
	if d := strings.TrimSpace(detalle); d != "" {
		fmt.Fprintf(&b, "📝 %s\n", html.EscapeString(d))
	}
	if link := waLink(e.Phone, mensajeEnRuta(e, cobro)); link != "" {
		fmt.Fprintf(&b, "\n👉 <a href=\"%s\">Avisarle al cliente por WhatsApp</a> (el mensaje va listo)\n", html.EscapeString(link))
	} else {
		b.WriteString("\nAvísale al cliente que esté pendiente y tenga el dinero listo.\n")
	}
	return b.String()
}

// avisoEnRuta avisa en el grupo de gestión y en el grupo de guías del vendedor, una vez por día y envío,
// que el paquete salió a reparto, para que cualquiera de los dos le avise al cliente.
func (b *Bot) avisoEnRuta(shipmentID, tracking, detalle string) {
	if shipmentID == "" && tracking == "" {
		return
	}
	day := time.Now().In(colombiaTZ).Format("2006-01-02")
	nuevo, err := b.st.marcarAviso(shipmentID+tracking+":last_mile:"+day, day)
	if err != nil {
		log.Printf("bot: no se pudo guardar el aviso de reparto: %v", err)
	}
	if !nuevo {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e, _, err := b.buscarEnvio(ctx, shipmentID, tracking)
	if errors.Is(err, skydropx.ErrMockMode) {
		return
	}
	if e.Tracking == "" {
		e.Tracking = tracking
	}
	var cobro float64
	if e.ID != "" {
		cobro, _ = b.sky.CODAmounts(ctx, e)
	}
	msg := enRutaTexto(e, cobro, detalle)
	for _, id := range b.chatsEnRuta() {
		b.say(ctx, id, msg)
	}
}

// chatsEnRuta son los grupos de gestión y de guías, sin repetir (si no hay grupo de gestión son el mismo).
// Reciben los avisos en los que cualquiera de los dos puede contactar al cliente.
func (b *Bot) chatsEnRuta() []int64 {
	var out []int64
	visto := map[int64]bool{}
	for _, id := range append(append([]int64(nil), b.gestionChats()...), b.opts.AdminChatIDs...) {
		if !visto[id] {
			visto[id] = true
			out = append(out, id)
		}
	}
	return out
}

// comandoGuia atiende /guia <número>: estado del envío, últimos eventos y cobro.
func (b *Bot) comandoGuia(ctx context.Context, chatID int64, text string) {
	tracking := strings.TrimSpace(strings.TrimPrefix(text, "/guia"))
	if f := strings.Fields(tracking); len(f) == 1 {
		tracking = f[0]
	} else {
		b.say(ctx, chatID, "Escribe el número de guía. Ejemplo: <code>/guia 240012345678</code>")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	e, ok, err := b.buscarEnvio(ctx, "", tracking)
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
	st, err := b.sky.Rastreo(ctx, e.Tracking, e.Carrier)
	if err != nil {
		b.say(ctx, chatID, "No se pudo consultar el rastreo: "+err.Error())
		return
	}
	var cobro float64
	if e.Status == "pending" {
		cobro, _ = b.sky.CODAmounts(ctx, e)
	}
	b.say(ctx, chatID, guiaTexto(e, st, cobro))
}

// guiaTexto arma la respuesta de /guia.
func guiaTexto(e skydropx.CODShipment, st skydropx.Status, cobro float64) string {
	cliente := e.Customer
	if cliente == "" {
		cliente = "Sin nombre"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "📦 <b>Guía %s</b>", html.EscapeString(e.Tracking))
	if e.Carrier != "" {
		fmt.Fprintf(&b, " · %s", html.EscapeString(e.Carrier))
	}
	fmt.Fprintf(&b, "\n\n<b>%s</b>\n\n👤 %s", estadoGuia(st), html.EscapeString(cliente))
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
		if cobro > 0 {
			fmt.Fprintf(&b, "\n💵 Cobro: por cobrar %s", formatCOP(cobro))
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
			fmt.Fprintf(&b, "\n   • %s — %s", diaHora(ev.Date.In(colombiaTZ)), html.EscapeString(desc))
		}
	}
	if st.Raw == "last_mile" {
		if link := waLink(e.Phone, mensajeEnRuta(e, cobro)); link != "" {
			fmt.Fprintf(&b, "\n\n👉 <a href=\"%s\">Avisarle al cliente por WhatsApp</a>", html.EscapeString(link))
		}
	}
	return b.String()
}
