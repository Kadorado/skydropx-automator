package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"skydropx-automator/internal/skydropx"
)

// diasOficinaLento es desde cuántos días esperando en la oficina un cliente cuenta como que se demoró.
const diasOficinaLento = 3

// historialCliente son los envíos anteriores de un cliente (por cédula o teléfono) según Skydropx.
// No se guarda nada: se consulta cada vez.
type historialCliente struct {
	malos []skydropx.Antecedente // los que no pagó o tardó en recoger en oficina
	total int                    // envíos anteriores encontrados (cobrados, por cobrar o no cobrados)
}

// conProblemas dice si hay que advertir.
func (h historialCliente) conProblemas() bool { return len(h.malos) > 0 }

// revisarCliente consulta en Skydropx el historial de un cliente.
func (b *Bot) revisarCliente(ctx context.Context, documento, telefono string) (historialCliente, error) {
	var h historialCliente
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var err error
	h.malos, h.total, err = b.sky.HistorialCliente(ctx, documento, telefono, diasOficinaLento)
	if errors.Is(err, skydropx.ErrMockMode) {
		err = nil
	}
	return h, err
}

// antecedenteLinea describe un envío anterior que salió mal.
func antecedenteLinea(a skydropx.Antecedente) string {
	fecha := ""
	if t, err := time.Parse(time.RFC3339, a.Date); err == nil {
		fecha = " · " + diaCorto(t.In(colombiaTZ))
	}
	if a.Tipo == "oficina" {
		return fmt.Sprintf("🐢 Tardó %d días en recoger en la oficina · guía %s%s", a.DiasEnOficina, html.EscapeString(a.Tracking), fecha)
	}
	return fmt.Sprintf("❌ No pagó (devolución o sin cobrar) · guía %s%s", html.EscapeString(a.Tracking), fecha)
}

// advertenciaGestion es el bloque que va arriba de la orden en el grupo de gestión ("" si no hay problemas).
func advertenciaGestion(h historialCliente) string {
	if !h.conProblemas() {
		return ""
	}
	var b strings.Builder
	b.WriteString("⚠️ <b>CLIENTE CON HISTORIAL</b>\n")
	for _, a := range h.malos {
		b.WriteString(antecedenteLinea(a) + "\n")
	}
	if h.total > 0 {
		fmt.Fprintf(&b, "📦 De %s anteriores\n", pedidos(h.total))
	}
	b.WriteString("<i>Revisa antes de generar la guía.</i>\n━━━━━━━━━━━━━━━\n\n")
	return b.String()
}

// advertenciaVendedor es la línea corta que ve el vendedor ("" si no hay problemas).
func advertenciaVendedor(h historialCliente) string {
	if !h.conProblemas() {
		return ""
	}
	var motivos []string
	var noPago, oficina int
	for _, a := range h.malos {
		if a.Tipo == "oficina" {
			oficina++
		} else {
			noPago++
		}
	}
	if noPago > 0 {
		motivos = append(motivos, fmt.Sprintf("%d sin pagar", noPago))
	}
	if oficina > 0 {
		motivos = append(motivos, fmt.Sprintf("%d demora en oficina", oficina))
	}
	return "\n\n⚠️ <b>Este cliente tiene historial</b> (" + strings.Join(motivos, ", ") +
		"). Se revisará antes de generar la guía; si puedes, confírmale el pedido o pídele que pague antes."
}

// comandoCliente atiende /cliente <cédula o teléfono>: el historial completo de esa persona.
func (b *Bot) comandoCliente(ctx context.Context, chatID int64, text string) {
	f := strings.Fields(strings.TrimPrefix(text, "/cliente"))
	if len(f) != 1 || (skydropx.ClaveDocumento(f[0]) == "" && skydropx.ClaveTelefono(f[0]) == "") {
		b.say(ctx, chatID, "Escribe la cédula o el teléfono. Ejemplo: <code>/cliente 1234567890</code>")
		return
	}
	h, err := b.revisarCliente(ctx, f[0], f[0])
	if err != nil {
		b.say(ctx, chatID, "No se pudo consultar Skydropx: "+html.EscapeString(err.Error()))
		return
	}
	b.say(ctx, chatID, clienteTexto(f[0], h))
}

// clienteTexto arma la respuesta de /cliente.
func clienteTexto(id string, h historialCliente) string {
	var b strings.Builder
	fmt.Fprintf(&b, "👤 <b>CLIENTE %s</b>\n", html.EscapeString(id))
	if h.total == 0 {
		b.WriteString("\nNo tiene envíos anteriores en la cuenta.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\n📦 %s anteriores\n", pedidos(h.total))
	if len(h.malos) == 0 {
		b.WriteString("✅ Todos pagaron sin problema (o siguen en camino).\n")
	} else {
		for _, a := range h.malos {
			b.WriteString(antecedenteLinea(a) + "\n")
		}
	}
	if h.conProblemas() {
		b.WriteString("\n⚠️ Sus pedidos llegan con advertencia.\n")
	}
	return b.String()
}
