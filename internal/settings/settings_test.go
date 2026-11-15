package settings

import (
	"path/filepath"
	"testing"
	"time"
)

func TestComisionPorFecha(t *testing.T) {
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
		"2026-11-14 23:59": 15000, // antes del cambio: se respeta lo que se pagaba
		"2026-11-15 00:00": 12000,
		"2026-11-30 18:00": 12000,
		"2026-12-01 08:00": 18000,
	}
	for d, want := range cases {
		if got := s.CommissionAt(day(d)); got != want {
			t.Errorf("%s: %v, se esperaba %v", d, got, want)
		}
	}

	// Corregir el valor de una fecha lo reemplaza, y todo sobrevive al reinicio.
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
