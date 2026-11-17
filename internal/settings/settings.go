// Package settings stores the settings changed from the app or the bot (without touching environment variables),
// such as the seller's per-terminal commission.
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

// CommissionRate is what the seller is paid per terminal from a date (YYYY-MM-DD, Colombia time).
type CommissionRate struct {
	From  string  `json:"from"`
	Value float64 `json:"value"`
}

// VendorPayment is a commission payment to the seller. It covers the already collected shipments marked when
// it was recorded; the difference between what was paid and what it covers stays as balance.
type VendorPayment struct {
	Date      string   `json:"date"`              // day the payment was recorded (YYYY-MM-DD)
	Amount    float64  `json:"amount"`            // what was paid
	Covered   float64  `json:"covered"`           // commission of the shipments it covers
	Units     int      `json:"units"`             // terminals of those shipments
	Through   string   `json:"through,omitempty"` // covers labels created up to this day (empty = all collected)
	Trackings []string `json:"trackings"`         // labels it covers
}

// AdExpense is an ad expense split half and half with the seller.
type AdExpense struct {
	Date   string  `json:"date"`           // day of the expense (YYYY-MM-DD)
	Amount float64 `json:"amount"`         // what it cost in total
	PaidBy string  `json:"paid_by"`        // "yo" (the owner: the seller owes half) or "vendedor" (half is owed to them)
	Note   string  `json:"note,omitempty"` // what it was for (Facebook, TikTok…)
}

// VendorShare is what the ad adds to what is owed to the seller: +half if they paid it,
// -half if the owner paid it.
func (a AdExpense) VendorShare() float64 {
	if a.PaidBy == "vendedor" {
		return a.Amount / 2
	}
	return -a.Amount / 2
}

// Store is the settings file. It is safe to use from several goroutines.
type Store struct {
	mu         sync.Mutex
	path       string
	fallback   float64          // commission before the first change (COMISION_POR_DATAFONO)
	Commission []CommissionRate `json:"commission"`
	Payments   []VendorPayment  `json:"vendor_payments"`
	Ads        []AdExpense      `json:"ads"`
}

// Load reads the settings from path; if the file doesn't exist, it starts empty.
// fallback is the commission for sales before the first change.
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

// CommissionAt is the per-terminal commission in effect at time t.
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

// CurrentCommission is the per-terminal commission in effect today.
func (s *Store) CurrentCommission() float64 { return s.CommissionAt(time.Now()) }

// CommissionHistory returns the commission changes, oldest first.
func (s *Store) CommissionHistory() []CommissionRate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]CommissionRate(nil), s.Commission...)
}

// SetCommission sets the per-terminal commission from day from (YYYY-MM-DD; empty = today).
// Earlier sales keep their value. A change on a date that already had a value replaces it.
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

// AddVendorPayment records a payment to the seller.
func (s *Store) AddVendorPayment(p VendorPayment) error {
	if p.Amount < 0 || p.Amount > 1_000_000_000 {
		return fmt.Errorf("valor de pago inválido: %.0f", p.Amount)
	}
	s.mu.Lock()
	s.Payments = append(s.Payments, p)
	s.mu.Unlock()
	return s.save()
}

// RemoveLastVendorPayment deletes the last recorded payment (to fix a mistake). ok is false if there is none.
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

// VendorPayments returns the payments to the seller, oldest first.
func (s *Store) VendorPayments() []VendorPayment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]VendorPayment(nil), s.Payments...)
}

// PaidTrackings are the labels already paid to the seller, with the payment day.
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

// VendorBalance is what is left from payments and shared ads: positive if the seller is still
// owed, negative if they owe the owner (deducted from their next commission).
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

// AddAd records a shared ad expense.
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

// RemoveLastAd deletes the last recorded ad expense. ok is false if there is none.
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

// AdExpenses returns the ad expenses, in the order they were recorded.
func (s *Store) AdExpenses() []AdExpense {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]AdExpense(nil), s.Ads...)
}

// save writes the file atomically (temp + rename).
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
