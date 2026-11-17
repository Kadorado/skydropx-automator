package bot

import (
	"strings"
	"testing"
	"time"

	"skydropx-automator/internal/settings"
)

func TestAdArgs(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, colombiaTZ)
	cases := []struct {
		args string
		want settings.AdExpense
	}{
		{"40000", settings.AdExpense{Date: "2026-11-16", Amount: 40000, PaidBy: "yo"}},
		{"$40.000 vendedor", settings.AdExpense{Date: "2026-11-16", Amount: 40000, PaidBy: "vendedor"}},
		{"40000 ayer facebook ads", settings.AdExpense{Date: "2026-11-15", Amount: 40000, PaidBy: "yo", Note: "facebook ads"}},
		{"40000 él 2026-11-10 tiktok", settings.AdExpense{Date: "2026-11-10", Amount: 40000, PaidBy: "vendedor", Note: "tiktok"}},
	}
	for _, c := range cases {
		got, err := adArgs(c.args, now)
		if err != nil || got != c.want {
			t.Errorf("%q: %+v %v", c.args, got, err)
		}
	}
	for _, bad := range []string{"", "abc", "0"} {
		if _, err := adArgs(bad, now); err == nil {
			t.Errorf("%q debería dar error", bad)
		}
	}
}

func TestAdsText(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, colombiaTZ)
	ads := []settings.AdExpense{
		{Date: "2026-10-30", Amount: 50000, PaidBy: "yo"},
		{Date: "2026-11-02", Amount: 40000, PaidBy: "yo", Note: "facebook"},
		{Date: "2026-11-10", Amount: 20000, PaidBy: "vendedor"},
	}
	msg := adsText(ads, -35000, now)
	for _, want := range []string{"2026-11-10: <b>$20.000</b> · pagó vendedor", "2026-11-02: <b>$40.000</b> · pagó tú · facebook",
		"Este mes: $60.000 (tú $40.000 · vendedor $20.000); a cada uno le toca $30.000", "él te debe <b>$35.000"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
}
