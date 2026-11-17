// Package bot receives the seller's messages over Telegram, creates the order in Skydropx
// and notifies when the shipment's label is ready.
package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"skydropx-automator/internal/customers"
	"skydropx-automator/internal/domain"
	"skydropx-automator/internal/parser"
	"skydropx-automator/internal/settings"
	"skydropx-automator/internal/skydropx"
	"skydropx-automator/internal/telegram"
)

type Options struct {
	Token            string
	APIBase          string           // empty = official Telegram API
	AllowedChatIDs   []int64          // authorized chats (you and your seller)
	AdminChatIDs     []int64          // group where each ready label is posted
	OpsChatIDs       []int64          // private group that receives new orders with the generate-label button and uses /imprimir
	DefaultUnitPrice float64          // unit price if the message has no "Valor:"
	StatePath        string           // JSON file with the offset and pending orders
	CheckEvery       time.Duration    // how often to check for new labels
	Settings         *settings.Store  // seller's commission (changed with /comision)
	Customers        *customers.Store // customer database in MongoDB (nil if MONGODB_URI isn't set)
	CustomersError   string           // why MongoDB didn't connect at startup ("" if it did or isn't configured)
}

type Bot struct {
	opts Options
	tg   *telegram.Client
	sky  *skydropx.Client
	st   *State

	checkMu sync.Mutex // keeps the webhook and the periodic check from announcing the same label twice
	codMu   sync.Mutex // keeps two delivery webhooks from announcing the same payment twice
}

func New(opts Options, sky *skydropx.Client) (*Bot, error) {
	st, err := loadState(opts.StatePath)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer el estado del bot: %w", err)
	}
	return &Bot{opts: opts, tg: telegram.New(opts.Token, opts.APIBase), sky: sky, st: st}, nil
}

// Run handles messages and checks labels until ctx is done.
func (b *Bot) Run(ctx context.Context) {
	b.setupCommands(ctx)
	b.customersAtStartup(ctx)
	go b.watchLabels(ctx)
	go b.watchSummaries(ctx)

	for ctx.Err() == nil {
		updates, err := b.tg.GetUpdates(ctx, b.st.Offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("bot: error consultando Telegram: %v", err)
			sleep(ctx, 5*time.Second)
			continue
		}
		for _, u := range updates {
			if u.Message != nil {
				if isRestoreCommand(u.Message) {
					b.restoreCommand(ctx, u.Message)
				} else {
					b.handle(ctx, u.Message.Chat.ID, senderName(u.Message.From), strings.TrimSpace(u.Message.Text))
				}
			}
			if u.CallbackQuery != nil {
				b.handleCallback(ctx, u.CallbackQuery)
			}
			if err := b.st.setOffset(u.UpdateID + 1); err != nil {
				log.Printf("bot: no se pudo guardar el progreso: %v", err)
			}
		}
	}
}

