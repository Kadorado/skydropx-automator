package bot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"regexp"
	"sort"
	"strings"
	"time"

	"skydropx-automator/internal/customers"
	"skydropx-automator/internal/domain"
	"skydropx-automator/internal/skydropx"
	"skydropx-automator/internal/telegram"
)

// migrateMaxPages caps how many pages of shipments (20 each) "/clientes migrar" reads from Skydropx.
const migrateMaxPages = 250

// UnsavedCustomer is the customer of an order that couldn't be saved in the customer list.
type UnsavedCustomer struct {
	OrderID string              `json:"order_id"`
	Data    domain.ShipmentData `json:"data"`
	Sender  string              `json:"sender"`
	At      time.Time           `json:"at"` // when the order was created: the date the order keeps
}

func (s *State) addUnsaved(u UnsavedCustomer) error {
	s.mu.Lock()
	s.UnsavedCustomers = append(s.UnsavedCustomers, u)
	s.mu.Unlock()
	return s.save()
}

func (s *State) getUnsaved(orderID string) (UnsavedCustomer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.UnsavedCustomers {
		if u.OrderID == orderID {
			return u, true
		}
	}
	return UnsavedCustomer{}, false
}

func (s *State) removeUnsaved(orderID string) error {
	s.mu.Lock()
	kept := s.UnsavedCustomers[:0]
	for _, u := range s.UnsavedCustomers {
		if u.OrderID != orderID {
			kept = append(kept, u)
		}
	}
	s.UnsavedCustomers = kept
	s.mu.Unlock()
	return s.save()
}

func (s *State) snapshotUnsaved() []UnsavedCustomer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]UnsavedCustomer(nil), s.UnsavedCustomers...)
}

// errNotConnected means MONGODB_URI is set but MongoDB didn't connect when the bot started.
var errNotConnected = errors.New("la base de clientes no se conectó al arrancar el bot")

// saveCustomer writes the customer of an order in the customer list.
func (b *Bot) saveCustomer(ctx context.Context, u UnsavedCustomer) error {
	if b.opts.Customers == nil {
		return errNotConnected
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	data := u.Data
	in := customers.Customer{
		Name: data.Nombre, DocumentID: data.Documento, Phone: data.Telefono, Email: data.Correo,
		Department: data.Departamento, City: data.Ciudad, Neighborhood: data.Barrio, Address: data.Direccion,
		Source: data.Fuente,
	}
	if data.AutorizaDatos == "si" {
		in.Consent = customers.Consent{Given: true, Date: u.At, Evidence: `Pedido por Telegram con "Autoriza datos: SÍ"`, RecordedBy: u.Sender}
	}
	units := data.Cantidad
	if units < 1 {
		units = 1
	}
	o := customers.Order{Date: u.At, Units: units, UnitPrice: data.ValorUnitario, CreatedBy: u.Sender}
	_, err := b.opts.Customers.RecordOrder(ctx, in, o)
	return err
}

// recordCustomer saves the customer of a new order. Success is silent; on failure the customer is kept
// on disk and the gestión group is told what happened, with a button to retry. The order never waits on it.
func (b *Bot) recordCustomer(ctx context.Context, data domain.ShipmentData, orderID, sender string) {
	if b.opts.Customers == nil && b.opts.CustomersError == "" {
		return // the customer list isn't configured
	}
	u := UnsavedCustomer{OrderID: orderID, Data: data, Sender: sender, At: time.Now()}
	err := b.saveCustomer(ctx, u)
	if err == nil {
		return
	}
	log.Printf("bot: couldn't save customer %q: %v", data.Nombre, err)
	if err := b.st.addUnsaved(u); err != nil {
		log.Printf("bot: couldn't keep the unsaved customer %q: %v", data.Nombre, err)
	}
	for _, id := range b.gestionChats() {
		if _, err := b.tg.SendMessageWithButtons(ctx, id, unsavedText(u, b.failureReason(err)), retryCustomerButton(orderID)); err != nil {
			log.Printf("bot: couldn't send the unsaved customer notice: %v", err)
		}
	}
}

func retryCustomerButton(orderID string) [][]telegram.Button {
	return [][]telegram.Button{telegram.Row(telegram.Button{Text: "🔄 Reintentar", Data: actGuardarCliente + ":" + orderID})}
}

// failureReason explains a failure in plain words, without connection strings.
func (b *Bot) failureReason(err error) string {
	if errors.Is(err, errNotConnected) {
		return "la base de clientes no se conectó cuando arrancó el bot (" + sanitizeReason(b.opts.CustomersError) + ")"
	}
	msg := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(msg, "server selection"):
		return "MongoDB no respondió a tiempo"
	default:
		return sanitizeReason(msg)
	}
}

