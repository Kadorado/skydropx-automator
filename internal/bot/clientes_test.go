package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"skydropx-automator/internal/domain"
	"skydropx-automator/internal/skydropx"
)

func historialDePrueba() historialCliente {
	return historialCliente{total: 4, malos: []skydropx.Antecedente{
		{CODShipment: skydropx.CODShipment{Tracking: "G1", Date: "2026-11-03T10:00:00-05:00"}, Tipo: "no_pago"},
		{CODShipment: skydropx.CODShipment{Tracking: "G2", Date: "2026-10-20T10:00:00-05:00"}, Tipo: "oficina", DiasEnOficina: 6},
	}}
}

func TestAdvertencias(t *testing.T) {
	h := historialDePrueba()
	g := advertenciaGestion(h)
	for _, want := range []string{"CLIENTE CON HISTORIAL", "No pagó (devolución o sin cobrar) · guía G1 · 3 nov",
		"Tardó 6 días en recoger en la oficina · guía G2 · 20 oct", "De 4 pedidos anteriores", "Revisa antes de generar la guía"} {
		if !strings.Contains(g, want) {
			t.Errorf("falta %q en:\n%s", want, g)
		}
	}
	if v := advertenciaVendedor(h); !strings.Contains(v, "Este cliente tiene historial</b> (1 sin pagar, 1 demora en oficina)") {
		t.Errorf("vendedor: %s", v)
	}
	if advertenciaGestion(historialCliente{total: 3}) != "" || advertenciaVendedor(historialCliente{}) != "" {
		t.Error("sin antecedentes no debe haber advertencia")
	}
	if m := clienteTexto("1234567890", historialCliente{total: 3}); !strings.Contains(m, "3 pedidos anteriores") || !strings.Contains(m, "Todos pagaron") {
		t.Errorf("cliente bueno:\n%s", m)
	}
	if m := clienteTexto("1234567890", h); !strings.Contains(m, "llegan con advertencia") {
		t.Errorf("cliente con historial:\n%s", m)
	}
}

func TestGenerarConHistorialPideConfirmacion(t *testing.T) {
	b, f := newTestBot(t)
	ctx := context.Background()
	data := domain.ShipmentData{Nombre: "Cliente", Ciudad: "Medellín", Departamento: "Antioquia", CodigoPostal: "050001", ValorUnitario: 60000}
	_ = b.st.add(Pending{OrderID: "orden-h", Data: &data, CreatedAt: time.Now(), Advertencia: advertenciaGestion(historialDePrueba())})

	b.handleCallback(ctx, callback("g:orden-h"))
	last := f.edits[len(f.edits)-1]
	if !strings.HasPrefix(last, "⚠️ <b>CLIENTE CON HISTORIAL</b>") || !strings.Contains(last, "¿Generar la guía igual?") {
		t.Fatalf("debe advertir antes de cotizar, llegó: %s", last)
	}
	if strings.Join(f.buttons, ",") != "f:orden-h,x:orden-h" {
		t.Fatalf("botones = %v", f.buttons)
	}
	// "No generar" vuelve al botón de generar con la advertencia arriba.
	b.handleCallback(ctx, callback("x:orden-h"))
	if strings.Join(f.buttons, ",") != "g:orden-h" || !strings.HasPrefix(f.edits[len(f.edits)-1], "⚠️") {
		t.Fatalf("después de no generar: %v %s", f.buttons, f.edits[len(f.edits)-1])
	}
	// "Generar de todas formas" cotiza, y ya no vuelve a preguntar.
	b.handleCallback(ctx, callback("f:orden-h"))
	if strings.Join(f.buttons, ",") != "c:orden-h,x:orden-h" {
		t.Fatalf("debe cotizar después de forzar, botones = %v", f.buttons)
	}
	b.handleCallback(ctx, callback("x:orden-h"))
	b.handleCallback(ctx, callback("g:orden-h"))
	if strings.Join(f.buttons, ",") != "c:orden-h,x:orden-h" {
		t.Fatalf("ya aceptado, generar debe cotizar directo, botones = %v", f.buttons)
	}
}
