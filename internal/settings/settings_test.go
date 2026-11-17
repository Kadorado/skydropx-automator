package settings

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCommissionByDate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := Load(path, 15000)
	if err != nil {
		t.Fatal(err)
	}
	day := func(d string) time.Time { t, _ := time.ParseInLocation("2006-01-02 15:04", d, colombia); return t }

	if got := s.CommissionAt(day("2026-11-10 12:00")); got != 15000 {
		t.Fatalf("sin cambios debe usar el valor inicial: %v", got)
	}
	if _, err := s.SetCommission(12000, "2026-11-15"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetCommission(18000, "2026-12-01"); err != nil {
		t.Fatal(err)
	}
	cases := map[string]float64{
		"2026-11-14 23:59": 15000, // before the change: what was paid is respected
		"2026-11-15 00:00": 12000,
		"2026-11-30 18:00": 12000,
		"2026-12-01 08:00": 18000,
	}
	for d, want := range cases {
		if got := s.CommissionAt(day(d)); got != want {
			t.Errorf("%s: %v, se esperaba %v", d, got, want)
		}
	}

	// Correcting a date's value replaces it, and everything survives a restart.
	if _, err := s.SetCommission(13000, "2026-11-15"); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path, 15000)
	if err != nil {
		t.Fatal(err)
	}
	if h := again.CommissionHistory(); len(h) != 2 || h[0].Value != 13000 {
		t.Fatalf("historial inesperado: %+v", h)
	}
	if _, err := again.SetCommission(-1, ""); err == nil {
		t.Fatal("una comisión negativa debe dar error")
	}
	if _, err := again.SetCommission(1000, "15/11/2026"); err == nil {
		t.Fatal("una fecha mal escrita debe dar error")
	}
}

func TestVendorPayments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ajustes.json")
	s, _ := Load(path, 15000)
	if err := s.AddVendorPayment(VendorPayment{Date: "2026-11-16", Amount: 30000, Covered: 36000, Trackings: []string{"A", "B"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddVendorPayment(VendorPayment{Date: "2026-11-17", Amount: 20000, Covered: 12000, Trackings: []string{"C"}}); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path, 15000) // it is saved to the file
	if err != nil {
		t.Fatal(err)
	}
	if got := s.PaidTrackings(); len(got) != 3 || got["A"] != "2026-11-16" || got["C"] != "2026-11-17" {
		t.Errorf("PaidTrackings: %v", got)
	}
	if b := s.VendorBalance(); b != -2000 {
		t.Errorf("VendorBalance: %v", b)
	}
	p, ok, err := s.RemoveLastVendorPayment()
	if err != nil || !ok || p.Date != "2026-11-17" {
		t.Fatalf("RemoveLast: %+v %v %v", p, ok, err)
	}
	if b := s.VendorBalance(); b != 6000 || len(s.PaidTrackings()) != 2 {
		t.Errorf("después de borrar: %v %v", b, s.PaidTrackings())
	}
}

func TestAdsInBalance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ajustes.json")
	s, _ := Load(path, 15000)
	_ = s.AddVendorPayment(VendorPayment{Date: "2026-11-16", Amount: 30000, Covered: 36000}) // 6.000 short
	if err := s.AddAd(AdExpense{Date: "2026-11-16", Amount: 40000}); err != nil {            // the owner paid: -20.000
		t.Fatal(err)
	}
	if err := s.AddAd(AdExpense{Date: "2026-11-17", Amount: 10000, PaidBy: "vendedor"}); err != nil { // +5.000
		t.Fatal(err)
	}
	if err := s.AddAd(AdExpense{Amount: 0}); err == nil {
		t.Error("publicidad en 0 debería dar error")
	}
	s, _ = Load(path, 15000)
	if b := s.VendorBalance(); b != -9000 {
		t.Errorf("VendorBalance: %v", b)
	}
	if a, ok, _ := s.RemoveLastAd(); !ok || a.PaidBy != "vendedor" || s.VendorBalance() != -14000 {
		t.Errorf("RemoveLastAd: %+v %v", a, s.VendorBalance())
	}
}
