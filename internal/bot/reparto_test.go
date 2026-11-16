package bot

import (
	"strings"
	"testing"
	"time"

	"skydropx-automator/internal/skydropx"
)

func TestWaLink(t *testing.T) {
	if got := waLink("300 123 4567", "Hola Ana"); got != "https://wa.me/573001234567?text=Hola+Ana" {
		t.Errorf("celular: %s", got)
	}
	if got := waLink("+57 300-123-4567", "x"); !strings.HasPrefix(got, "https://wa.me/573001234567?") {
		t.Errorf("con indicativo: %s", got)
	}
	if got := waLink("12345", "x"); got != "" {
		t.Errorf("teléfono inválido: %s", got)
	}
}

func TestEnRutaTexto(t *testing.T) {
	e := skydropx.CODShipment{Tracking: "240012345678", Customer: "Ana Pérez", Phone: "3001234567", City: "Montería"}
	msg := enRutaTexto(e, 74800, "En ruta de entrega - Monteria")
	for _, want := range []string{"En ruta de entrega: llega hoy", "Ana Pérez", "Cobra al entregar:</b> $74.800",
		`<a href="https://wa.me/573001234567?text=Hola+Ana`, "Avisarle al cliente por WhatsApp"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
	if m := mensajeEnRuta(e, 74800); !strings.Contains(m, "Hola Ana 👋") || !strings.Contains(m, "guía 240012345678") || !strings.Contains(m, "$74.800") {
		t.Errorf("mensaje al cliente: %s", m)
	}
	if msg := enRutaTexto(skydropx.CODShipment{Tracking: "X"}, 0, ""); strings.Contains(msg, "wa.me") || !strings.Contains(msg, "Sin nombre") {
		t.Errorf("sin datos:\n%s", msg)
	}
}

func TestGuiaTexto(t *testing.T) {
	e := skydropx.CODShipment{Tracking: "240012345678", Carrier: "interrapidisimo", Customer: "Ana Pérez", Phone: "3001234567", Status: "pending"}
	st := skydropx.Status{Raw: "last_mile", History: []skydropx.TrackEvent{
		{Date: time.Date(2026, 11, 16, 8, 5, 0, 0, colombiaTZ), Description: "EN RUTA DE ENTREGA"},
	}}
	msg := guiaTexto(e, st, 74800)
	for _, want := range []string{"Guía 240012345678</b> · interrapidisimo", "En ruta de entrega: llega hoy",
		"por cobrar $74.800", "16 nov 8:05 a. m. — EN RUTA DE ENTREGA", "wa.me/573001234567"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
	e.Status = "collected"
	if msg := guiaTexto(e, skydropx.Status{Raw: "delivered"}, 0); !strings.Contains(msg, "✅ Entregado") || !strings.Contains(msg, "ya pagó") || strings.Contains(msg, "wa.me") {
		t.Errorf("entregado:\n%s", msg)
	}
}

func TestChatsEnRuta(t *testing.T) {
	b := &Bot{opts: Options{GestionChatIDs: []int64{1}, AdminChatIDs: []int64{2, 1}}}
	if got := b.chatsEnRuta(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("con gestión: %v", got)
	}
	b = &Bot{opts: Options{AdminChatIDs: []int64{2}}}
	if got := b.chatsEnRuta(); len(got) != 1 || got[0] != 2 {
		t.Errorf("sin gestión: %v", got)
	}
}