func (b *Bot) handle(ctx context.Context, chatID int64, sender, text string) {
	// Your ops group: /imprimir and help. Labels are generated with each order's buttons.
	if b.isOps(chatID) {
		if strings.HasPrefix(text, "/imprimir") {
			b.printLabels(ctx, chatID, daysToPrint(text))
			return
		}
		if strings.HasPrefix(text, "/comision") {
			b.commissionCommand(ctx, chatID, text)
			return
		}
		if strings.HasPrefix(text, "/resumen") {
			b.summaryCommand(ctx, chatID, text)
			return
		}
		if strings.HasPrefix(text, "/deuda") {
			b.debtCommand(ctx, chatID, text)
			return
		}
		if strings.HasPrefix(text, "/publicidad") {
			b.adsCommand(ctx, chatID, text)
			return
		}
		if strings.HasPrefix(text, "/pagos") {
			b.paymentsCommand(ctx, chatID)
			return
		}
		if strings.HasPrefix(text, "/pago") {
			b.paymentCommand(ctx, chatID, text)
			return
		}
		if strings.HasPrefix(text, "/novedades") {
			b.issuesCommand(ctx, chatID)
			return
		}
		if strings.HasPrefix(text, "/clientes") {
			b.customersCommand(ctx, chatID, text)
			return
		}
		if strings.HasPrefix(text, "/despachos") {
			b.dispatchCommand(ctx, chatID)
			return
		}
		if strings.HasPrefix(text, "/autoriza") {
			b.consentCommand(ctx, chatID, sender, text)
			return
		}
		if strings.HasPrefix(text, "/baja") {
			b.optOutCommand(ctx, chatID, text)
			return
		}
		if strings.HasPrefix(text, "/cliente") {
			b.customerCommand(ctx, chatID, text)
			return
		}
		if strings.HasPrefix(text, "/guia") {
			b.labelCommand(ctx, chatID, text)
			return
		}
		if text == "" || strings.HasPrefix(text, "/start") || strings.HasPrefix(text, "/ayuda") {
			b.say(ctx, chatID, helpOps)
			return
		}
		if !b.isAllowed(chatID) {
			return // no orders are created in the ops group
		}
	}
	// The labels group (TELEGRAM_ADMIN_CHAT_IDS) receives orders and labels, but no orders are created there.
	if !b.isAllowed(chatID) && b.isAdmin(chatID) {
		if strings.HasPrefix(text, "/cliente") {
			b.customerCommand(ctx, chatID, text)
			return
		}
		if strings.HasPrefix(text, "/guia") {
			b.labelCommand(ctx, chatID, text)
			return
		}
		if text == "" || strings.HasPrefix(text, "/start") || strings.HasPrefix(text, "/ayuda") {
			b.say(ctx, chatID, helpLabelsGroup)
		}
		return // other messages in the group (conversation, forwards) are ignored without replying
	}
	if !b.isAllowed(chatID) {
		log.Printf("bot: chat no autorizado %d", chatID)
		b.say(ctx, chatID, fmt.Sprintf("No estás autorizado para usar este bot. Tu ID de chat es <code>%d</code>. Pásaselo al administrador para que te dé acceso.", chatID))
		return
	}
	// Commission and summaries are only handled from the ops group.
	if strings.HasPrefix(text, "/comision") || strings.HasPrefix(text, "/resumen") || strings.HasPrefix(text, "/novedades") ||
		strings.HasPrefix(text, "/deuda") || strings.HasPrefix(text, "/pago") ||
		strings.HasPrefix(text, "/publicidad") || strings.HasPrefix(text, "/clientes") || strings.HasPrefix(text, "/autoriza") || strings.HasPrefix(text, "/despachos") ||
		strings.HasPrefix(text, "/baja") {
		b.say(ctx, chatID, "Este comando solo funciona en el grupo de gestión.")
		return
	}
	if text == "" || strings.HasPrefix(text, "/start") || strings.HasPrefix(text, "/ayuda") {
		b.say(ctx, chatID, helpSeller)
		return
	}
	if strings.HasPrefix(text, "/cliente") {
		b.customerCommand(ctx, chatID, text)
		return
	}
	if strings.HasPrefix(text, "/guia") {
		b.labelCommand(ctx, chatID, text)
		return
	}
	b.createOrder(ctx, chatID, sender, text)
}

// senderName builds a readable name of who wrote, for the admin notice.
func senderName(u *telegram.User) string {
	if u == nil {
		return ""
	}
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if u.Username != "" {
		if name == "" {
			return "@" + u.Username
		}
		name += " (@" + u.Username + ")"
	}
	return name
}

// duplicateWindow is how long an order with the same data is considered a repeat.
const duplicateWindow = 24 * time.Hour

// undoRepeat detects "REPETIR" on the first line: the seller confirms the repeated order is new.
func undoRepeat(text string) (string, bool) {
	first, rest, _ := strings.Cut(text, "\n")
	if strings.EqualFold(strings.Trim(strings.TrimSpace(first), "*_ "), "repetir") {
		return strings.TrimSpace(rest), true
	}
	return text, false
}

// fingerprint identifies an order by recipient, address, quantity and price, ignoring
// case, accents, spaces and punctuation.
func fingerprint(d domain.ShipmentData) string {
	parts := []string{
		d.Document, d.Phone, d.City, d.Address,
		strconv.Itoa(d.Quantity), strconv.FormatFloat(d.UnitPrice, 'f', 0, 64),
	}
	for i, p := range parts {
		parts[i] = normalize(p)
	}
	return strings.Join(parts, "|")
}

func howLongAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "menos de un minuto"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
}

// labelDestination says where the label is posted, for the messages to the seller.
func (b *Bot) labelDestination() string {
	if len(b.opts.AdminChatIDs) > 0 {
		return "al grupo"
	}
	return "a este chat"
}

// isAdmin reports whether the chat is the seller's group (TELEGRAM_ADMIN_CHAT_IDS).
func (b *Bot) isAdmin(chatID int64) bool {
	for _, id := range b.opts.AdminChatIDs {
		if id == chatID {
			return true
		}
	}
	return false
}

func (b *Bot) isAllowed(chatID int64) bool {
	for _, id := range b.opts.AllowedChatIDs {
		if id == chatID {
			return true
		}
	}
	return false
}

