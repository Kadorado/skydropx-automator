package bot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"sort"
	"strings"
	"time"

	"skydropx-automator/internal/customers"
	"skydropx-automator/internal/domain"
	"skydropx-automator/internal/skydropx"
)

// migrateMaxPages caps how many pages of shipments (20 each) "/clientes migrar" reads from Skydropx.
const migrateMaxPages = 250

// recordCustomer saves the customer of a new order in the customer list (if it's configured).
// A failure is logged but never blocks the order.
func (b *Bot) recordCustomer(ctx context.Context, data domain.ShipmentData, sender string) {
	if b.opts.Customers == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	now := time.Now()
	in := customers.Customer{
		Name: data.Nombre, DocumentID: data.Documento, Phone: data.Telefono, Email: data.Correo,
		Department: data.Departamento, City: data.Ciudad, Neighborhood: data.Barrio, Address: data.Direccion,
		Source: data.Fuente,
	}
	if data.AutorizaDatos == "si" {
		in.Consent = customers.Consent{Given: true, Date: now, Evidence: `Pedido por Telegram con "Autoriza datos: SÍ"`, RecordedBy: sender}
	}
	units := data.Cantidad
	if units < 1 {
		units = 1
	}
	o := customers.Order{Date: now, Units: units, UnitPrice: data.ValorUnitario, CreatedBy: sender}
	if _, err := b.opts.Customers.RecordOrder(ctx, in, o); err != nil {
		log.Printf("bot: couldn't save customer %q: %v", data.Nombre, err)
	}
}

const ayudaClientes = `<code>/clientes</code> → resumen de la base y el archivo para Excel
<code>/clientes migrar</code> → traer una vez los clientes de los envíos anteriores en Skydropx
<code>/autoriza 3001234567 respondió SÍ por WhatsApp</code> → registrar que el cliente autorizó (cédula o teléfono)
<code>/baja 3001234567</code> → el cliente no quiere más mensajes`

// customersCommand handles /clientes and "/clientes migrar".
func (b *Bot) customersCommand(ctx context.Context, chatID int64, text string) {
	if b.opts.Customers == nil {
		b.say(ctx, chatID, "La base de clientes no está configurada: falta <code>MONGODB_URI</code> en Render.")
		return
	}
	if args := strings.Fields(strings.TrimPrefix(text, "/clientes")); len(args) > 0 && strings.EqualFold(args[0], "migrar") {
		b.migrateCustomers(ctx, chatID)
		return
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
		b.say(ctx, chatID, "La base de clientes no está configurada: falta <code>MONGODB_URI</code> en Render.")
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
		b.say(ctx, chatID, "La base de clientes no está configurada: falta <code>MONGODB_URI</code> en Render.")
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
