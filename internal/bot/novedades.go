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

// diasNovedades es hasta cuántos días atrás se revisan los envíos abiertos con /novedades.
const diasNovedades = 45

// maxMensaje deja margen bajo el límite de 4096 caracteres de un mensaje de Telegram.
const maxMensaje = 3800

// grupoNovedad es cada bloque del informe de /novedades, en orden de urgencia.
var gruposNovedad = []struct {
	kind, titulo, accion string
}{
	{"devolucion", "❌ <b>En devolución o sin cobrar</b>", "Llama al cliente: a veces se puede reprogramar antes de que llegue de vuelta."},
	{"intento", "⚠️ <b>No pudieron entregar</b>", "Llama al cliente para que esté pendiente del reintento."},
	{"oficina", "🏢 <b>Para reclamar en oficina</b>", "Mira el historial: si ya llegó a la oficina de destino, avísale al cliente que lo recoja y pague ahí."},
	{"novedad", "🚨 <b>Con novedad</b>", "Revisa con el cliente o con la transportadora."},
}

// comandoNovedades atiende /novedades: revisa los envíos abiertos y manda los que hay que gestionar.
func (b *Bot) comandoNovedades(ctx context.Context, chatID int64) {
	b.say(ctx, chatID, "⏳ Revisando el rastreo de los envíos abiertos… puede tardar un minuto.")
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	since := time.Now().In(colombiaTZ).AddDate(0, 0, -diasNovedades)
	problemas, revisados, err := b.sky.Problemas(ctx, since)
	if err != nil {
		if errors.Is(err, skydropx.ErrMockMode) {
			b.say(ctx, chatID, "Modo simulación: no hay envíos reales que revisar.")
			return
		}
		if len(problemas) == 0 {
			b.say(ctx, chatID, "No se pudo revisar el rastreo: "+err.Error())
			return
		}
	}
	for _, msg := range novedadesMensajes(problemas, revisados) {
		b.say(ctx, chatID, msg)
	}
}

// novedadesMensajes arma el informe agrupado, partido en mensajes que caben en Telegram.
func novedadesMensajes(problemas []skydropx.Problema, revisados int) []string {
	if len(problemas) == 0 {
		return []string{fmt.Sprintf("✅ <b>Sin novedades.</b> Revisé %d envíos abiertos de los últimos %d días y ninguno tiene problemas.", revisados, diasNovedades)}
	}
	msgs := []string{fmt.Sprintf("📋 <b>Novedades para gestionar:</b> %d de %d envíos abiertos (últimos %d días)", len(problemas), revisados, diasNovedades)}
	for _, g := range gruposNovedad {
		var items []string
		for _, p := range problemas {
			if p.Kind == g.kind {
				items = append(items, novedadItem(p))
			}
		}
		if len(items) == 0 {
			continue
		}
		actual := fmt.Sprintf("%s (%d)\n<i>%s</i>", g.titulo, len(items), g.accion)
		for _, it := range items {
			if len(actual)+len(it)+2 > maxMensaje {
				msgs = append(msgs, actual)
				actual = g.titulo + " (continuación)"
			}
			actual += "\n\n" + it
		}
		msgs = append(msgs, actual)
	}
	return msgs
}

func novedadItem(p skydropx.Problema) string {
	cliente := p.Customer
	if cliente == "" {
		cliente = "Sin nombre"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "👤 <b>%s</b>", html.EscapeString(cliente))
	if p.Phone != "" {
		fmt.Fprintf(&b, " · 📞 %s", html.EscapeString(p.Phone))
	}
	if p.City != "" {
		fmt.Fprintf(&b, " · 📍 %s", html.EscapeString(p.City))
	}
	fmt.Fprintf(&b, "\n📦 %s", html.EscapeString(p.Tracking))
	if len(p.History) > 0 {
		b.WriteString("\n🕒 <b>Últimos eventos:</b>")
		for _, ev := range p.History {
			desc := strings.TrimSpace(ev.Description)
			if desc == "" {
				desc = ev.Status
			}
			fmt.Fprintf(&b, "\n   • %s — %s", diaHora(ev.Date.In(colombiaTZ)), html.EscapeString(desc))
		}
	} else if ev := strings.TrimSpace(p.LastEvent); ev != "" {
		fmt.Fprintf(&b, "\n📝 %s", html.EscapeString(ev))
		if t, err := time.Parse(time.RFC3339Nano, p.LastDate); err == nil {
			fmt.Fprintf(&b, " (%s)", diaCorto(t.In(colombiaTZ)))
		}
	}
	return b.String()
}

func diaHora(t time.Time) string {
	ampm := "a. m."
	if t.Hour() >= 12 {
		ampm = "p. m."
	}
	return fmt.Sprintf("%s %s %s", diaCorto(t), t.Format("3:04"), ampm)
}
