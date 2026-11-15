package bot

import (
	"strings"
	"testing"
	"time"

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
			{Date: "2026-11-14T11:00:00-05:00", Tracking: "B", Status: "pending", Units: 2, Commission: 24000},
			{Date: "2026-11-13T22:00:00-05:00", Tracking: "C", Status: "collected", Units: 2, Commission: 24000},
			{Date: "2026-11-13T09:00:00-05:00", Tracking: "D", Status: "collected", Units: 1, Commission: 12000},
			{Date: "2026-11-13T09:00:00-05:00", Tracking: "E", Status: "pending", Units: 1, Commission: 12000},
			{Date: "2026-11-12T09:00:00-05:00", Tracking: "F", Status: "failed", Units: 1, Commission: 12000},
			{Date: "2026-11-12T09:00:00-05:00", Tracking: "G", Status: "cancelled"},
		},
	}
	estados := map[string]string{"A": "camino", "B": "oficina", "E": "novedad"}
	msg := deudaTexto(d, h, f, estados)
	for _, want := range []string{"Del 12 nov al 14 nov",
		"sáb 14 nov</b>: 🚚 $12.000 (1) · 🏢 $24.000 (2)",
		"vie 13 nov</b>: ✅ $36.000 (3) · ⚠️ $12.000 (1)",
		"jue 12 nov</b>: ❌ 1 sin cobrar",
		"Acumulado en esos días: $84.000</b> (7 datáfonos)",
		"Se le debe: $36.000", "En camino: $12.000 (1 datáfono)", "En oficina: $24.000 (2 datáfonos)", "Con novedad: $12.000"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
	if !strings.Contains(deudaTexto(d, h, skydropx.Finance{}, nil), "No hay ventas") {
		t.Error("sin envíos debe decir que no hay ventas")
	}
}
