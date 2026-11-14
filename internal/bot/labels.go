package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"
	"time"

	"skydropx-automator/internal/domain"
	"skydropx-automator/internal/labels"
	"skydropx-automator/internal/skydropx"
	"skydropx-automator/internal/telegram"
)

// Acciones de los botones: "<acción>:<ID de la orden>" (un UUID cabe en los 64 bytes de Telegram).
const (
	actGenerar   = "g"
	actConfirmar = "c"
	actCancelar  = "x"
)

func botonGenerar(orderID string) [][]telegram.Button {
	return [][]telegram.Button{telegram.Row(telegram.Button{Text: "🚚 Generar guía Interrapidísimo", Data: actGenerar + ":" + orderID})}
}

// adminOrderText es el aviso de orden nueva que reciben los administradores.
func adminOrderText(sender string, data domain.ShipmentData) string {
	qty := data.Cantidad
	if qty < 1 {
		qty = 1
	}
	return fmt.Sprintf(
		"🆕 <b>Nueva orden para gestionar</b>\n🧑‍💼 Creada por: %s\n👤 %s\n📞 %s\n📍 %s, %s\n🏠 %s\n📦 Cantidad: %d\n💰 Cobro contra entrega: %s",
		html.EscapeString(sender), html.EscapeString(data.Nombre), html.EscapeString(data.Telefono),
		html.EscapeString(data.Ciudad), html.EscapeString(data.Departamento), html.EscapeString(data.Direccion),
		qty, formatCOP(data.ValorUnitario*float64(qty)),
	)
}

// handleCallback atiende los botones de generar, confirmar y cancelar la guía.
func (b *Bot) handleCallback(ctx context.Context, cq *telegram.CallbackQuery) {
	if cq.Message == nil {
		_ = b.tg.AnswerCallback(ctx, cq.ID, "")
		return
	}
	chatID, msgID := cq.Message.Chat.ID, cq.Message.MessageID
	if !b.isGestion(chatID) {
		_ = b.tg.AnswerCallback(ctx, cq.ID, "La guía se gestiona desde el grupo de gestión.")
		return
	}
	action, orderID, _ := strings.Cut(cq.Data, ":")
	p, ok := b.st.getPending(orderID)
	if !ok {
		_ = b.tg.AnswerCallback(ctx, cq.ID, "Esta orden ya tiene guía o no está pendiente.")
		_ = b.tg.EditMessage(ctx, chatID, msgID, html.EscapeString(cq.Message.Text), nil)
		return
	}
	if p.Data == nil {
		_ = b.tg.AnswerCallback(ctx, cq.ID, "Esta orden se creó antes de los botones: genera la guía en Skydropx.")
		return
	}

	switch action {
	case actGenerar:
		b.cotizarGuia(ctx, cq.ID, chatID, msgID, p)
	case actConfirmar:
		b.confirmarGuia(ctx, cq.ID, chatID, msgID, p)
	case actCancelar:
		_ = b.tg.AnswerCallback(ctx, cq.ID, "Cancelado.")
		if _, err := b.st.updatePending(orderID, func(p *Pending) { p.Quote = nil }); err != nil {
			log.Printf("bot: no se pudo guardar la orden %s: %v", orderID, err)
		}
		_ = b.tg.EditMessage(ctx, chatID, msgID, adminOrderText(p.Sender, *p.Data), botonGenerar(orderID))
	default:
		_ = b.tg.AnswerCallback(ctx, cq.ID, "")
	}
}

