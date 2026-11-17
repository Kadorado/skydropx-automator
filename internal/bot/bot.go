// Package bot recibe los mensajes del vendedor por Telegram, crea la orden en Skydropx
// y avisa cuando la guía del envío ya está generada.
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
	Token          string
	APIBase        string           // vacío = API oficial de Telegram
	AllowedChatIDs []int64          // chats autorizados (tú y tu vendedor)
	AdminChatIDs   []int64          // grupo donde se publica cada guía lista
	GestionChatIDs []int64          // grupo privado que recibe las órdenes nuevas con el botón de generar guía y usa /imprimir
	DefaultValor   float64          // valor unitario si el mensaje no trae "Valor:"
	StatePath      string           // archivo JSON con offset y órdenes pendientes
	CheckEvery     time.Duration    // cada cuánto revisar si hay guías nuevas
	Settings       *settings.Store  // comisión del vendedor (se cambia con /comision)
	Customers      *customers.Store // base de clientes en MongoDB (nil si MONGODB_URI no está configurada)
}

type Bot struct {
	opts Options
	tg   *telegram.Client
	sky  *skydropx.Client
	st   *State

	checkMu sync.Mutex // evita que el webhook y la revisión periódica avisen dos veces la misma guía
	cobroMu sync.Mutex // evita que dos webhooks de entrega avisen dos veces el mismo pago
}

func New(opts Options, sky *skydropx.Client) (*Bot, error) {
	st, err := loadState(opts.StatePath)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer el estado del bot: %w", err)
	}
	return &Bot{opts: opts, tg: telegram.New(opts.Token, opts.APIBase), sky: sky, st: st}, nil
}

// Run atiende mensajes y revisa guías hasta que ctx termine.
func (b *Bot) Run(ctx context.Context) {
	b.configurarComandos(ctx)
	go b.watchGuias(ctx)
	go b.watchResumenes(ctx)

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
				b.handle(ctx, u.Message.Chat.ID, senderName(u.Message.From), strings.TrimSpace(u.Message.Text))
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
	// Tu grupo de gestión: /imprimir y ayuda. Las guías se generan con los botones de cada orden.
	if b.isGestion(chatID) {
		if strings.HasPrefix(text, "/imprimir") {
			b.imprimir(ctx, chatID, diasAImprimir(text))
			return
		}
		if strings.HasPrefix(text, "/comision") {
			b.comandoComision(ctx, chatID, text)
			return
		}
		if strings.HasPrefix(text, "/resumen") {
			b.comandoResumen(ctx, chatID, text)
			return
		}
		if strings.HasPrefix(text, "/deuda") {
			b.comandoDeuda(ctx, chatID, text)
			return
		}
		if strings.HasPrefix(text, "/publicidad") {
			b.comandoPublicidad(ctx, chatID, text)
			return
		}
		if strings.HasPrefix(text, "/pagos") {
			b.comandoPagos(ctx, chatID)
			return
		}
		if strings.HasPrefix(text, "/pago") {
			b.comandoPago(ctx, chatID, text)
			return
		}
		if strings.HasPrefix(text, "/novedades") {
			b.comandoNovedades(ctx, chatID)
			return
		}
		if strings.HasPrefix(text, "/clientes") {
			b.customersCommand(ctx, chatID, text)
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
			b.comandoCliente(ctx, chatID, text)
			return
		}
		if strings.HasPrefix(text, "/guia") {
			b.comandoGuia(ctx, chatID, text)
			return
		}
		if text == "" || strings.HasPrefix(text, "/start") || strings.HasPrefix(text, "/ayuda") {
			b.say(ctx, chatID, ayudaGestion)
			return
		}
		if !b.isAllowed(chatID) {
			return // en el grupo de gestión no se crean órdenes
		}
	}
	// El grupo de guías (TELEGRAM_ADMIN_CHAT_IDS) recibe órdenes y guías, pero ahí no se crean pedidos.
	if !b.isAllowed(chatID) && b.isAdmin(chatID) {
		if strings.HasPrefix(text, "/cliente") {
			b.comandoCliente(ctx, chatID, text)
			return
		}
		if strings.HasPrefix(text, "/guia") {
			b.comandoGuia(ctx, chatID, text)
			return
		}
		if text == "" || strings.HasPrefix(text, "/start") || strings.HasPrefix(text, "/ayuda") {
			b.say(ctx, chatID, ayudaGrupoGuias)
		}
		return // los demás mensajes del grupo (conversación, reenvíos) se ignoran sin responder
	}
	if !b.isAllowed(chatID) {
		log.Printf("bot: chat no autorizado %d", chatID)
		b.say(ctx, chatID, fmt.Sprintf("No estás autorizado para usar este bot. Tu ID de chat es <code>%d</code>. Pásaselo al administrador para que te dé acceso.", chatID))
		return
	}
	// La comisión y los resúmenes solo se manejan desde el grupo de gestión.
	if strings.HasPrefix(text, "/comision") || strings.HasPrefix(text, "/resumen") || strings.HasPrefix(text, "/novedades") ||
		strings.HasPrefix(text, "/deuda") || strings.HasPrefix(text, "/pago") ||
		strings.HasPrefix(text, "/publicidad") || strings.HasPrefix(text, "/clientes") || strings.HasPrefix(text, "/autoriza") ||
		strings.HasPrefix(text, "/baja") {
		b.say(ctx, chatID, "Este comando solo funciona en el grupo de gestión.")
		return
	}
	if text == "" || strings.HasPrefix(text, "/start") || strings.HasPrefix(text, "/ayuda") {
		b.say(ctx, chatID, ayudaVendedor)
		return
	}
	if strings.HasPrefix(text, "/cliente") {
		b.comandoCliente(ctx, chatID, text)
		return
	}
	if strings.HasPrefix(text, "/guia") {
		b.comandoGuia(ctx, chatID, text)
		return
	}
	b.createOrder(ctx, chatID, sender, text)
}

