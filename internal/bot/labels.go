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

// Button actions: "<action>:<order ID>" (a UUID fits in Telegram's 64 bytes).
const (
	actGenerate = "g"
	actConfirm  = "c"
	actCancel   = "x"
	actForce    = "f" // generate the label even if the customer has a history

	actSaveCustomer = "rc" // retry saving a customer in the customer list
)

func generateButton(orderID string) [][]telegram.Button {
	return [][]telegram.Button{telegram.Row(telegram.Button{Text: "🚚 Generar guía Interrapidísimo", Data: actGenerate + ":" + orderID})}
}

// adminOrderText is the new-order notice the admins receive.
func adminOrderText(sender string, data domain.ShipmentData) string {
	qty := data.Quantity
	if qty < 1 {
		qty = 1
	}
	return fmt.Sprintf(
		"🆕 <b>Nueva orden para gestionar</b>\n🧑‍💼 Creada por: %s\n👤 %s\n📞 %s\n📍 %s, %s\n🏠 %s\n📦 Cantidad: %d\n💰 Cobro contra entrega: %s",
		html.EscapeString(sender), html.EscapeString(data.Name), html.EscapeString(data.Phone),
		html.EscapeString(data.City), html.EscapeString(data.Department), html.EscapeString(data.Address),
		qty, formatCOP(data.UnitPrice*float64(qty)),
	)
}

// orderText is the order notice in the ops group, with the customer's warning above.
func orderText(p Pending) string {
	return p.Warning + adminOrderText(p.Sender, *p.Data)
}

// handleCallback handles the generate, confirm and cancel label buttons.
func (b *Bot) handleCallback(ctx context.Context, cq *telegram.CallbackQuery) {
	if cq.Message == nil {
		_ = b.tg.AnswerCallback(ctx, cq.ID, "")
		return
	}
	chatID, msgID := cq.Message.Chat.ID, cq.Message.MessageID
	if !b.isOps(chatID) {
		_ = b.tg.AnswerCallback(ctx, cq.ID, "La guía se gestiona desde el grupo de gestión.")
		return
	}
	action, orderID, _ := strings.Cut(cq.Data, ":")
	if action == actSaveCustomer {
		b.retryCustomer(ctx, cq.ID, chatID, msgID, orderID)
		return
	}
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
	case actGenerate:
		if p.Warning != "" && !p.HistoryAccepted && !p.LabelRequested {
			_ = b.tg.AnswerCallback(ctx, cq.ID, "Este cliente tiene historial.")
			_ = b.tg.EditMessage(ctx, chatID, msgID, orderText(p)+"\n\n⚠️ <b>¿Generar la guía igual?</b> Este cliente no pagó o tardó en recoger antes.",
				[][]telegram.Button{telegram.Row(
					telegram.Button{Text: "⚠️ Generar de todas formas", Data: actForce + ":" + orderID},
					telegram.Button{Text: "✋ No generar", Data: actCancel + ":" + orderID},
				)})
			return
		}
		b.quoteLabel(ctx, cq.ID, chatID, msgID, p)
	case actForce:
		if _, err := b.st.updatePending(orderID, func(p *Pending) { p.HistoryAccepted = true }); err != nil {
			log.Printf("bot: no se pudo guardar la orden %s: %v", orderID, err)
		}
		p.HistoryAccepted = true
		b.quoteLabel(ctx, cq.ID, chatID, msgID, p)
	case actConfirm:
		b.confirmLabel(ctx, cq.ID, chatID, msgID, p)
	case actCancel:
		_ = b.tg.AnswerCallback(ctx, cq.ID, "Cancelado.")
		if _, err := b.st.updatePending(orderID, func(p *Pending) { p.Quote = nil }); err != nil {
			log.Printf("bot: no se pudo guardar la orden %s: %v", orderID, err)
		}
		_ = b.tg.EditMessage(ctx, chatID, msgID, orderText(p), generateButton(orderID))
	default:
		_ = b.tg.AnswerCallback(ctx, cq.ID, "")
	}
}

// quoteLabel quotes with Interrapidísimo and shows the price with the confirm or cancel buttons.
func (b *Bot) quoteLabel(ctx context.Context, callbackID string, chatID, msgID int64, p Pending) {
	if p.LabelRequested {
		_ = b.tg.AnswerCallback(ctx, callbackID, "La guía de esta orden ya se pidió.")
		return
	}
	_ = b.tg.AnswerCallback(ctx, callbackID, "Cotizando…")
	base := orderText(p)
	_ = b.tg.EditMessage(ctx, chatID, msgID, base+"\n\n⏳ Cotizando con Interrapidísimo…", nil)

	qctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	q, err := b.sky.QuoteLabel(qctx, *p.Data)
	if err != nil {
		_ = b.tg.EditMessage(ctx, chatID, msgID, base+"\n\n❌ No se pudo cotizar: "+html.EscapeString(err.Error()), generateRetryButton(p.OrderID))
		return
	}
	if _, err := b.st.updatePending(p.OrderID, func(p *Pending) { p.Quote = &q }); err != nil {
		log.Printf("bot: no se pudo guardar la cotización de %s: %v", p.OrderID, err)
	}

	days := ""
	if q.Days > 0 {
		days = fmt.Sprintf(" · %d día(s)", q.Days)
	}
	text := fmt.Sprintf("%s\n\n🚚 <b>Interrapidísimo</b>%s · <b>%s</b>%s\n%s\n🛡️ Sin Protección Plus\n%s\n\n¿Genero la guía?",
		base, serviceName(q.Service), formatCOP(q.Total), days, cashOnDelivery(*p.Data, q.Total), b.balanceBefore(qctx, q.Total))
	_ = b.tg.EditMessage(ctx, chatID, msgID, text, [][]telegram.Button{telegram.Row(
		telegram.Button{Text: "✅ Confirmar", Data: actConfirm + ":" + p.OrderID},
		telegram.Button{Text: "❌ Cancelar", Data: actCancel + ":" + p.OrderID},
	)})
}

