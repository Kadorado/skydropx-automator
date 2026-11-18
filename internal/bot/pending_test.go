package bot

import (
	"strings"
	"testing"
	"time"

	"skydropx-automator/internal/skydropx"
)

func TestPendingMessages(t *testing.T) {
	now := time.Date(2026, 11, 17, 20, 0, 0, 0, colombiaTZ)
	pending := []skydropx.ShipmentMoney{
		{Date: "2026-11-16T10:00:00-05:00", Tracking: "G2", Customer: "Luis", Phone: "3105556677", City: "Quibdó", CustomerPays: 75000},
		{Date: "2026-11-10T10:00:00-05:00", Tracking: "G1", Customer: "Ana", City: "Tadó", CustomerPays: 135000},
	}
	msgs := pendingMessages(pending, map[string]string{"G1": "oficina"}, now)
	all := strings.Join(msgs, "\n")
	for _, want := range []string{"POR COBRAR:</b> 2 envíos · $210.000", "En oficina</b> (1 · $135.000)", "G1 · 💵 $135.000 · hace 7 días",
		"En camino</b> (1 · $75.000)", "📞 3105556677 · 📍 Quibdó"} {
		if !strings.Contains(all, want) {
			t.Errorf("falta %q en:\n%s", want, all)
		}
	}
	if strings.Contains(all, "Con novedad") {
		t.Error("empty groups must not be shown")
	}
	if got := pendingMessages(nil, nil, now); len(got) != 1 || !strings.Contains(got[0], "No hay nada por cobrar") {
		t.Fatalf("empty report: %v", got)
	}
}