// senderName arma un nombre legible de quien escribió, para el aviso al administrador.
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

// duplicateWindow es el tiempo en que un pedido con los mismos datos se considera repetido.
const duplicateWindow = 24 * time.Hour

// quitarRepetir detecta "REPETIR" en la primera línea: el vendedor confirma que el pedido repetido es nuevo.
func quitarRepetir(text string) (string, bool) {
	first, rest, _ := strings.Cut(text, "\n")
	if strings.EqualFold(strings.Trim(strings.TrimSpace(first), "*_ "), "repetir") {
		return strings.TrimSpace(rest), true
	}
	return text, false
}

// huella identifica un pedido por destinatario, dirección, cantidad y valor, sin importar
// mayúsculas, tildes, espacios ni signos.
func huella(d domain.ShipmentData) string {
	parts := []string{
		d.Documento, d.Telefono, d.Ciudad, d.Direccion,
		strconv.Itoa(d.Cantidad), strconv.FormatFloat(d.ValorUnitario, 'f', 0, 64),
	}
	for i, p := range parts {
		parts[i] = normalizar(p)
	}
	return strings.Join(parts, "|")
}

func haceCuanto(t time.Time) string {
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

// guiaDestino dice dónde se publica la guía, para los mensajes al vendedor.
func (b *Bot) guiaDestino() string {
	if len(b.opts.AdminChatIDs) > 0 {
		return "al grupo"
	}
	return "a este chat"
}

// isAdmin dice si el chat es el grupo del vendedor (TELEGRAM_ADMIN_CHAT_IDS).
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
	text, forzar := quitarRepetir(text)
	data := parser.ParseWhatsAppMessage(text).Data
	if data.ValorUnitario <= 0 {
		data.ValorUnitario = b.opts.DefaultValor
	}
	if missing := parser.ValidateShipmentData(data); len(missing) > 0 {
		b.say(ctx, chatID, "⚠️ Faltan datos para crear la orden: <b>"+html.EscapeString(strings.Join(missing, ", "))+"</b>. Envíame el mensaje completo de nuevo.")
		return
	}

	h := huella(data)
	desde := time.Now().Add(-duplicateWindow)
	if prev, ok := b.st.findRecent(h, desde); ok && !forzar {
		b.say(ctx, chatID, fmt.Sprintf(
			"⚠️ <b>Esta orden ya se creó</b> hace %s para <b>%s</b> con los mismos datos, así que no la volví a crear.\n\nSi de verdad es un pedido nuevo, envíame el mensaje otra vez escribiendo <code>REPETIR</code> en la primera línea.",
			haceCuanto(prev.CreatedAt), html.EscapeString(prev.Nombre),
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

	// El historial del cliente en Skydropx (no pagó o tardó en recoger) se avisa con la orden.
	hist, err := b.revisarCliente(ctx, data.Documento, data.Telefono)
	if err != nil {
		log.Printf("bot: no se pudo revisar el historial del cliente: %v", err)
	}
	advertencia := advertenciaGestion(hist)

	if err := b.st.add(Pending{
		Advertencia: advertencia,
		OrderID:     res.ID,
		ChatID:      chatID,
		Nombre:      data.Nombre,
		Destino:     fmt.Sprintf("%s, %s", data.Ciudad, data.Departamento),
		CreatedAt:   time.Now(),
		Sender:      senderOrChat(sender, chatID),
		Data:        &data,
	}); err != nil {
		log.Printf("bot: no se pudo guardar la orden pendiente %s: %v", res.ID, err)
	}
	if err := b.st.addRecent(Recent{Huella: h, OrderID: res.ID, Nombre: data.Nombre, CreatedAt: time.Now()}, desde); err != nil {
		log.Printf("bot: no se pudo guardar la orden reciente %s: %v", res.ID, err)
	}

	msg := fmt.Sprintf(
		"✅ <b>Orden creada</b>\n👤 %s\n📍 %s, %s\n💰 Valor unitario: %s\n\nCuando finalicen el envío en Skydropx, la guía y el PDF llegarán %s.",
		html.EscapeString(data.Nombre), html.EscapeString(data.Ciudad), html.EscapeString(data.Departamento),
		formatCOP(data.ValorUnitario), b.guiaDestino(),
	)
	if !res.IsMock {
		b.recordCustomer(ctx, data, senderOrChat(sender, chatID))
	}
	msg += advertenciaVendedor(hist)
	if res.IsMock {
		msg += "\n\n<i>(Modo simulación: no se creó nada en Skydropx.)</i>"
	}
	b.say(ctx, chatID, msg)
	b.notifyAdmins(ctx, senderOrChat(sender, chatID), data, res.ID, res.IsMock, advertencia, advertenciaVendedor(hist))
}

func senderOrChat(sender string, chatID int64) string {
	if sender == "" {
		return fmt.Sprintf("chat %d", chatID)
	}
	return sender
}

// notifyAdmins avisa la orden nueva: al grupo de gestión con el botón para generar la guía
// y al grupo del vendedor (TELEGRAM_ADMIN_CHAT_IDS) solo como aviso.
// advertencia va arriba del aviso de gestión; vendedor, al final del aviso del grupo del vendedor.
func (b *Bot) notifyAdmins(ctx context.Context, sender string, data domain.ShipmentData, orderID string, isMock bool, advertencia, vendedor string) {
	msg := advertencia + adminOrderText(sender, data)
	msgVendedor := adminOrderText(sender, data) + vendedor
	var buttons [][]telegram.Button
	if isMock {
		msg += "\n\n<i>(Modo simulación: no se creó nada en Skydropx.)</i>"
	} else {
		buttons = botonGenerar(orderID)
	}
	for _, id := range b.gestionChats() {
		if _, err := b.tg.SendMessageWithButtons(ctx, id, msg, buttons); err != nil {
			log.Printf("bot: no se pudo avisar la orden nueva: %v", err)
		}
	}
	// El grupo del vendedor también ve la orden, pero sin botones: la guía solo se gestiona en el grupo de gestión.
	for _, id := range b.opts.AdminChatIDs {
		if b.isGestion(id) {
			continue // ya recibió el aviso con botones
		}
		b.say(ctx, id, msgVendedor+"\n\n📄 La guía llegará aquí cuando esté lista.")
	}
}

// watchGuias revisa periódicamente las órdenes pendientes y avisa cuando ya tienen guía.
func (b *Bot) watchGuias(ctx context.Context) {
	sleep(ctx, 30*time.Second)
	ticker := time.NewTicker(b.opts.CheckEvery)
	defer ticker.Stop()
	for {
		b.checkGuias(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (b *Bot) checkGuias(ctx context.Context) {
	for _, p := range b.st.snapshotPending() {
		if ctx.Err() != nil {
			return
		}
		b.checkPending(ctx, p, nil)
	}
}

// checkPending avisa la guía de una orden pendiente si ya la tiene y la quita de pendientes.
// Si shipmentIDs viene vacío, se consultan los envíos de la orden en Skydropx.
func (b *Bot) checkPending(ctx context.Context, p Pending, shipmentIDs []string) {
	b.checkMu.Lock()
	defer b.checkMu.Unlock()
	if !b.st.isPending(p.OrderID) {
		return // ya se avisó (por el webhook o por la revisión periódica)
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
	if sh, ok := b.guiaDeEnvios(ctx, shipmentIDs); ok {
		// La guía va al grupo (TELEGRAM_ADMIN_CHAT_IDS); si no hay grupo configurado, al chat que pidió la orden.
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

// guiaDeEnvios busca el primer envío de la lista que ya tenga número de guía.
func (b *Bot) guiaDeEnvios(ctx context.Context, ids []string) (shipmentInfo, bool) {
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

// shipmentListFromDetail convierte GET /api/v1/shipments/{id} (un solo "data") en la forma de lista.
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

// ---------- Lectura de envíos de Skydropx ----------

type shipmentInfo struct {
	orderID     string
	tracking    string
	carrier     string
	service     string
	label       string
	trackingURL string
	onDelivery  string // estado del cobro contra entrega: pending | collected | vacío
	nombre      string
	destino     string
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

// message arma la guía con el mismo formato de "Mis envíos": *negrita* de WhatsApp y URLs completas,
// para que el vendedor la copie tal cual al cliente.
func (s shipmentInfo) message() string {
	transportadora := carrierDisplayName(s.carrier)
	if s.service != "" {
		transportadora += " - " + s.service
	}
	seguimiento := s.trackingURL
	if seguimiento == "" {
		seguimiento = carrierTrackingURL[normalizar(s.carrier)]
	}
	if seguimiento == "" {
		seguimiento = "pendiente"
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
		html.EscapeString(s.tracking), html.EscapeString(s.nombre), html.EscapeString(s.destino),
		html.EscapeString(transportadora), cobroTexto(s.onDelivery),
		html.EscapeString(seguimiento), html.EscapeString(pdf),
	)
}

func cobroTexto(onDelivery string) string {
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

// carrierTrackingURL es la página de seguimiento de cada transportadora, si Skydropx no la envía.
var carrierTrackingURL = map[string]string{
	"interrapidisimo": "https://siguetuenvio.interrapidisimo.com/",
	"servientrega":    "https://www.servientrega.com/wps/portal",
	"envia":           "https://envia.co/",
	"coordinadora":    "https://coordinadora.com/",
}

// normalizar deja solo letras sin tilde y números, en minúscula: "Interrapidísimo" -> "interrapidisimo".
func normalizar(name string) string {
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
	if n, ok := carrierNames[normalizar(name)]; ok {
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
			info.nombre = a.Attributes.Name
			info.destino = strings.Trim(a.Attributes.City+", "+a.Attributes.State, ", ")
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