// cotizarGuia cotiza con Interrapidísimo y muestra el precio con los botones de confirmar o cancelar.
func (b *Bot) cotizarGuia(ctx context.Context, callbackID string, chatID, msgID int64, p Pending) {
	if p.LabelRequested {
		_ = b.tg.AnswerCallback(ctx, callbackID, "La guía de esta orden ya se pidió.")
		return
	}
	_ = b.tg.AnswerCallback(ctx, callbackID, "Cotizando…")
	base := adminOrderText(p.Sender, *p.Data)
	_ = b.tg.EditMessage(ctx, chatID, msgID, base+"\n\n⏳ Cotizando con Interrapidísimo…", nil)

	qctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	q, err := b.sky.QuoteLabel(qctx, *p.Data)
	if err != nil {
		_ = b.tg.EditMessage(ctx, chatID, msgID, base+"\n\n❌ No se pudo cotizar: "+html.EscapeString(err.Error()), botonGenerarReintentar(p.OrderID))
		return
	}
	if _, err := b.st.updatePending(p.OrderID, func(p *Pending) { p.Quote = &q }); err != nil {
		log.Printf("bot: no se pudo guardar la cotización de %s: %v", p.OrderID, err)
	}

	dias := ""
	if q.Days > 0 {
		dias = fmt.Sprintf(" · %d día(s)", q.Days)
	}
	text := fmt.Sprintf("%s\n\n🚚 <b>Interrapidísimo</b>%s · <b>%s</b>%s\n🛡️ Sin Protección Plus\n\n¿Genero la guía?",
		base, servicio(q.Service), formatCOP(q.Total), dias)
	_ = b.tg.EditMessage(ctx, chatID, msgID, text, [][]telegram.Button{telegram.Row(
		telegram.Button{Text: "✅ Confirmar", Data: actConfirmar + ":" + p.OrderID},
		telegram.Button{Text: "❌ Cancelar", Data: actCancelar + ":" + p.OrderID},
	)})
}

func servicio(s string) string {
	if s = strings.TrimSpace(s); s == "" {
		return ""
	}
	return " " + html.EscapeString(s)
}

func botonGenerarReintentar(orderID string) [][]telegram.Button {
	return [][]telegram.Button{telegram.Row(telegram.Button{Text: "🔄 Reintentar", Data: actGenerar + ":" + orderID})}
}

// confirmarGuia crea el envío con la tarifa cotizada. La guía la publica después la revisión de pendientes.
func (b *Bot) confirmarGuia(ctx context.Context, callbackID string, chatID, msgID int64, p Pending) {
	if p.Quote == nil {
		_ = b.tg.AnswerCallback(ctx, callbackID, "Primero cotiza la guía.")
		return
	}
	// Se marca antes de llamar a Skydropx para que un segundo toque no cree otra guía.
	claimed := false
	if _, err := b.st.updatePending(p.OrderID, func(p *Pending) {
		if !p.LabelRequested {
			p.LabelRequested = true
			claimed = true
		}
	}); err != nil {
		log.Printf("bot: no se pudo guardar la orden %s: %v", p.OrderID, err)
	}
	if !claimed {
		_ = b.tg.AnswerCallback(ctx, callbackID, "La guía de esta orden ya se pidió.")
		return
	}
	_ = b.tg.AnswerCallback(ctx, callbackID, "Generando la guía…")
	base := adminOrderText(p.Sender, *p.Data)
	_ = b.tg.EditMessage(ctx, chatID, msgID, base+"\n\n⏳ Generando la guía con Interrapidísimo…", nil)

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	shipmentID, err := b.sky.CreateLabel(cctx, p.OrderID, *p.Quote, *p.Data)
	if err != nil {
		if _, serr := b.st.updatePending(p.OrderID, func(p *Pending) { p.LabelRequested = false; p.Quote = nil }); serr != nil {
			log.Printf("bot: no se pudo guardar la orden %s: %v", p.OrderID, serr)
		}
		_ = b.tg.EditMessage(ctx, chatID, msgID, base+"\n\n❌ No se generó la guía: "+html.EscapeString(err.Error()), botonGenerarReintentar(p.OrderID))
		return
	}
	_ = b.tg.EditMessage(ctx, chatID, msgID, fmt.Sprintf(
		"%s\n\n✅ <b>Guía solicitada</b> con Interrapidísimo (%s, sin Protección Plus). En cuanto Skydropx la genere, llega aquí.",
		base, formatCOP(p.Quote.Total)), nil)

	// Skydropx tarda unos segundos en generar la guía: se revisa pronto en vez de esperar la revisión periódica.
	go func() {
		for _, wait := range []time.Duration{15 * time.Second, 45 * time.Second, 2 * time.Minute} {
			sleep(ctx, wait)
			if !b.st.isPending(p.OrderID) {
				return
			}
			b.checkPending(ctx, p, []string{shipmentID})
		}
	}()
}

// maxDiasImprimir es el máximo de días hacia atrás que acepta /imprimir.
const maxDiasImprimir = 15

