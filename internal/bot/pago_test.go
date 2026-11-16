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
		{Date: "2026-11-14T10:00:00-05:00", Tracking: "A", Status: "collected", Units: 2, Commission: 24000},
		{Date: "2026-11-14T11:00:00-05:00", Tracking: "B", Status: "collected", Units: 1, Commission: 12000},
		{Date: "2026-11-13T10:00:00-05:00", Tracking: "C", Status: "collected", Units: 1, Commission: 12000},
		{Date: "2026-11-13T10:00:00-05:00", Tracking: "D", Status: "pending", Units: 1, Commission: 12000},
	}
	cubre := porPagar(rows, map[string]string{"C": "2026-11-14"})
	if len(cubre) != 2 || cubre[0].Tracking != "A" || cubre[1].Tracking != "B" {
		t.Fatalf("porPagar: %+v", cubre)
	}
	p := settings.VendorPayment{Date: "2026-11-16", Amount: 30000, Covered: 36000, Units: 3, Through: "2026-11-15"}
	msg := pagoTexto(p, cubre, 0)
	for _, want := range []string{"Pago al vendedor registrado: $30.000", "guías creadas hasta el 2026-11-15",
		"sáb 14 nov · 2 pedidos = 3 datáfonos · $36.000", "Le pagaste $6.000 menos", "aún le debes <b>$6.000"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
	if msg := pagoTexto(settings.VendorPayment{Amount: 36000, Covered: 36000}, cubre, 0); !strings.Contains(msg, "Cuadra exacto") || strings.Contains(msg, "le debes") {
		t.Errorf("pago exacto:\n%s", msg)
	}
	if msg := pagoTexto(settings.VendorPayment{Amount: 10000}, nil, 0); !strings.Contains(msg, "él te debe <b>$10.000") {
		t.Errorf("adelanto:\n%s", msg)
	}
}
