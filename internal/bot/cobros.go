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

// Skydropx no tiene un webhook de "el cliente pagó": el pago se avisa con el webhook de entrega,
// porque en contra entrega el cliente paga al recibir.

// avisoEntrega avisa el pago en cuanto Skydropx reporta la entrega del paquete (estado "collected"),
// o que no se pudo cobrar si va en devolución ("failed"). No repite el aviso si ya se dio.
func (b *Bot) avisoEntrega(shipmentID, tracking, estado string) {
	if shipmentID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	b.cobroMu.Lock()
	defer b.cobroMu.Unlock()

	e := skydropx.CODShipment{ID: shipmentID, Tracking: tracking}
	envios, err := b.sky.CODShipments(ctx)
	if errors.Is(err, skydropx.ErrMockMode) {
		return
	}
	if err != nil {
		log.Printf("bot: no se pudieron consultar los envíos para el aviso de entrega: %v", err)
	}
	for _, x := range envios {
		if x.ID == shipmentID {
			e = x
			break
		}
	}
	if e.Status == "cancelled" {
		return
	}
	e.Status = estado
	nuevo, err := b.st.marcarCobro(e.ID, estado)
	if err != nil {
		log.Printf("bot: no se pudo guardar el estado del cobro: %v", err)
	}
	if !nuevo {
		return // ya se avisó (webhook repetido o revisión periódica)
	}
	pays, product := b.sky.CODAmounts(ctx, e)
	msg := cobroMensaje(e, pays, product)
	for _, id := range b.gestionChats() {
		b.say(ctx, id, msg)
	}
}

// cobroMensaje arma el aviso de un pago recibido o de un cobro que no se pudo hacer.
func cobroMensaje(e skydropx.CODShipment, pays, product float64) string {
	cliente := html.EscapeString(e.Customer)
	if cliente == "" {
		cliente = "Sin nombre"
	}
	detalle := "producto + envío"
	if product > 0 && pays >= product {
		detalle = fmt.Sprintf("producto %s + envío %s", formatCOP(product), formatCOP(pays-product))
	}
	if e.Status == "failed" {
		return fmt.Sprintf("❌ <b>No se pudo cobrar</b>\n\n👤 <b>Cliente:</b> %s\n📦 <b>Guía:</b> %s\n"+
			"💸 <b>No se recaudó:</b> %s (%s)\n\nEl envío (%s) ya estaba pagado y puede llegar un cargo por la devolución.",
			cliente, html.EscapeString(e.Tracking), formatCOP(pays), detalle, formatCOP(e.Cost))
	}
	return fmt.Sprintf("💵 <b>¡Pagaron!</b>\n\n👤 <b>Cliente:</b> %s\n📦 <b>Guía:</b> %s\n"+
		"💰 <b>Pagó:</b> %s (%s)\n\nEl paquete se entregó y lo cobrado llega a tu saldo disponible de Skydropx.",
		cliente, html.EscapeString(e.Tracking), formatCOP(pays), detalle)
}

// avisoIncidencia avisa en el grupo de gestión que la transportadora recibió el paquete, o un problema
// de entrega (intento fallido, novedad o paquete en oficina) para llamar al cliente antes de que el envío
// se devuelva. La recogida se avisa una sola vez; cada problema, una vez por día.
func (b *Bot) avisoIncidencia(shipmentID, status, tracking, detalle, trackingURL string) {
	if shipmentID == "" {
		return
	}
	day := time.Now().In(colombiaTZ).Format("2006-01-02")
	key := shipmentID + ":" + status
	if status != "picked_up" {
		key += ":" + day
	}
	nuevo, err := b.st.marcarAviso(key, day)
	if err != nil {
		log.Printf("bot: no se pudo guardar el aviso de entrega: %v", err)
	}
	if !nuevo {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e, _, err := b.buscarEnvio(ctx, shipmentID, "")
	if errors.Is(err, skydropx.ErrMockMode) {
		return
	}
	if e.Tracking == "" {
		e.Tracking = tracking
	}
	msg := incidenciaMensaje(e, status, detalle, trackingURL)
	for _, id := range b.gestionChats() {
		b.say(ctx, id, msg)
	}
}

// incidenciaMensaje arma el aviso de un problema de entrega con lo que hay que hacer.
func incidenciaMensaje(e skydropx.CODShipment, status, detalle, trackingURL string) string {
	titulo, accion := "🚨 <b>Novedad con el envío</b>", "Revisa con el cliente o con la transportadora para que no se devuelva."
	switch status {
	case "picked_up":
		titulo, accion = "🚚 <b>La transportadora recibió el paquete</b>", "Ya va en camino. Puedes avisarle al cliente."
	case "delivery_attempt":
		titulo, accion = "⚠️ <b>No pudieron entregar</b>", "Llama al cliente para que esté pendiente: la transportadora vuelve a intentar."
	case "delivered_to_branch":
		titulo, accion = "🏢 <b>El paquete quedó en la oficina</b>", "Avísale al cliente que lo recoja y pague ahí; si no lo recoge, se devuelve."
	}
	cliente := e.Customer
	if cliente == "" {
		cliente = "Sin nombre"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n👤 <b>Cliente:</b> %s\n", titulo, html.EscapeString(cliente))
	if e.Phone != "" {
		fmt.Fprintf(&b, "📞 <b>Teléfono:</b> %s\n", html.EscapeString(e.Phone))
	}
	if e.City != "" {
		fmt.Fprintf(&b, "📍 <b>Ciudad:</b> %s\n", html.EscapeString(e.City))
	}
	fmt.Fprintf(&b, "📦 <b>Guía:</b> %s\n", html.EscapeString(e.Tracking))
	if d := strings.TrimSpace(detalle); d != "" {
		fmt.Fprintf(&b, "📝 <b>Detalle:</b> %s\n", html.EscapeString(d))
	}
	if trackingURL != "" {
		fmt.Fprintf(&b, "🔎 %s\n", html.EscapeString(trackingURL))
	}
	fmt.Fprintf(&b, "\n%s", accion)
	return b.String()
}