const ayudaImprimir = "🖨️ /imprimir arma las guías de hoy en hojas carta de 4.\n" +
	"/imprimir 3 incluye las de los últimos 3 días (hasta 15), por ejemplo después de un festivo."

// diasAImprimir lee el número de días de "/imprimir 3" (o "/imprimir@bot 3"). Sin número es 1 (hoy).
func diasAImprimir(text string) int {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return 1
	}
	n, err := strconv.Atoi(fields[1])
	if err != nil || n < 1 {
		return 1
	}
	if n > maxDiasImprimir {
		return maxDiasImprimir
	}
	return n
}

// periodo describe los días que se imprimen: "de hoy" o "de los últimos 3 días".
func periodo(dias int) string {
	if dias <= 1 {
		return "de hoy"
	}
	return fmt.Sprintf("de los últimos %d días", dias)
}

// imprimir arma las guías de los últimos dias días en hojas carta de 4 y las envía como PDF.
func (b *Bot) imprimir(ctx context.Context, chatID int64, dias int) {
	b.say(ctx, chatID, "⏳ Buscando las guías "+periodo(dias)+"…")
	pctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	guias, err := b.sky.RecentLabels(pctx, dias)
	if errors.Is(err, skydropx.ErrMockMode) {
		b.say(ctx, chatID, "Modo simulación: no hay guías reales para imprimir.")
		return
	}
	if err != nil {
		b.say(ctx, chatID, "❌ No pude consultar las guías: "+html.EscapeString(err.Error()))
		return
	}
	if len(guias) == 0 {
		if dias <= 1 {
			b.say(ctx, chatID, "Hoy todavía no hay guías generadas. Para incluir días anteriores escribe, por ejemplo, /imprimir 3")
		} else {
			b.say(ctx, chatID, "No hay guías generadas "+periodo(dias)+".")
		}
		return
	}

	var pdfs [][]byte
	var fallidas []string
	for _, g := range guias {
		data, _, err := b.sky.DownloadLabel(pctx, g.LabelURL)
		if err != nil {
			log.Printf("bot: no se pudo descargar la guía %s: %v", g.Tracking, err)
			fallidas = append(fallidas, g.Tracking)
			continue
		}
		pdfs = append(pdfs, data)
	}
	if len(pdfs) == 0 {
		b.say(ctx, chatID, "❌ No pude descargar ninguna guía "+periodo(dias)+".")
		return
	}
	hojas, err := labels.Sheets(pctx, pdfs)
	if err != nil {
		b.say(ctx, chatID, "❌ No pude armar las hojas: "+html.EscapeString(err.Error()))
		return
	}

	numHojas := (len(pdfs) + labels.PerSheet - 1) / labels.PerSheet
	caption := fmt.Sprintf("🖨️ <b>%d guía(s) %s</b> en %d hoja(s) carta.", len(pdfs), periodo(dias), numHojas)
	if len(fallidas) > 0 {
		caption += "\n⚠️ No se pudieron descargar: " + html.EscapeString(strings.Join(fallidas, ", "))
	}
	nombre := fmt.Sprintf("guias-%s.pdf", time.Now().In(colombiaTZ).Format("2006-01-02"))
	if err := b.tg.SendDocument(ctx, chatID, nombre, hojas, caption); err != nil {
		log.Printf("bot: no se pudo enviar el PDF de guías: %v", err)
		b.say(ctx, chatID, "❌ No pude enviar el PDF: "+html.EscapeString(err.Error()))
	}
}

// colombiaTZ es la hora de Colombia (UTC-5, sin horario de verano); la imagen de Docker no trae zonas horarias.
var colombiaTZ = time.FixedZone("COT", -5*3600)

// gestionChats son los chats que gestionan las guías: TELEGRAM_GESTION_CHAT_IDS o, si no está, el grupo de administradores.
func (b *Bot) gestionChats() []int64 {
	if len(b.opts.GestionChatIDs) > 0 {
		return b.opts.GestionChatIDs
	}
	return b.opts.AdminChatIDs
}

func (b *Bot) isGestion(chatID int64) bool {
	for _, id := range b.gestionChats() {
		if id == chatID {
			return true
		}
	}
	return false
}
