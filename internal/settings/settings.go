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

// VendorPayment es un pago de comisión al vendedor. Cubre los envíos ya cobrados que se marcaron al
// registrarlo; la diferencia entre lo pagado y lo que cubre queda como saldo.
type VendorPayment struct {
	Date      string   `json:"date"`              // día en que se registró el pago (AAAA-MM-DD)
	Amount    float64  `json:"amount"`            // lo que se le pagó
	Covered   float64  `json:"covered"`           // comisión de los envíos que cubre
	Units     int      `json:"units"`             // datáfonos de esos envíos
	Through   string   `json:"through,omitempty"` // cubre guías creadas hasta este día (vacío = todas las cobradas)
	Trackings []string `json:"trackings"`         // guías que cubre
}

// AdExpense es un gasto de publicidad que se reparte mitad y mitad con el vendedor.
type AdExpense struct {
	Date   string  `json:"date"`           // día del gasto (AAAA-MM-DD)
	Amount float64 `json:"amount"`         // lo que costó en total
	PaidBy string  `json:"paid_by"`        // "yo" (el dueño: el vendedor debe la mitad) o "vendedor" (se le debe la mitad)
	Note   string  `json:"note,omitempty"` // para qué fue (Facebook, TikTok…)
}

// VendorShare es lo que la publicidad le suma a lo que se le debe al vendedor: +la mitad si la pagó él,
// -la mitad si la pagó el dueño.
func (a AdExpense) VendorShare() float64 {
	if a.PaidBy == "vendedor" {
		return a.Amount / 2
	}
	return -a.Amount / 2
}

// Store es el archivo de ajustes. Es seguro usarlo desde varias goroutines.
type Store struct {
	mu         sync.Mutex
	path       string
	fallback   float64          // comisión antes del primer cambio (COMISION_POR_DATAFONO)
	Commission []CommissionRate `json:"commission"`
	Payments   []VendorPayment  `json:"vendor_payments"`
	Ads        []AdExpense      `json:"ads"`
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

// AddVendorPayment registra un pago al vendedor.
func (s *Store) AddVendorPayment(p VendorPayment) error {
	if p.Amount < 0 || p.Amount > 1_000_000_000 {
		return fmt.Errorf("valor de pago inválido: %.0f", p.Amount)
	}
	s.mu.Lock()
	s.Payments = append(s.Payments, p)
	s.mu.Unlock()
	return s.save()
}

// RemoveLastVendorPayment borra el último pago registrado (para corregir un error). ok es false si no hay.
func (s *Store) RemoveLastVendorPayment() (p VendorPayment, ok bool, err error) {
	s.mu.Lock()
	if len(s.Payments) == 0 {
		s.mu.Unlock()
		return p, false, nil
	}
	p = s.Payments[len(s.Payments)-1]
	s.Payments = s.Payments[:len(s.Payments)-1]
	s.mu.Unlock()
	return p, true, s.save()
}

// VendorPayments devuelve los pagos al vendedor, del más antiguo al más nuevo.
func (s *Store) VendorPayments() []VendorPayment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]VendorPayment(nil), s.Payments...)
}

// PaidTrackings son las guías que ya se le pagaron al vendedor, con el día del pago.
func (s *Store) PaidTrackings() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for _, p := range s.Payments {
		for _, t := range p.Trackings {
			out[t] = p.Date
		}
	}
	return out
}

// VendorBalance es lo que quedó de los pagos y la publicidad compartida: positivo si se le sigue
// debiendo al vendedor, negativo si él le debe al dueño (se descuenta de su próxima comisión).
func (s *Store) VendorBalance() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b float64
	for _, p := range s.Payments {
		b += p.Covered - p.Amount
	}
	for _, a := range s.Ads {
		b += a.VendorShare()
	}
	return b
}

// AddAd registra un gasto de publicidad compartido.
func (s *Store) AddAd(a AdExpense) error {
	if a.Amount <= 0 || a.Amount > 1_000_000_000 {
		return fmt.Errorf("valor de publicidad inválido: %.0f", a.Amount)
	}
	if a.PaidBy != "vendedor" {
		a.PaidBy = "yo"
	}
	s.mu.Lock()
	s.Ads = append(s.Ads, a)
	s.mu.Unlock()
	return s.save()
}

// RemoveLastAd borra el último gasto de publicidad registrado. ok es false si no hay.
func (s *Store) RemoveLastAd() (a AdExpense, ok bool, err error) {
	s.mu.Lock()
	if len(s.Ads) == 0 {
		s.mu.Unlock()
		return a, false, nil
	}
	a = s.Ads[len(s.Ads)-1]
	s.Ads = s.Ads[:len(s.Ads)-1]
	s.mu.Unlock()
	return a, true, s.save()
}

// AdExpenses devuelve los gastos de publicidad, en el orden en que se registraron.
func (s *Store) AdExpenses() []AdExpense {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]AdExpense(nil), s.Ads...)
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