func (b *Bot) createOrder(ctx context.Context, chatID int64, sender, text string) {
	text, force := undoRepeat(text)
	data := parser.ParseWhatsAppMessage(text).Data
	if data.UnitPrice <= 0 {
		data.UnitPrice = b.opts.DefaultUnitPrice
	}
	if missing := parser.ValidateShipmentData(data); len(missing) > 0 {
		b.say(ctx, chatID, "⚠️ Faltan datos para crear la orden: <b>"+html.EscapeString(strings.Join(missing, ", "))+"</b>. Envíame el mensaje completo de nuevo.")
		return
	}

	h := fingerprint(data)
	from := time.Now().Add(-duplicateWindow)
	if prev, ok := b.st.findRecent(h, from); ok && !force {
		b.say(ctx, chatID, fmt.Sprintf(
			"⚠️ <b>Esta orden ya se creó</b> hace %s para <b>%s</b> con los mismos datos, así que no la volví a crear.\n\nSi de verdad es un pedido nuevo, envíame el mensaje otra vez escribiendo <code>REPETIR</code> en la primera línea.",
			howLongAgo(prev.CreatedAt), html.EscapeString(prev.Name),
		))
		return
	}

	b.say(ctx, chatID, "⏳ Creando la orden…")
	createCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	res, err := b.sky.CreateShipment(createCtx, data)
	if err != nil {
		b.say(ctx, chatID, "❌ No se creó la orden: "+html.EscapeString(err.Error()))
		return
	}

	// The customer's Skydropx history (didn't pay or was slow to pick up) is shown with the order.
	hist, err := b.checkCustomer(ctx, data.Document, data.Phone)
	if err != nil {
		log.Printf("bot: no se pudo revisar el historial del cliente: %v", err)
	}
	warning := carrierOpsWarning(data) + opsWarning(hist)

	if err := b.st.add(Pending{
		Warning:     warning,
		OrderID:     res.ID,
		ChatID:      chatID,
		Name:        data.Name,
		Destination: fmt.Sprintf("%s, %s", data.City, data.Department),
		CreatedAt:   time.Now(),
		Sender:      senderOrChat(sender, chatID),
		Data:        &data,
	}); err != nil {
		log.Printf("bot: no se pudo guardar la orden pendiente %s: %v", res.ID, err)
	}
	if err := b.st.addRecent(Recent{Fingerprint: h, OrderID: res.ID, Name: data.Name, CreatedAt: time.Now()}, from); err != nil {
		log.Printf("bot: no se pudo guardar la orden reciente %s: %v", res.ID, err)
	}

	msg := fmt.Sprintf(
		"✅ <b>Orden creada</b>\n👤 %s\n📍 %s, %s\n💰 Valor unitario: %s\n\nCuando finalicen el envío en Skydropx, la guía y el PDF llegarán %s.",
		html.EscapeString(data.Name), html.EscapeString(data.City), html.EscapeString(data.Department),
		formatCOP(data.UnitPrice), b.labelDestination(),
	)
	if !res.IsMock {
		b.recordCustomer(ctx, data, res.ID, senderOrChat(sender, chatID))
	}
	seller := carrierSellerWarning(data) + sellerWarning(hist)
	msg += seller
	if res.IsMock {
		msg += "\n\n<i>(Modo simulación: no se creó nada en Skydropx.)</i>"
	}
	b.say(ctx, chatID, msg)
	b.notifyAdmins(ctx, senderOrChat(sender, chatID), data, res.ID, res.IsMock, warning, seller)
}

func senderOrChat(sender string, chatID int64) string {
	if sender == "" {
		return fmt.Sprintf("chat %d", chatID)
	}
	return sender
}

// notifyAdmins announces the new order: to the ops group with the button to generate the label
// and to the seller's group (TELEGRAM_ADMIN_CHAT_IDS) only as a notice.
// warning goes above the ops notice; seller, at the end of the seller group's notice.
func (b *Bot) notifyAdmins(ctx context.Context, sender string, data domain.ShipmentData, orderID string, isMock bool, warning, seller string) {
	msg := warning + adminOrderText(sender, data)
	sellerMsg := adminOrderText(sender, data) + seller
	var buttons [][]telegram.Button
	if isMock {
		msg += "\n\n<i>(Modo simulación: no se creó nada en Skydropx.)</i>"
	} else {
		buttons = generateButton(orderID)
	}
	for _, id := range b.opsChats() {
		if _, err := b.tg.SendMessageWithButtons(ctx, id, msg, buttons); err != nil {
			log.Printf("bot: no se pudo avisar la orden nueva: %v", err)
		}
	}
	// The seller's group also sees the order, but without buttons: the label is only handled in the ops group.
	for _, id := range b.opts.AdminChatIDs {
		if b.isOps(id) {
			continue // already got the notice with buttons
		}
		b.say(ctx, id, sellerMsg+"\n\n📄 La guía llegará aquí cuando esté lista.")
	}
}

