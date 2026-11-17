package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"strings"
	"time"

	"skydropx-automator/internal/skydropx"
)

// Skydropx has no "the customer paid" webhook: the payment is announced with the delivery webhook,
// because with cash on delivery the customer pays on receipt.

// deliveryNotice announces the payment as soon as Skydropx reports the package delivered (state "collected"),
// or that it couldn't be collected if it is being returned ("failed"). It doesn't repeat a notice already given.
// Both notices also reach the seller's labels group, without what only matters to the owner.
func (b *Bot) deliveryNotice(shipmentID, tracking, stage string) {
	if shipmentID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	b.codMu.Lock()
	defer b.codMu.Unlock()

	e := skydropx.CODShipment{ID: shipmentID, Tracking: tracking}
	shps, err := b.sky.CODShipments(ctx)
	if errors.Is(err, skydropx.ErrMockMode) {
		return
	}
	if err != nil {
		log.Printf("bot: no se pudieron consultar los envíos para el aviso de entrega: %v", err)
	}
	for _, x := range shps {
		if x.ID == shipmentID {
			e = x
			break
		}
	}
	if e.Status == "cancelled" {
		return
	}
	e.Status = stage
	fresh, err := b.st.markCOD(e.ID, stage)
	if err != nil {
		log.Printf("bot: no se pudo guardar el estado del cobro: %v", err)
	}
	if !fresh {
		return // already announced (repeated webhook or periodic check)
	}
	pays, product := b.sky.CODAmounts(ctx, e)
	msg := codMessage(e, pays, product)
	for _, id := range b.opsChats() {
		b.say(ctx, id, msg)
	}
	sellerMsg := codSellerMessage(e, pays, product)
	for _, id := range b.opts.AdminChatIDs {
		if !b.isOps(id) {
			b.say(ctx, id, sellerMsg)
		}
	}
}

// codSellerMessage is the notice for the seller's group: without the Skydropx balance or the
// return cost, and if it couldn't be collected, with what they need to do.
func codSellerMessage(e skydropx.CODShipment, pays, product float64) string {
	msg := codMessage(e, pays, product)
	if i := strings.LastIndex(msg, "\n\n"); i >= 0 {
		msg = msg[:i]
	}
	if e.Status == "failed" {
		return msg + "\n\n📞 Llama al cliente para saber por qué no lo recibió; si todavía lo quiere, avísanos antes de que el paquete regrese."
	}
	return msg + "\n\n✅ El paquete se entregó y el cliente pagó."
}

// customerContact is the customer's phone and city lines ("" if unknown).
func customerContact(e skydropx.CODShipment) string {
	var b strings.Builder
	if e.Phone != "" {
		fmt.Fprintf(&b, "📞 <b>Teléfono:</b> %s\n", html.EscapeString(e.Phone))
	}
	if e.City != "" {
		fmt.Fprintf(&b, "📍 <b>Ciudad:</b> %s\n", html.EscapeString(e.City))
	}
	return b.String()
}

// codMessage builds the notice of a received payment or of a collection that failed.
func codMessage(e skydropx.CODShipment, pays, product float64) string {
	cust := html.EscapeString(e.Customer)
	if cust == "" {
		cust = "Sin nombre"
	}
	detail := "producto + envío"
	if product > 0 && pays >= product {
		detail = fmt.Sprintf("producto %s + envío %s", formatCOP(product), formatCOP(pays-product))
	}
	if e.Status == "failed" {
		return fmt.Sprintf("❌ <b>No se pudo cobrar</b>\n\n👤 <b>Cliente:</b> %s\n%s📦 <b>Guía:</b> %s\n"+
			"💸 <b>No se recaudó:</b> %s (%s)\n\nEl envío (%s) ya estaba pagado y puede llegar un cargo por la devolución.",
			cust, customerContact(e), html.EscapeString(e.Tracking), formatCOP(pays), detail, formatCOP(e.Cost))
	}
	return fmt.Sprintf("💵 <b>¡Pagaron!</b>\n\n👤 <b>Cliente:</b> %s\n📦 <b>Guía:</b> %s\n"+
		"💰 <b>Pagó:</b> %s (%s)\n\nEl paquete se entregó y lo cobrado llega a tu saldo disponible de Skydropx.",
		cust, html.EscapeString(e.Tracking), formatCOP(pays), detail)
}

// incidentNotice tells the ops group that the carrier received the package, or about a delivery
// problem (failed attempt, incident or package at the office) to call the customer before the shipment
// is returned. Pickup is announced only once; each problem, once per day.
func (b *Bot) incidentNotice(shipmentID, status, tracking, detail, trackingURL string) {
	if shipmentID == "" {
		return
	}
	day := time.Now().In(colombiaTZ).Format("2006-01-02")
	key := shipmentID + ":" + status
	if status != "picked_up" {
		key += ":" + day
	}
	fresh, err := b.st.markNotice(key, day)
	if err != nil {
		log.Printf("bot: no se pudo guardar el aviso de entrega: %v", err)
	}
	if !fresh {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e, _, err := b.findShipment(ctx, shipmentID, "")
	if errors.Is(err, skydropx.ErrMockMode) {
		return
	}
	if e.Tracking == "" {
		e.Tracking = tracking
	}
	msg := incidentMessage(e, status, detail, trackingURL)
	chats := b.opsChats()
	if status != "picked_up" {
		chats = b.inTransitChats() // delivery problems also go to the seller, so they call the customer in time
	}
	for _, id := range chats {
		b.say(ctx, id, msg)
	}
}

// incidentMessage builds the notice of a delivery problem with what needs to be done.
func incidentMessage(e skydropx.CODShipment, status, detail, trackingURL string) string {
	title, act := "🚨 <b>Novedad con el envío</b>", "Revisa con el cliente o con la transportadora para que no se devuelva."
	switch status {
	case "picked_up":
		title, act = "🚚 <b>La transportadora recibió el paquete</b>", "Ya va en camino. Puedes avisarle al cliente."
	case "delivery_attempt":
		title, act = "⚠️ <b>No pudieron entregar</b>", "Llama al cliente para que esté pendiente: la transportadora vuelve a intentar."
	case "delivered_to_branch":
		title, act = "🏢 <b>El paquete quedó en la oficina</b>", "Avísale al cliente que lo recoja y pague ahí; si no lo recoge, se devuelve."
	}
	cust := e.Customer
	if cust == "" {
		cust = "Sin nombre"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n👤 <b>Cliente:</b> %s\n", title, html.EscapeString(cust))
	if e.Phone != "" {
		fmt.Fprintf(&b, "📞 <b>Teléfono:</b> %s\n", html.EscapeString(e.Phone))
	}
	if e.City != "" {
		fmt.Fprintf(&b, "📍 <b>Ciudad:</b> %s\n", html.EscapeString(e.City))
	}
	fmt.Fprintf(&b, "📦 <b>Guía:</b> %s\n", html.EscapeString(e.Tracking))
	if d := strings.TrimSpace(detail); d != "" {
		fmt.Fprintf(&b, "📝 <b>Detalle:</b> %s\n", html.EscapeString(d))
	}
	if trackingURL != "" {
		fmt.Fprintf(&b, "🔎 %s\n", html.EscapeString(trackingURL))
	}
	fmt.Fprintf(&b, "\n%s", act)
	return b.String()
}