// balanceBefore shows the balance before confirming and warns if it isn't enough for the label.
func (b *Bot) balanceBefore(ctx context.Context, cost float64) string {
	cr, err := b.sky.Credits(ctx)
	if err != nil {
		log.Printf("bot: no se pudo consultar el saldo: %v", err)
		return "💳 Saldo: no se pudo consultar"
	}
	if cr.Balance < cost {
		return fmt.Sprintf("⚠️ <b>Saldo insuficiente:</b> tienes %s y la guía cuesta %s. Recarga en Skydropx antes de confirmar.", formatCOP(cr.Balance), formatCOP(cost))
	}
	return "💳 Saldo actual: " + formatCOP(cr.Balance)
}

// balanceAfter shows what is left after the label and warns if it isn't enough for another one like it.
func (b *Bot) balanceAfter(ctx context.Context, cost float64) string {
	cr, err := b.sky.Credits(ctx)
	if err != nil {
		log.Printf("bot: no se pudo consultar el saldo: %v", err)
		return "💳 Saldo restante: no se pudo consultar"
	}
	msg := "💳 Saldo restante: <b>" + formatCOP(cr.Balance) + "</b>"
	if cr.Balance < cost {
		msg += "\n⚠️ No alcanza para otra guía como esta. Recarga en Skydropx."
	}
	return msg
}

// isNoBalance recognizes Skydropx's rejection for lack of credits.
func isNoBalance(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "fondos insuficientes") || strings.Contains(m, "insufficient_credits") || strings.Contains(m, "saldo insuficiente")
}

// cashOnDelivery explains what the carrier charges the customer: the product plus shipping.
func cashOnDelivery(data domain.ShipmentData, shp float64) string {
	qty := data.Quantity
	if qty < 1 {
		qty = 1
	}
	productName := data.UnitPrice * float64(qty)
	return fmt.Sprintf("💵 Contra entrega, el cliente paga el envío: cobra <b>%s</b> al recibir (producto %s + envío %s)",
		formatCOP(productName+shp), formatCOP(productName), formatCOP(shp))
}

func serviceName(s string) string {
	if s = strings.TrimSpace(s); s == "" {
		return ""
	}
	return " " + html.EscapeString(s)
}

func generateRetryButton(orderID string) [][]telegram.Button {
	return [][]telegram.Button{telegram.Row(telegram.Button{Text: "🔄 Reintentar", Data: actGenerate + ":" + orderID})}
}

// confirmLabel creates the shipment with the quoted rate. The label is posted later by the pending check.
func (b *Bot) confirmLabel(ctx context.Context, callbackID string, chatID, msgID int64, p Pending) {
	if p.Quote == nil {
		_ = b.tg.AnswerCallback(ctx, callbackID, "Primero cotiza la guía.")
		return
	}
	// It is marked before calling Skydropx so a second tap doesn't create another label.
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
	base := orderText(p)
	_ = b.tg.EditMessage(ctx, chatID, msgID, base+"\n\n⏳ Generando la guía con Interrapidísimo…", nil)

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	shipmentID, err := b.sky.CreateLabel(cctx, p.OrderID, *p.Quote, *p.Data)
	if err != nil {
		if _, serr := b.st.updatePending(p.OrderID, func(p *Pending) { p.LabelRequested = false; p.Quote = nil }); serr != nil {
			log.Printf("bot: no se pudo guardar la orden %s: %v", p.OrderID, serr)
		}
		msg := "❌ No se generó la guía: " + html.EscapeString(err.Error())
		if isNoBalance(err) {
			msg = "❌ <b>No se generó la guía: saldo insuficiente en Skydropx.</b>\n" + b.balanceBefore(cctx, p.Quote.Total) + "\nRecarga en Skydropx y toca Reintentar."
		}
		_ = b.tg.EditMessage(ctx, chatID, msgID, base+"\n\n"+msg, generateRetryButton(p.OrderID))
		return
	}
	// The customer's address stays in Mis direcciones under their name; if it fails, the label goes on anyway.
	addressLine := "📒 Dirección guardada en Mis direcciones como \"" + html.EscapeString(p.Data.Name) + "\"."
	if err := b.sky.SaveRecipientAddress(cctx, *p.Data, p.Quote.PostalCode); err != nil {
		log.Printf("bot: no se pudo guardar la dirección de %s: %v", p.OrderID, err)
		addressLine = "⚠️ No se pudo guardar la dirección en Mis direcciones: " + html.EscapeString(err.Error())
	}
	_ = b.tg.EditMessage(ctx, chatID, msgID, fmt.Sprintf(
		"%s\n\n✅ <b>Guía solicitada</b> con Interrapidísimo (%s, sin Protección Plus).\n%s\n%s\n%s\n\nEn cuanto Skydropx la genere, llega al grupo del vendedor.",
		base, formatCOP(p.Quote.Total), cashOnDelivery(*p.Data, p.Quote.Total), addressLine, b.balanceAfter(cctx, p.Quote.Total)), nil)

	// Skydropx takes a few seconds to generate the label: it is checked soon instead of waiting for the periodic check.
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

// maxPrintDays is the most days back /imprimir accepts.
const maxPrintDays = 15

// daysToPrint reads the number of days from "/imprimir 3" (or "/imprimir@bot 3"). Without a number it is 1 (today).
func daysToPrint(text string) int {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return 1
	}
	n, err := strconv.Atoi(fields[1])
	if err != nil || n < 1 {
		return 1
	}
	if n > maxPrintDays {
		return maxPrintDays
	}
	return n
}