// watchLabels periodically checks pending orders and notifies when they have a label.
func (b *Bot) watchLabels(ctx context.Context) {
	sleep(ctx, 30*time.Second)
	ticker := time.NewTicker(b.opts.CheckEvery)
	defer ticker.Stop()
	for {
		b.checkLabels(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (b *Bot) checkLabels(ctx context.Context) {
	for _, p := range b.st.snapshotPending() {
		if ctx.Err() != nil {
			return
		}
		b.checkPending(ctx, p, nil)
	}
}

// checkPending announces the label of a pending order if it has one and removes it from pending.
// If shipmentIDs is empty, the order's shipments are looked up in Skydropx.
func (b *Bot) checkPending(ctx context.Context, p Pending, shipmentIDs []string) {
	b.checkMu.Lock()
	defer b.checkMu.Unlock()
	if !b.st.isPending(p.OrderID) {
		return // already announced (by the webhook or the periodic check)
	}
	if len(shipmentIDs) == 0 {
		ids, err := b.sky.OrderShipmentIDs(ctx, p.OrderID)
		if err != nil {
			if !errors.Is(err, skydropx.ErrMockMode) {
				log.Printf("bot: no se pudo consultar la orden %s: %v", p.OrderID, err)
			}
			return
		}
		shipmentIDs = ids
	}
	if sh, ok := b.shipmentsLabel(ctx, shipmentIDs); ok {
		// The label goes to the group (TELEGRAM_ADMIN_CHAT_IDS); if no group is set, to the chat that requested the order.
		targets := b.opts.AdminChatIDs
		if len(targets) == 0 {
			targets = []int64{p.ChatID}
		}
		msg := sh.message()
		for _, id := range targets {
			b.say(ctx, id, msg)
		}
		if err := b.st.remove(p.OrderID); err != nil {
			log.Printf("bot: no se pudo actualizar la orden %s: %v", p.OrderID, err)
		}
	}
}

// shipmentsLabel finds the first shipment in the list that already has a tracking number.
func (b *Bot) shipmentsLabel(ctx context.Context, ids []string) (shipmentInfo, bool) {
	for _, id := range ids {
		raw, err := b.sky.GetShipment(ctx, id)
		if err != nil {
			log.Printf("bot: no se pudo consultar el envío %s: %v", id, err)
			continue
		}
		list, err := shipmentListFromDetail(raw)
		if err != nil {
			continue
		}
		for _, sh := range parseShipments(list) {
			if sh.tracking != "" {
				return sh, true
			}
		}
	}
	return shipmentInfo{}, false
}

// shipmentListFromDetail turns GET /api/v1/shipments/{id} (a single "data") into the list shape.
func shipmentListFromDetail(raw json.RawMessage) (skydropx.ShipmentList, error) {
	var d struct {
		Data     json.RawMessage   `json:"data"`
		Included []json.RawMessage `json:"included"`
	}
	if err := json.Unmarshal(raw, &d); err != nil || len(d.Data) == 0 {
		return skydropx.ShipmentList{}, fmt.Errorf("respuesta de envío inesperada")
	}
	return skydropx.ShipmentList{Data: []json.RawMessage{d.Data}, Included: d.Included}, nil
}

func (b *Bot) say(ctx context.Context, chatID int64, html string) {
	if err := b.tg.SendMessage(ctx, chatID, html); err != nil {
		log.Printf("bot: no se pudo enviar el mensaje: %v", err)
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func formatCOP(v float64) string {
	s := fmt.Sprintf("%.0f", v)
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, c)
	}
	return "$" + string(out)
}

// ---------- Reading Skydropx shipments ----------

type shipmentInfo struct {
	orderID     string
	tracking    string
	carrier     string
	service     string
	label       string
	trackingURL string
	onDelivery  string // cash-on-delivery collection state: pending | collected | empty
	fullName    string
	dest        string
}

type shipmentIndex []shipmentInfo

func (idx shipmentIndex) byOrder(orderID string) (shipmentInfo, bool) {
	for _, s := range idx {
		if s.orderID == orderID && s.tracking != "" {
			return s, true
		}
	}
	return shipmentInfo{}, false
}

// message builds the label with the same format as "Mis envíos": WhatsApp *bold* and full URLs,
// so the seller can copy it as is to the customer.
func (s shipmentInfo) message() string {
	carrierText := carrierDisplayName(s.carrier)
	if s.service != "" {
		carrierText += " - " + s.service
	}
	followUp := s.trackingURL
	if followUp == "" {
		followUp = carrierTrackingURL[normalize(s.carrier)]
	}
	if followUp == "" {
		followUp = "pendiente"
	}
	pdf := s.label
	if pdf == "" {
		pdf = "pendiente (la guía aún no está disponible)"
	}
	return fmt.Sprintf(
		"✅ *Envío creado con éxito*\n\n"+
			"📦 *Guía:* %s\n"+
			"👤 *Cliente:* %s\n"+
			"📍 *Destino:* %s\n"+
			"🚚 *Transportadora:* %s\n"+
			"📊 *Estado:* pendiente de rastreo\n"+
			"💰 *Cobro:* %s\n"+
			"🔎 *Seguimiento:* %s\n\n"+
			"📄 *Descarga la guía (PDF):*\n%s",
		html.EscapeString(s.tracking), html.EscapeString(s.fullName), html.EscapeString(s.dest),
		html.EscapeString(carrierText), codText(s.onDelivery),
		html.EscapeString(followUp), html.EscapeString(pdf),
	)
}

func codText(onDelivery string) string {
	switch onDelivery {
	case "collected":
		return "Cobrado (pago contra entrega)"
	case "pending":
		return "Por cobrar (pago contra entrega)"
	default:
		return "No aplica"
	}
}

var carrierNames = map[string]string{
	"interrapidisimo": "Interrapidísimo",
	"servientrega":    "Servientrega",
	"coordinadora":    "Coordinadora",
	"envia":           "Envía",
	"fedex":           "FedEx",
	"ups":             "UPS",
}

// carrierTrackingURL is each carrier's tracking page, if Skydropx doesn't send it.
var carrierTrackingURL = map[string]string{
	"interrapidisimo": "https://siguetuenvio.interrapidisimo.com/",
	"servientrega":    "https://www.servientrega.com/wps/portal",
	"envia":           "https://envia.co/",
	"coordinadora":    "https://coordinadora.com/",
}

// normalize keeps only unaccented letters and digits, lowercase: "Interrapidísimo" -> "interrapidisimo".
func normalize(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch r {
		case 'á':
			r = 'a'
		case 'é':
			r = 'e'
		case 'í':
			r = 'i'
		case 'ó':
			r = 'o'
		case 'ú', 'ü':
			r = 'u'
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func carrierDisplayName(name string) string {
	if n, ok := carrierNames[normalize(name)]; ok {
		return n
	}
	return strings.TrimSpace(name)
}

func parseShipments(list skydropx.ShipmentList) shipmentIndex {
	type shipment struct {
		ID         string `json:"id"`
		Attributes struct {
			OrderID     string `json:"order_id"`
			Tracking    string `json:"master_tracking_number"`
			CarrierName string `json:"carrier_name"`
			ServiceName string `json:"service_name"`
			OnDelivery  string `json:"on_delivery_status"`
		} `json:"attributes"`
		Relationships struct {
			AddressTo struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			} `json:"address_to"`
			Packages struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			} `json:"packages"`
		} `json:"relationships"`
	}
	type included struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Name        string `json:"name"`
			City        string `json:"area_level2"`
			State       string `json:"area_level1"`
			LabelURL    string `json:"label_url"`
			TrackingURL string `json:"tracking_url_provider"`
		} `json:"attributes"`
	}

	inc := map[string]included{}
	for _, raw := range list.Included {
		var i included
		if json.Unmarshal(raw, &i) == nil {
			inc[i.Type+":"+i.ID] = i
		}
	}

	var out shipmentIndex
	for _, raw := range list.Data {
		var s shipment
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		info := shipmentInfo{
			orderID:    s.Attributes.OrderID,
			tracking:   s.Attributes.Tracking,
			carrier:    s.Attributes.CarrierName,
			service:    s.Attributes.ServiceName,
			onDelivery: s.Attributes.OnDelivery,
		}
		if a, ok := inc["address:"+s.Relationships.AddressTo.Data.ID]; ok {
			info.fullName = a.Attributes.Name
			info.dest = strings.Trim(a.Attributes.City+", "+a.Attributes.State, ", ")
		}
		for _, p := range s.Relationships.Packages.Data {
			if pkg, ok := inc["package:"+p.ID]; ok && pkg.Attributes.LabelURL != "" {
				info.label = pkg.Attributes.LabelURL
				info.trackingURL = pkg.Attributes.TrackingURL
				break
			}
		}
		out = append(out, info)
	}
	return out
}
