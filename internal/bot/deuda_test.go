package bot

import (
	"strings"
	"testing"
	"time"

	"skydropx-automator/internal/settings"
	"skydropx-automator/internal/skydropx"
)

func TestRangoDeuda(t *testing.T) {
	now := time.Date(2026, 11, 14, 15, 0, 0, 0, colombiaTZ) // sábado
	casos := []struct{ args, desde, hasta string }{
		{"", "2026-11-08", "2026-11-14"},
		{" 3", "2026-11-12", "2026-11-14"},
		{"hoy", "2026-11-14", "2026-11-14"},
		{"ayer", "2026-11-13", "2026-11-13"},
		{"semana", "2026-11-02", "2026-11-08"},
		{"mes", "2026-11-01", "2026-11-14"},
		{"2026-11-03", "2026-11-03", "2026-11-03"},
		{"2026-11-10 2026-11-01", "2026-11-01", "2026-11-10"},
	}
	for _, c := range casos {
		d, h, err := rangoDeuda(c.args, now)
		if err != nil {
			t.Errorf("%q: %v", c.args, err)
			continue
		}
		if got := d.Format("2006-01-02") + " " + h.Format("2006-01-02"); got != c.desde+" "+c.hasta {
			t.Errorf("%q: %s, quería %s %s", c.args, got, c.desde, c.hasta)
		}
	}
	for _, mal := range []string{"0", "100", "mañana", "2026-01-01 2026-12-31", "a b c"} {
		if _, _, err := rangoDeuda(mal, now); err == nil {
			t.Errorf("%q debería dar error", mal)
		}
	}
}

func TestDeudaTexto(t *testing.T) {
	d := time.Date(2026, 11, 12, 0, 0, 0, 0, colombiaTZ)
	h := time.Date(2026, 11, 14, 0, 0, 0, 0, colombiaTZ)
	f := skydropx.Finance{
		PerShipment: []skydropx.ShipmentMoney{
			{Date: "2026-11-14T10:00:00-05:00", Tracking: "A", Status: "pending", Units: 1, Commission: 12000},
			{Date: "2026-11-14T11:00:00-05:00", Tracking: "B", Customer: "Ana Ruiz", Status: "pending", Units: 2, Commission: 24000},
			{Date: "2026-11-13T22:00:00-05:00", Tracking: "C", Customer: "Luis Gómez", Status: "collected", Units: 2, Commission: 24000},
			{Date: "2026-11-13T09:00:00-05:00", Tracking: "D", Status: "collected", Units: 1, Commission: 12000},
			{Date: "2026-11-12T08:00:00-05:00", Tracking: "H", Status: "collected", Units: 1, Commission: 12000},
			{Date: "2026-11-13T09:00:00-05:00", Tracking: "E", Status: "pending", Units: 1, Commission: 12000},
			{Date: "2026-11-12T09:00:00-05:00", Tracking: "F", Status: "failed", Units: 1, Commission: 12000},
			{Date: "2026-11-12T09:00:00-05:00", Tracking: "G", Status: "cancelled"},
		},
	}
	estados := map[string]string{"A": "camino", "B": "oficina", "E": "novedad"}
	msg := deudaTexto(d, h, f, estados, cuentaVendedor{
		pagadas: map[string]string{"H": "2026-11-13"},
		ads:     []settings.AdExpense{{Date: "2026-11-13", Amount: 40000, PaidBy: "yo"}, {Date: "2026-11-01", Amount: 99000}},
		saldo:   6000,
	})
	for _, want := range []string{"Del 12 nov al 14 nov · 6 pedidos = 8 datáfonos",
		"PARA PAGARLE HOY: $42.000", "Cobrado sin pagarle: $36.000", "Más lo que le quedó debiendo: +$6.000",
		"Ya le pagaste: <b>$12.000</b> · 1 datáfono",
		"Cobrado, por pagarle: <b>$36.000</b> · 3 datáfonos",
		"Falta que el cliente pague: <b>$48.000</b> · 4 datáfonos",
		"🚚 En camino: $12.000 · 1 datáfono", "🏢 En oficina (el cliente debe reclamarlo): $24.000 · 2 datáfonos",
		"⚠️ Con novedad o en devolución: $12.000 · 1 datáfono",
		"No se cobraron: 1 pedido", "Total ganado: $96.000</b> · 8 datáfonos",
		"Publicidad en estas fechas: $40.000 (él pone $20.000)",
		"<b>sáb 14 nov</b> · 3 datáfonos · $36.000\n   🚚 $12.000 · 🏢 $24.000\n   📦 Ana Ruiz: 2 datáfonos × $12.000 🏢",
		"<b>vie 13 nov</b> · 4 datáfonos · $48.000\n   ✅ $36.000 · ⚠️ $12.000\n   📦 Luis Gómez: 2 datáfonos × $12.000 ✅",
		"<b>jue 12 nov</b> · 1 datáfono · $12.000\n   💵 $12.000 · ❌ 1 sin cobrar"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
	if !strings.Contains(deudaTexto(d, h, skydropx.Finance{}, nil, cuentaVendedor{}), "No hay ventas") {
		t.Error("sin envíos debe decir que no hay ventas")
	}
}