var mongoURIPattern = regexp.MustCompile(`mongodb(\+srv)?://\S+`)

// sanitizeReason hides any connection string and keeps the reason short.
func sanitizeReason(s string) string {
	s = mongoURIPattern.ReplaceAllString(s, "<uri>")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// unsavedText is the notice of a customer that couldn't be saved.
func unsavedText(u UnsavedCustomer, reason string) string {
	return fmt.Sprintf(`⚠️ <b>No se guardó el cliente en la base</b>
👤 %s · 📞 %s
❓ Motivo: %s

El pedido sigue normal; solo falta guardar al cliente. El bot lo tiene apartado, no se pierde.

<b>Qué hacer</b>
1. Toca <b>🔄 Reintentar</b> en unos minutos.
2. Si sigue fallando: en Atlas revisa que el cluster esté activo y que Network Access tenga <code>0.0.0.0/0</code>; luego reinicia el servicio en Render (Manual Deploy). Al arrancar conectado, el bot guarda solo los clientes apartados.`,
		html.EscapeString(u.Data.Nombre), html.EscapeString(u.Data.Telefono), html.EscapeString(reason))
}

// retryCustomer handles the 🔄 Reintentar button of an unsaved customer.
func (b *Bot) retryCustomer(ctx context.Context, callbackID string, chatID, msgID int64, orderID string) {
	u, ok := b.st.getUnsaved(orderID)
	if !ok {
		_ = b.tg.AnswerCallback(ctx, callbackID, "Este cliente ya está guardado.")
		_ = b.tg.EditMessage(ctx, chatID, msgID, "✅ Cliente guardado en la base.", nil)
		return
	}
	if err := b.saveCustomer(ctx, u); err != nil {
		reason := b.failureReason(err)
		log.Printf("bot: retry couldn't save customer %q: %v", u.Data.Nombre, err)
		_ = b.tg.AnswerCallback(ctx, callbackID, "Sigue fallando.")
		_ = b.tg.EditMessage(ctx, chatID, msgID, unsavedText(u, reason)+"\n\n<i>Último intento: "+fechaHora(time.Now())+"</i>", retryCustomerButton(orderID))
		return
	}
	if err := b.st.removeUnsaved(orderID); err != nil {
		log.Printf("bot: couldn't forget the saved customer %q: %v", u.Data.Nombre, err)
	}
	_ = b.tg.AnswerCallback(ctx, callbackID, "Guardado.")
	_ = b.tg.EditMessage(ctx, chatID, msgID, "✅ <b>"+html.EscapeString(u.Data.Nombre)+"</b> quedó guardado en la base.", nil)
}

// customersAtStartup warns the gestión group if MongoDB didn't connect, and otherwise saves the customers
// left aside while it was failing.
func (b *Bot) customersAtStartup(ctx context.Context) {
	if b.opts.Customers == nil {
		if b.opts.CustomersError != "" {
			b.sayGestion(ctx, "⚠️ <b>La base de clientes no se conectó</b>\n❓ Motivo: "+html.EscapeString(sanitizeReason(b.opts.CustomersError))+
				"\n\nLos pedidos funcionan normal, pero sus clientes quedan apartados hasta que la base conecte.\n\n<b>Qué hacer</b>\nEn Atlas revisa que el cluster esté activo y que Network Access tenga <code>0.0.0.0/0</code>; en Render revisa que <code>MONGODB_URI</code> esté bien copiada y reinicia el servicio (Manual Deploy).")
		}
		return
	}
	pending := b.st.snapshotUnsaved()
	if len(pending) == 0 {
		return
	}
	saved, failed := 0, 0
	for _, u := range pending {
		if err := b.saveCustomer(ctx, u); err != nil {
			log.Printf("bot: startup couldn't save customer %q: %v", u.Data.Nombre, err)
			failed++
			continue
		}
		if err := b.st.removeUnsaved(u.OrderID); err != nil {
			log.Printf("bot: couldn't forget the saved customer %q: %v", u.Data.Nombre, err)
		}
		saved++
	}
	msg := fmt.Sprintf("✅ Se guardaron en la base <b>%d</b> clientes que habían quedado apartados.", saved)
	if failed > 0 {
		msg += fmt.Sprintf("\n⚠️ %d siguen sin guardarse: usa el botón 🔄 Reintentar de su aviso.", failed)
	}
	b.sayGestion(ctx, msg)
}

// notConnectedText explains why the customer commands don't work.
func (b *Bot) notConnectedText() string {
	if b.opts.CustomersError != "" {
		return "⚠️ La base de clientes no se conectó al arrancar el bot.\n❓ Motivo: " + html.EscapeString(sanitizeReason(b.opts.CustomersError)) +
			"\n\nRevisa Network Access en Atlas y reinicia el servicio en Render (Manual Deploy)."
	}
	return "La base de clientes no está configurada: falta <code>MONGODB_URI</code> en Render."
}

func (b *Bot) sayGestion(ctx context.Context, msg string) {
	for _, id := range b.gestionChats() {
		b.say(ctx, id, msg)
	}
}

const ayudaClientes = `<code>/clientes</code> → resumen de la base y el archivo para Excel
<code>/clientes migrar</code> → traer una vez los clientes de los envíos anteriores en Skydropx
<code>/clientes respaldo</code> → copia completa de la base (también llega sola cada lunes)
<code>/autoriza 3001234567 respondió SÍ por WhatsApp</code> → registrar que el cliente autorizó (cédula o teléfono)
<code>/baja 3001234567</code> → el cliente no quiere más mensajes`

// customersCommand handles /clientes and "/clientes migrar".
func (b *Bot) customersCommand(ctx context.Context, chatID int64, text string) {
	if b.opts.Customers == nil {
		b.say(ctx, chatID, b.notConnectedText())
		return
	}
	if args := strings.Fields(strings.TrimPrefix(text, "/clientes")); len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "migrar":
			b.migrateCustomers(ctx, chatID)
			return
		case "respaldo":
			if err := b.sendBackup(ctx, chatID); err != nil {
				b.say(ctx, chatID, "⚠️ No se pudo hacer el respaldo: "+html.EscapeString(sanitizeReason(err.Error())))
			}
			return
		case "restaurar": // without a file: restoreCommand handles the ones with a file
			b.say(ctx, chatID, restoreHowTo)
			return
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	all, err := b.opts.Customers.All(ctx)
	if err != nil {
		b.say(ctx, chatID, "No se pudo leer la base de clientes: "+html.EscapeString(err.Error()))
		return
	}
	b.say(ctx, chatID, customersSummaryText(customers.Summary(all)))
	if len(all) == 0 {
		return
	}
	var buf bytes.Buffer
	if err := customers.WriteCSV(&buf, all); err != nil {
		b.say(ctx, chatID, "No se pudo armar el archivo: "+html.EscapeString(err.Error()))
		return
	}
	name := "clientes-" + time.Now().In(colombiaTZ).Format("2006-01-02") + ".csv"
	if err := b.tg.SendDocument(ctx, chatID, name, buf.Bytes(), "📎 Base de clientes (se abre en Excel)"); err != nil {
		log.Printf("bot: couldn't send the customer file: %v", err)
	}
}

// customersSummaryText is the /clientes answer.
func customersSummaryText(st customers.Stats) string {
	var b strings.Builder
	b.WriteString("👥 <b>BASE DE CLIENTES</b>\n\n")
	if st.Customers == 0 {
		b.WriteString("Todavía no hay clientes. Se guardan solos con cada pedido nuevo, y con <code>/clientes migrar</code> traes los de los envíos anteriores en Skydropx.\n\n")
		b.WriteString(ayudaClientes)
		return b.String()
	}
	fmt.Fprintf(&b, "👤 Clientes: <b>%d</b> · 📦 Pedidos: %d\n", st.Customers, st.Orders)
	fmt.Fprintf(&b, "✅ Autorizaron publicidad: <b>%d</b>\n", st.Contactable)
	fmt.Fprintf(&b, "⛔ Sin autorización: %d\n", st.Customers-st.Contactable-st.OptedOut)
	if st.OptedOut > 0 {
		fmt.Fprintf(&b, "🚫 Pidieron no recibir mensajes: %d\n", st.OptedOut)
	}
	fmt.Fprintf(&b, "🔁 Compraron más de una vez: %d\n", st.Repeat)

	b.WriteString("\n📣 <b>De dónde llegaron</b>\n")
	for _, s := range topCounts(st.BySource, 8) {
		name := s.name
		if name == "" {
			name = "Sin indicar"
		}
		fmt.Fprintf(&b, "• %s: %d\n", html.EscapeString(name), s.n)
	}
	b.WriteString("\n📍 <b>Ciudades con más clientes</b>\n")
	for _, c := range topCounts(st.ByCity, 8) {
		fmt.Fprintf(&b, "• %s: %d\n", html.EscapeString(c.name), c.n)
	}
	b.WriteString("\n<i>Solo a los que autorizaron se les puede enviar publicidad.</i>")
	return b.String()
}

type count struct {
	name string
	n    int
}

// topCounts returns the max entries with the highest counts (ties in alphabetical order).
func topCounts(m map[string]int, max int) []count {
	var out []count
	for name, n := range m {
		out = append(out, count{name, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].name < out[j].name
	})
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// migrateCustomers brings the customers of the shipments made in Skydropx before the list started.
func (b *Bot) migrateCustomers(ctx context.Context, chatID int64) {
	since := b.opts.Customers.Since().In(colombiaTZ)
	b.say(ctx, chatID, fmt.Sprintf("⏳ Trayendo los clientes de los envíos en Skydropx anteriores al %s. Puede tardar unos minutos…",
		fechaHora(since)))
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	recipients, truncated, err := b.sky.Recipients(ctx, migrateMaxPages)
	if errors.Is(err, skydropx.ErrMockMode) {
		b.say(ctx, chatID, "El bot no está conectado a Skydropx (modo de prueba).")
		return
	}
	if err != nil {
		b.say(ctx, chatID, "No se pudieron traer los envíos de Skydropx: "+html.EscapeString(err.Error()))
		return
	}
	history := make([]customers.Sighting, 0, len(recipients))
	for _, r := range recipients {
		date, err := time.Parse(time.RFC3339, r.Date)
		if err != nil {
			continue
		}
		history = append(history, customers.Sighting{
			Customer: customers.Customer{
				Name: r.Name, DocumentID: r.DocumentID, Phone: r.Phone, Email: r.Email,
				Department: r.Department, City: r.City, Neighborhood: r.Neighborhood, Address: r.Address,
			},
			Order: customers.Order{Date: date},
		})
	}
	res, err := b.opts.Customers.Migrate(ctx, history)
	if err != nil {
		b.say(ctx, chatID, "La migración se cortó (puedes repetirla, no duplica): "+html.EscapeString(err.Error()))
		return
	}
	b.say(ctx, chatID, migrationText(res, since, truncated))
}

// migrationText is the answer to "/clientes migrar".
func migrationText(r customers.MigrationResult, since time.Time, truncated bool) string {
	var b strings.Builder
	b.WriteString("✅ <b>CLIENTES MIGRADOS DESDE SKYDROPX</b>\n\n")
	fmt.Fprintf(&b, "📦 Envíos leídos: %d\n", r.Shipments)
	fmt.Fprintf(&b, "🆕 Clientes nuevos: <b>%d</b>\n", r.New)
	fmt.Fprintf(&b, "🧾 Pedidos agregados: %d\n", r.OrdersAdded)
	if r.Repeated > 0 {
		fmt.Fprintf(&b, "♻️ Ya estaban migrados: %d\n", r.Repeated)
	}
	if r.AfterSince > 0 {
		fmt.Fprintf(&b, "🤖 Del %s en adelante (ya los guardó el bot): %d\n", fechaHora(since), r.AfterSince)
	}
	if r.NoKeys > 0 {
		fmt.Fprintf(&b, "⚠️ Sin cédula ni teléfono (no se guardaron): %d\n", r.NoKeys)
	}
	if truncated {
		fmt.Fprintf(&b, "\n<i>Había más de %d envíos: los más antiguos no se trajeron.</i>\n", migrateMaxPages*20)
	}
	b.WriteString("\n<i>Quedan con fuente «skydropx» y sin autorización para publicidad: regístrala con /autoriza cuando el cliente acepte. Si la repites, no duplica nada.</i>")
	return b.String()
}

// fechaHora formats a moment in Colombia, e.g. "lun 16 nov 6:45 p. m.".
func fechaHora(t time.Time) string {
	t = t.In(colombiaTZ)
	ampm := "a. m."
	if t.Hour() >= 12 {
		ampm = "p. m."
	}
	h := t.Hour() % 12
	if h == 0 {
		h = 12
	}
	return fmt.Sprintf("%s %d:%02d %s", fechaCorta(t.Format("2006-01-02")), h, t.Minute(), ampm)
}

// consentCommand handles /autoriza <document or phone> [how]: the customer authorized the use of their data.
func (b *Bot) consentCommand(ctx context.Context, chatID int64, sender, text string) {
	if b.opts.Customers == nil {
		b.say(ctx, chatID, b.notConnectedText())
		return
	}
	f := strings.Fields(strings.TrimPrefix(text, "/autoriza"))
	if len(f) == 0 {
		b.say(ctx, chatID, "Escribe la cédula o el teléfono del cliente y cómo autorizó. Ejemplo:\n<code>/autoriza 3001234567 respondió SÍ por WhatsApp</code>")
		return
	}
	evidence := strings.Join(f[1:], " ")
	if evidence == "" {
		evidence = "Registrado con /autoriza"
	}
	c, err := b.opts.Customers.GrantConsent(ctx, f[0], evidence, senderOrChat(sender, chatID))
	if errors.Is(err, customers.ErrNotFound) {
		b.say(ctx, chatID, "No encontré ese cliente en la base. Revisa la cédula o el teléfono.")
		return
	}
	if err != nil {
		b.say(ctx, chatID, "No se pudo guardar: "+html.EscapeString(err.Error()))
		return
	}
	b.say(ctx, chatID, fmt.Sprintf("✅ <b>%s</b> autorizó el uso de sus datos para publicidad.\n📝 %s", html.EscapeString(c.Name), html.EscapeString(evidence)))
}

// optOutCommand handles /baja <document or phone>: the customer doesn't want more messages.
func (b *Bot) optOutCommand(ctx context.Context, chatID int64, text string) {
	if b.opts.Customers == nil {
		b.say(ctx, chatID, b.notConnectedText())
		return
	}
	f := strings.Fields(strings.TrimPrefix(text, "/baja"))
	if len(f) != 1 {
		b.say(ctx, chatID, "Escribe la cédula o el teléfono del cliente. Ejemplo: <code>/baja 3001234567</code>")
		return
	}
	c, err := b.opts.Customers.OptOut(ctx, f[0])
	if errors.Is(err, customers.ErrNotFound) {
		b.say(ctx, chatID, "No encontré ese cliente en la base. Revisa la cédula o el teléfono.")
		return
	}
	if err != nil {
		b.say(ctx, chatID, "No se pudo guardar: "+html.EscapeString(err.Error()))
		return
	}
	b.say(ctx, chatID, fmt.Sprintf("🚫 <b>%s</b> quedó dado de baja: no se le debe enviar más publicidad. Queda constancia de la fecha.", html.EscapeString(c.Name)))
}

// backupHour is when the weekly backup of the customer list is sent, on Mondays (Colombia time).
const backupHour = 7

func (s *State) backupDue(day string) (bool, error) {
	s.mu.Lock()
	if s.LastCustomerBackup == day {
		s.mu.Unlock()
		return false, nil
	}
	s.LastCustomerBackup = day
	s.mu.Unlock()
	return true, s.save()
}

// backupIfDue sends the weekly backup to the gestión group on Monday mornings. A failure is reported:
// a backup that silently stops is worse than none.
func (b *Bot) backupIfDue(ctx context.Context, now time.Time) {
	if b.opts.Customers == nil || now.Weekday() != time.Monday || now.Hour() < backupHour {
		return
	}
	if ok, _ := b.st.backupDue(now.Format("2006-01-02")); !ok {
		return
	}
	for _, id := range b.gestionChats() {
		if err := b.sendBackup(ctx, id); err != nil {
			log.Printf("bot: weekly customer backup failed: %v", err)
			b.say(ctx, id, "⚠️ <b>No se pudo hacer el respaldo semanal de clientes</b>\n❓ Motivo: "+html.EscapeString(sanitizeReason(err.Error()))+
				"\n\nHazlo a mano con <code>/clientes respaldo</code> cuando MongoDB responda.")
		}
	}
}

// sendBackup sends a full copy of the customer list as a JSON file.
func (b *Bot) sendBackup(ctx context.Context, chatID int64) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	backup, err := b.opts.Customers.Backup(ctx)
	if err != nil {
		return err
	}
	orders := 0
	for _, c := range backup.Customers {
		orders += len(c.Orders)
	}
	var buf bytes.Buffer
	if err := customers.WriteBackup(&buf, backup); err != nil {
		return err
	}
	name := "clientes-respaldo-" + time.Now().In(colombiaTZ).Format("2006-01-02") + ".json"
	caption := fmt.Sprintf("🗄️ <b>Respaldo de la base de clientes</b>\n👤 %d clientes · 📦 %d pedidos\n\n"+
		"Guárdalo. Si algún día se pierden clientes, responde a este archivo con <code>/clientes restaurar</code>.", len(backup.Customers), orders)
	return b.tg.SendDocument(ctx, chatID, name, buf.Bytes(), caption)
}

const restoreHowTo = `Para restaurar, <b>responde al archivo del respaldo</b> (el <code>clientes-respaldo-….json</code>) con <code>/clientes restaurar</code>, o envía el archivo con ese texto.

Solo recupera los clientes que falten: nunca borra ni cambia los que ya están.`

// isRestoreCommand reports a "/clientes restaurar" sent with a file or replying to one.
func isRestoreCommand(m *telegram.Message) bool {
	text := strings.TrimSpace(m.Text)
	if m.Document != nil {
		text = strings.TrimSpace(m.Caption)
	}
	f := strings.Fields(strings.ToLower(text))
	if len(f) < 2 || (f[0] != "/clientes" && !strings.HasPrefix(f[0], "/clientes@")) || f[1] != "restaurar" {
		return false
	}
	return m.Document != nil || (m.ReplyTo != nil && m.ReplyTo.Document != nil)
}

// restoreCommand puts back the customers of a backup file that are missing from the list.
func (b *Bot) restoreCommand(ctx context.Context, m *telegram.Message) {
	chatID := m.Chat.ID
	if !b.isGestion(chatID) {
		b.say(ctx, chatID, "Este comando solo funciona en el grupo de gestión.")
		return
	}
	if b.opts.Customers == nil {
		b.say(ctx, chatID, b.notConnectedText())
		return
	}
	doc := m.Document
	if doc == nil {
		doc = m.ReplyTo.Document
	}
	if doc.FileSize > telegram.MaxDownloadSize {
		b.say(ctx, chatID, "⚠️ El archivo es muy grande para que el bot lo lea (máximo 20 MB).")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	data, err := b.tg.DownloadFile(ctx, doc.FileID)
	if err != nil {
		b.say(ctx, chatID, "⚠️ No pude descargar el archivo de Telegram: "+html.EscapeString(err.Error()))
		return
	}
	backup, err := customers.ReadBackup(bytes.NewReader(data))
	if err != nil {
		b.say(ctx, chatID, "⚠️ Ese archivo no es un respaldo de clientes. Usa el <code>clientes-respaldo-….json</code> que manda el bot (el Excel no sirve para restaurar).")
		return
	}
	res, err := b.opts.Customers.Restore(ctx, backup)
	if err != nil {
		b.say(ctx, chatID, "⚠️ La restauración se cortó (puedes repetirla, no duplica): "+html.EscapeString(sanitizeReason(err.Error())))
		return
	}
	b.say(ctx, chatID, restoreText(res, backup.CreatedAt))
}

// restoreText is the answer to "/clientes restaurar".
func restoreText(r customers.RestoreResult, createdAt time.Time) string {
	var b strings.Builder
	b.WriteString("✅ <b>RESTAURACIÓN TERMINADA</b>\n\n")
	fmt.Fprintf(&b, "🗄️ Respaldo del %s: %d clientes\n", fechaHora(createdAt), r.InBackup)
	fmt.Fprintf(&b, "♻️ Recuperados: <b>%d</b>\n", r.Restored)
	fmt.Fprintf(&b, "✔️ Ya estaban (no se tocaron): %d\n", r.AlreadyThere)
	if r.Conflicts > 0 {
		fmt.Fprintf(&b, "⚠️ No se recuperaron porque su cédula ya la tiene otro cliente: %d\n", r.Conflicts)
	}
	b.WriteString("\n<i>La restauración nunca borra ni cambia clientes que ya existen.</i>")
	return b.String()
}
