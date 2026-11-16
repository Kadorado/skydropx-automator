package bot

import (
	"strings"
	"testing"
	"time"

	"skydropx-automator/internal/settings"
	"skydropx-automator/internal/skydropx"
)

func TestArgsPago(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, colombiaTZ)
	casos := []struct {
		args  string
		valor float64
		hasta string
	}{
		{"150000", 150000, ""},
		{"$150.000", 150000, ""},
		{"150000 hasta ayer", 150000, "2026-11-15"},
		{"150000 hoy", 150000, "2026-11-16"},
		{"150000 hasta 2026-11-10", 150000, "2026-11-10"},
	}
	for _, c := range casos {
		v, h, err := argsPago(c.args, now)
		if err != nil || v != c.valor || h != c.hasta {
			t.Errorf("%q: %v %q %v", c.args, v, h, err)
		}
	}
	for _, mal := range []string{"", "abc", "0", "150000 mañana", "150000 hasta 2026-11-10 x"} {
		if _, _, err := argsPago(mal, now); err == nil {
			t.Errorf("%q debería dar error", mal)
		}
	}
}

func TestPorPagarYPagoTexto(t *testing.T) {
	rows := []skydropx.ShipmentMoney{
		{Date: "2026-11-13T10:00:00-05:00", Tracking: "A", Status: "collected", Units: 3, Commission: 45000},
		{Date: "2026-11-12T11:00:00-05:00", Tracking: "B", Status: "collected", Units: 2, Commission: 30000},
		{Date: "2026-11-11T10:00:00-05:00", Tracking: "C", Status: "collected", Units: 4, Commission: 60000},
		{Date: "2026-11-10T10:00:00-05:00", Tracking: "D", Status: "collected", Units: 1, Commission: 15000},
		{Date: "2026-11-06T10:00:00-05:00", Tracking: "E", Status: "collected", Units: 1, Commission: 15000},
		{Date: "2026-11-05T10:00:00-05:00", Tracking: "F", Status: "collected", Units: 1, Commission: 15000},
		{Date: "2026-11-13T10:00:00-05:00", Tracking: "G", Status: "pending", Units: 1, Commission: 15000},
	}
	pendientes := porPagar(rows, map[string]string{"F": "2026-11-07"})
	if len(pendientes) != 5 {
		t.Fatalf("porPagar: %+v", pendientes)
	}

	// El caso real: $120.000 cubren del 6 al 12 de noviembre; las 3 del 13 quedan por pagarle.
	cubre, quedan := repartirPago(pendientes, 120000)
	if len(cubre) != 4 || cubre[0].Tracking != "E" || cubre[3].Tracking != "B" || len(quedan) != 1 || quedan[0].Tracking != "A" {
		t.Fatalf("repartirPago: cubre %+v quedan %+v", cubre, quedan)
	}
	p := settings.VendorPayment{Date: "2026-11-16", Amount: 120000, Covered: 120000, Units: 8}
	msg := pagoTexto(p, cubre, quedan, 0)
	for _, want := range []string{"Pago al vendedor registrado: $120.000", "Envíos que quedan pagados",
		"mié 11 nov · 1 pedido = 4 datáfonos · $60.000", "Comisión pagada: $120.000</b> (4 pedidos = 8 datáfonos)",
		"Aún se le debe: $45.000</b> (1 pedido = 3 datáfonos, ya cobrados)", "vie 13 nov · 1 pedido = 3 datáfonos · $45.000"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "aún le debes") || strings.Contains(msg, "te debe") {
		t.Errorf("no debe quedar saldo aparte:\n%s", msg)
	}

	// Con publicidad pendiente (él te debe $20.000), $130.000 alcanzan para $150.000 de comisión.
	if cubre, _ := repartirPago(pendientes, 130000-(-20000)); len(cubre) != 4 {
		t.Errorf("con saldo a favor: %+v", cubre)
	}
	// Lo que sobra sin completar un envío queda como adelanto.
	cubre, quedan = repartirPago(pendientes, 40000)
	p = settings.VendorPayment{Amount: 40000, Covered: 30000}
	if msg := pagoTexto(p, cubre, quedan, 0); len(cubre) != 2 || !strings.Contains(msg, "él te debe <b>$10.000") {
		t.Errorf("adelanto parcial (%d envíos):\n%s", len(cubre), msg)
	}
	if msg := pagoTexto(settings.VendorPayment{Amount: 30000, Covered: 30000}, cubre, nil, 0); !strings.Contains(msg, "paz y salvo") {
		t.Errorf("pago exacto:\n%s", msg)
	}
	if msg := pagoTexto(settings.VendorPayment{Amount: 10000}, nil, nil, 0); !strings.Contains(msg, "queda como adelanto") || !strings.Contains(msg, "él te debe <b>$10.000") {
		t.Errorf("adelanto:\n%s", msg)
	}
}