// periodText describes the days being printed: "de hoy" or "de los últimos 3 días".
func periodText(days int) string {
	if days <= 1 {
		return "de hoy"
	}
	return fmt.Sprintf("de los últimos %d días", days)
}

// printLabels lays out the labels of the last days days on letter sheets of 4 and sends them as a PDF.
func (b *Bot) printLabels(ctx context.Context, chatID int64, days int) {
	b.say(ctx, chatID, "⏳ Buscando las guías "+periodText(days)+"…")
	pctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	lbls, err := b.sky.RecentLabels(pctx, days)
	if errors.Is(err, skydropx.ErrMockMode) {
		b.say(ctx, chatID, "Modo simulación: no hay guías reales para imprimir.")
		return
	}
	if err != nil {
		b.say(ctx, chatID, "❌ No pude consultar las guías: "+html.EscapeString(err.Error()))
		return
	}
	if len(lbls) == 0 {
		if days <= 1 {
			b.say(ctx, chatID, "Hoy todavía no hay guías generadas. Para incluir días anteriores escribe, por ejemplo, /imprimir 3")
		} else {
			b.say(ctx, chatID, "No hay guías generadas "+periodText(days)+".")
		}
		return
	}

	var pdfs [][]byte
	var failedLabels []string
	for _, g := range lbls {
		data, _, err := b.sky.DownloadLabel(pctx, g.LabelURL)
		if err != nil {
			log.Printf("bot: no se pudo descargar la guía %s: %v", g.Tracking, err)
			failedLabels = append(failedLabels, g.Tracking)
			continue
		}
		pdfs = append(pdfs, data)
	}
	if len(pdfs) == 0 {
		b.say(ctx, chatID, "❌ No pude descargar ninguna guía "+periodText(days)+".")
		return
	}
	sheets, err := labels.Sheets(pctx, pdfs)
	if err != nil {
		b.say(ctx, chatID, "❌ No pude armar las hojas: "+html.EscapeString(err.Error()))
		return
	}

	numSheets := (len(pdfs) + labels.PerSheet - 1) / labels.PerSheet
	caption := fmt.Sprintf("🖨️ <b>%d guía(s) %s</b> en %d hoja(s) carta.", len(pdfs), periodText(days), numSheets)
	if len(failedLabels) > 0 {
		caption += "\n⚠️ No se pudieron descargar: " + html.EscapeString(strings.Join(failedLabels, ", "))
	}
	fullName := fmt.Sprintf("guias-%s.pdf", time.Now().In(colombiaTZ).Format("2006-01-02"))
	if err := b.tg.SendDocument(ctx, chatID, fullName, sheets, caption); err != nil {
		log.Printf("bot: no se pudo enviar el PDF de guías: %v", err)
		b.say(ctx, chatID, "❌ No pude enviar el PDF: "+html.EscapeString(err.Error()))
	}
}

// colombiaTZ is Colombia time (UTC-5, no daylight saving); the Docker image has no time zones.
var colombiaTZ = time.FixedZone("COT", -5*3600)

// opsChats are the chats that handle labels: TELEGRAM_GESTION_CHAT_IDS or, if unset, the admins group.
func (b *Bot) opsChats() []int64 {
	if len(b.opts.OpsChatIDs) > 0 {
		return b.opts.OpsChatIDs
	}
	return b.opts.AdminChatIDs
}

func (b *Bot) isOps(chatID int64) bool {
	for _, id := range b.opsChats() {
		if id == chatID {
			return true
		}
	}
	return false
}
