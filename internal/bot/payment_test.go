package bot

import (
	"strings"
	"testing"
	"time"

	"skydropx-automator/internal/settings"
	"skydropx-automator/internal/skydropx"
)

func TestPaymentArgs(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, colombiaTZ)
	cases := []struct {
		args  string
		amt   float64
		until string
	}{
		{"150000", 150000, ""},
		{"$150.000", 150000, ""},
		{"150000 hasta ayer", 150000, "2026-11-15"},
		{"150000 hoy", 150000, "2026-11-16"},
		{"150000 hasta 2026-11-10", 150000, "2026-11-10"},
	}
	for _, c := range cases {
		v, h, err := paymentArgs(c.args, now)
		if err != nil || v != c.amt || h != c.until {
			t.Errorf("%q: %v %q %v", c.args, v, h, err)
		}
	}
	for _, bad := range []string{"", "abc", "0", "150000 mañana", "150000 hasta 2026-11-10 x"} {
		if _, _, err := paymentArgs(bad, now); err == nil {
			t.Errorf("%q debería dar error", bad)
		}
	}
}

func TestUnpaidAndPaymentText(t *testing.T) {
	rows := []skydropx.ShipmentMoney{
		{Date: "2026-11-13T10:00:00-05:00", Tracking: "A", Status: "collected", Units: 3, Commission: 45000},
		{Date: "2026-11-12T11:00:00-05:00", Tracking: "B", Status: "collected", Units: 2, Commission: 30000},
		{Date: "2026-11-11T10:00:00-05:00", Tracking: "C", Status: "collected", Units: 4, Commission: 60000},
		{Date: "2026-11-10T10:00:00-05:00", Tracking: "D", Status: "collected", Units: 1, Commission: 15000},
		{Date: "2026-11-06T10:00:00-05:00", Tracking: "E", Status: "collected", Units: 1, Commission: 15000},
		{Date: "2026-11-05T10:00:00-05:00", Tracking: "F", Status: "collected", Units: 1, Commission: 15000},
		{Date: "2026-11-13T10:00:00-05:00", Tracking: "G", Status: "pending", Units: 1, Commission: 15000},
	}
	pendingRows := unpaidRows(rows, map[string]string{"F": "2026-11-07"})
	if len(pendingRows) != 5 {
		t.Fatalf("porPagar: %+v", pendingRows)
	}

	// The real case: $120.000 covers Nov 6 to 12; the 3 from the 13th remain unpaid.
	covered, remaining := allocatePayment(pendingRows, 120000)
	if len(covered) != 4 || covered[0].Tracking != "E" || covered[3].Tracking != "B" || len(remaining) != 1 || remaining[0].Tracking != "A" {
		t.Fatalf("repartirPago: cubre %+v quedan %+v", covered, remaining)
	}
	p := settings.VendorPayment{Date: "2026-11-16", Amount: 120000, Covered: 120000, Units: 8}
	msg := paymentText(p, covered, remaining, 0)
	for _, want := range []string{"PAGO AL VENDEDOR REGISTRADO", "lun 16 nov · <b>$120.000</b>",
		"mié 11 nov · 1 pedido = 4 datáfonos · $60.000", "QUEDA PAGADO: $120.000</b> · 4 pedidos = 8 datáfonos",
		"AÚN SE LE DEBE: $45.000</b> · 1 pedido = 3 datáfonos ya cobrados", "vie 13 nov · 1 pedido = 3 datáfonos · $45.000"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "aún le debes") || strings.Contains(msg, "te debe") {
		t.Errorf("no debe quedar saldo aparte:\n%s", msg)
	}

	// With pending ads (he owes you $20.000), $130.000 covers $150.000 of commission.
	if covered, _ := allocatePayment(pendingRows, 130000-(-20000)); len(covered) != 4 {
		t.Errorf("con saldo a favor: %+v", covered)
	}
	// What is left over without completing a shipment stays as an advance.
	covered, remaining = allocatePayment(pendingRows, 40000)
	p = settings.VendorPayment{Amount: 40000, Covered: 30000}
	if msg := paymentText(p, covered, remaining, 0); len(covered) != 2 || !strings.Contains(msg, "él te debe <b>$10.000") {
		t.Errorf("adelanto parcial (%d envíos):\n%s", len(covered), msg)
	}
	if msg := paymentText(settings.VendorPayment{Amount: 30000, Covered: 30000}, covered, nil, 0); !strings.Contains(msg, "paz y salvo") {
		t.Errorf("pago exacto:\n%s", msg)
	}
	if msg := paymentText(settings.VendorPayment{Amount: 10000}, nil, nil, 0); !strings.Contains(msg, "queda como adelanto") || !strings.Contains(msg, "él te debe <b>$10.000") {
		t.Errorf("adelanto:\n%s", msg)
	}
}
