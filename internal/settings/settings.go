// Package settings guarda los ajustes que se cambian desde la app o el bot (sin tocar variables de entorno),
// como la comisión del vendedor por datáfono.
package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var colombia = time.FixedZone("COT", -5*3600)

// CommissionRate es lo que se le paga al vendedor por datáfono desde una fecha (AAAA-MM-DD, hora de Colombia).
type CommissionRate struct {
	From  string  `json:"from"`
	Value float64 `json:"value"`
}

// Store es el archivo de ajustes. Es seguro usarlo desde varias goroutines.
type Store struct {
	mu         sync.Mutex
	path       string
	fallback   float64          // comisión antes del primer cambio (COMISION_POR_DATAFONO)
	Commission []CommissionRate `json:"commission"`
}

// Load lee los ajustes de path; si el archivo no existe, empieza vacío.
// fallback es la comisión que aplica a las ventas anteriores al primer cambio.
func Load(path string, fallback float64) (*Store, error) {
	s := &Store{path: path, fallback: fallback}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	sort.Slice(s.Commission, func(i, j int) bool { return s.Commission[i].From < s.Commission[j].From })
	return s, nil
}

// CommissionAt es la comisión por datáfono vigente en el momento t.
func (s *Store) CommissionAt(t time.Time) float64 {
	day := t.In(colombia).Format("2006-01-02")
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.fallback
	for _, r := range s.Commission {
		if r.From > day {
			break
		}
		v = r.Value
	}
	return v
}

// CurrentCommission es la comisión por datáfono vigente hoy.
func (s *Store) CurrentCommission() float64 { return s.CommissionAt(time.Now()) }

// CommissionHistory devuelve los cambios de comisión, del más antiguo al más nuevo.
func (s *Store) CommissionHistory() []CommissionRate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]CommissionRate(nil), s.Commission...)
}

// SetCommission fija la comisión por datáfono desde el día from (AAAA-MM-DD; vacío = hoy).
// Las ventas anteriores conservan su valor. Un cambio en una fecha que ya tenía valor lo reemplaza.
func (s *Store) SetCommission(value float64, from string) (CommissionRate, error) {
	if value < 0 || value > 10_000_000 {
		return CommissionRate{}, fmt.Errorf("valor de comisión inválido: %.0f", value)
	}
	if from == "" {
		from = time.Now().In(colombia).Format("2006-01-02")
	}
	if _, err := time.ParseInLocation("2006-01-02", from, colombia); err != nil {
		return CommissionRate{}, fmt.Errorf("fecha inválida: %s (usa AAAA-MM-DD)", from)
	}
	r := CommissionRate{From: from, Value: value}
	s.mu.Lock()
	kept := s.Commission[:0:0]
	for _, old := range s.Commission {
		if old.From != from {
			kept = append(kept, old)
		}
	}
	s.Commission = append(kept, r)
	sort.Slice(s.Commission, func(i, j int) bool { return s.Commission[i].From < s.Commission[j].From })
	s.mu.Unlock()
	return r, s.save()
}

// save escribe el archivo de forma atómica (temporal + renombrado).
func (s *Store) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
