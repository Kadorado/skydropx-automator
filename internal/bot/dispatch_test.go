package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"skydropx-automator/internal/skydropx"
)

func at(m time.Month, d, h, min int) time.Time {
	return time.Date(2026, m, d, h, min, 0, 0, colombiaTZ)
}

func TestDispatchText(t *testing.T) {
	now := at(11, 17, 8, 0) // Tuesday
	list := []skydropx.Undispatched{
		{Tracking: "G1", Name: "Ana Pérez", City: "Montería", Department: "Córdoba", CreatedAt: at(11, 13, 18, 10)},
		{Tracking: "G2", Name: "Luis", CreatedAt: at(11, 17, 7, 30), Unknown: true},
	}
	msg := dispatchText(list, now)
	for _, want := range []string{"POR DESPACHAR", "Hay <b>2</b> guías", "<b>Ana Pérez</b> — Montería, Córdoba",
		"Guía <code>G1</code> · creada vie 13 nov 6:10 p. m. (hace 4 días)", "(hoy)", "No pude revisar el rastreo",
		"Hoy la oficina atiende hasta las 6:30 p. m."} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
	if !strings.Contains(dispatchText(nil, now), "Nada por despachar") {
		t.Error("empty list")
	}
}

func TestOfficeLine(t *testing.T) {
	cases := map[time.Time]string{
		at(11, 14, 9, 0):   "Hoy la oficina atiende hasta las 5 p. m.",                           // Saturday
		at(11, 14, 17, 30): "La oficina ya cerró. Próximo día: mar 17 nov, hasta las 6:30 p. m.", // Saturday evening
		at(11, 15, 9, 0):   "no abre hoy (domingo). Próximo día: mar 17 nov",
		at(11, 16, 9, 0):   "no abre hoy (festivo). Próximo día: mar 17 nov",
		at(11, 20, 18, 0):  "Hoy la oficina atiende hasta las 6:30 p. m.",
		at(11, 20, 18, 30): "ya cerró. Próximo día: sáb 21 nov, hasta las 5 p. m.",
	}
	for now, want := range cases {
		if got := officeLine(now); !strings.Contains(got, want) {
			t.Errorf("%s: %q, want %q", now.Format("Mon 02 15:04"), got, want)
		}
	}
}

func TestDispatchReminderSchedule(t *testing.T) {
	b, f := newTestBot(t)
	ctx := context.Background()
	b.opts.OpsChatIDs = []int64{-100}
	created := time.Now().Add(-48 * time.Hour).Format(time.RFC3339)
	f.shipmentList = `{"data":[
		{"id":"d1","attributes":{"created_at":"` + created + `","master_tracking_number":"DSP-WAITING","carrier_name":"interrapidisimo"},"relationships":{"address_to":{"data":{"id":"a1"}}}},
		{"id":"d2","attributes":{"created_at":"` + created + `","master_tracking_number":"DSP-SENT","carrier_name":"interrapidisimo"},"relationships":{"address_to":{"data":{"id":"a1"}}}}
	],"included":[{"id":"a1","type":"address","attributes":{"name":"Ana Pérez","area_level2":"Montería","area_level1":"Córdoba"}}],"meta":{"total_pages":1}}`
	f.tracking = map[string]string{"DSP-WAITING": "created", "DSP-SENT": "picked_up"}

	// Sunday, holiday, too early and after closing: nothing.
	for _, now := range []time.Time{at(11, 15, 9, 0), at(11, 16, 9, 0), at(11, 17, 7, 0), at(11, 21, 17, 5)} {
		b.dispatchReminderIfDue(ctx, now)
	}
	if len(f.messages) != 0 {
		t.Fatalf("no reminder on Sundays, holidays, before 8 nor after closing: %v", f.messages)
	}

	b.dispatchReminderIfDue(ctx, at(11, 17, 8, 5))
	if len(f.messages) != 1 {
		t.Fatalf("one reminder on Tuesday morning, got %d", len(f.messages))
	}
	msg := f.messages[0]
	if !strings.Contains(msg, "DSP-WAITING") || strings.Contains(msg, "DSP-SENT") || !strings.Contains(msg, "Hay <b>1</b> guía") {
		t.Fatalf("only what Interrapidísimo hasn't received:\n%s", msg)
	}
	b.dispatchReminderIfDue(ctx, at(11, 17, 10, 0))
	if len(f.messages) != 1 {
		t.Fatal("once per day")
	}

}

func TestDispatchReminderSilentWhenAllDispatched(t *testing.T) {
	b, f := newTestBot(t)
	b.opts.OpsChatIDs = []int64{-100}
	created := time.Now().Add(-24 * time.Hour).Format(time.RFC3339)
	f.shipmentList = `{"data":[{"id":"d3","attributes":{"created_at":"` + created + `","master_tracking_number":"DSP-ALL-SENT"}}],"meta":{"total_pages":1}}`
	f.tracking = map[string]string{"DSP-ALL-SENT": "in_transit"}
	b.dispatchReminderIfDue(context.Background(), at(11, 18, 8, 0))
	if len(f.messages) != 0 {
		t.Fatalf("nothing pending, nothing said: %v", f.messages)
	}
	if b.st.LastDispatchReminder != "2026-11-18" {
		t.Fatal("the day is marked as checked")
	}
}
