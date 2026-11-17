package bot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"skydropx-automator/internal/domain"
	"skydropx-automator/internal/skydropx"
)

// Pending is an order created by the bot that doesn't have a label yet.
type Pending struct {
	OrderID     string    `json:"order_id"`
	ChatID      int64     `json:"chat_id"`
	Name        string    `json:"nombre"`
	Destination string    `json:"destino"`
	CreatedAt   time.Time `json:"created_at"`

	// To generate the label from Telegram (empty in orders created before this feature).
	Sender         string               `json:"sender,omitempty"`
	Data           *domain.ShipmentData `json:"data,omitempty"`
	Quote          *skydropx.LabelQuote `json:"quote,omitempty"`
	LabelRequested bool                 `json:"label_requested,omitempty"`

	// Warning is the customer's history (didn't pay or was slow to pick up) shown above the order;
	// with a warning, generating the label asks for confirmation (HistoryAccepted).
	Warning         string `json:"advertencia,omitempty"`
	HistoryAccepted bool   `json:"historial_aceptado,omitempty"`
}

// Recent is a recently created order, to detect repeated orders.
type Recent struct {
	Fingerprint string    `json:"huella"`
	OrderID     string    `json:"order_id"`
	Name        string    `json:"nombre"`
	CreatedAt   time.Time `json:"created_at"`
}

// State is saved to disk so progress isn't lost on restart.
type State struct {
	Offset  int64     `json:"offset"`
	Pending []Pending `json:"pending"`
	Recent  []Recent  `json:"recent"`

	// Collection state already announced per shipment (collected | failed), so the notice isn't repeated.
	CODNotices map[string]string `json:"cobros,omitempty"`

	// Delivery problem notices already sent (shipment:state:day -> day), so they aren't repeated.
	IssueNotices map[string]string `json:"avisos,omitempty"`

	// Customers that couldn't be saved in the customer list, kept until a retry works.
	UnsavedCustomers []UnsavedCustomer `json:"unsaved_customers,omitempty"`

	// Day (YYYY-MM-DD) of the last automatic backup of the customer list.
	LastCustomerBackup string `json:"last_customer_backup,omitempty"`

	// Day (YYYY-MM-DD) of the last morning reminder of labels left to dispatch.
	LastDispatchReminder string `json:"last_dispatch_reminder,omitempty"`

	// Last day (YYYY-MM-DD) each automatic summary was sent.
	LastDailySummary  string `json:"ultimo_resumen_diario,omitempty"`
	LastWeeklySummary string `json:"ultimo_resumen_semanal,omitempty"`

	mu   sync.Mutex
	path string
}

func loadState(path string) (*State, error) {
	s := &State{path: path}
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
	s.path = path
	return s, nil
}

// save writes the state atomically (temp file + rename).
func (s *State) save() error {
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

func (s *State) add(p Pending) error {
	s.mu.Lock()
	s.Pending = append(s.Pending, p)
	s.mu.Unlock()
	return s.save()
}

func (s *State) remove(orderID string) error {
	s.mu.Lock()
	kept := s.Pending[:0]
	for _, p := range s.Pending {
		if p.OrderID != orderID {
			kept = append(kept, p)
		}
	}
	s.Pending = kept
	s.mu.Unlock()
	return s.save()
}

// getPending returns a copy of the pending order.
func (s *State) getPending(orderID string) (Pending, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.Pending {
		if p.OrderID == orderID {
			return p, true
		}
	}
	return Pending{}, false
}

// updatePending modifies the pending order and saves. Returns false if it is no longer pending.
func (s *State) updatePending(orderID string, fn func(*Pending)) (bool, error) {
	s.mu.Lock()
	found := false
	for i := range s.Pending {
		if s.Pending[i].OrderID == orderID {
			fn(&s.Pending[i])
			found = true
			break
		}
	}
	s.mu.Unlock()
	if !found {
		return false, nil
	}
	return true, s.save()
}

func (s *State) setOffset(offset int64) error {
	s.mu.Lock()
	s.Offset = offset
	s.mu.Unlock()
	return s.save()
}

func (s *State) isPending(orderID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.Pending {
		if p.OrderID == orderID {
			return true
		}
	}
	return false
}

// findRecent looks for an order with the same fingerprint created after since.
func (s *State) findRecent(fingerprint string, since time.Time) (Recent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.Recent {
		if r.Fingerprint == fingerprint && r.CreatedAt.After(since) {
			return r, true
		}
	}
	return Recent{}, false
}

// addRecent records an order and drops the ones before since.
func (s *State) addRecent(r Recent, since time.Time) error {
	s.mu.Lock()
	kept := s.Recent[:0]
	for _, old := range s.Recent {
		if old.CreatedAt.After(since) {
			kept = append(kept, old)
		}
	}
	s.Recent = append(kept, r)
	s.mu.Unlock()
	return s.save()
}

func (s *State) snapshotPending() []Pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Pending, len(s.Pending))
	copy(out, s.Pending)
	return out
}

// markCOD saves a shipment's collection state. Returns false if it already had that state (already announced).
func (s *State) markCOD(id, status string) (bool, error) {
	s.mu.Lock()
	if s.CODNotices[id] == status {
		s.mu.Unlock()
		return false, nil
	}
	if s.CODNotices == nil {
		s.CODNotices = map[string]string{}
	}
	s.CODNotices[id] = status
	s.mu.Unlock()
	return true, s.save()
}

// markNotice records a notice for the day (YYYY-MM-DD). Returns false if it was already sent.
// Notices older than 30 days are forgotten so the file doesn't grow forever.
func (s *State) markNotice(key, day string) (bool, error) {
	s.mu.Lock()
	if _, ok := s.IssueNotices[key]; ok {
		s.mu.Unlock()
		return false, nil
	}
	if s.IssueNotices == nil {
		s.IssueNotices = map[string]string{}
	}
	if t, err := time.Parse("2006-01-02", day); err == nil {
		limit := t.AddDate(0, 0, -30).Format("2006-01-02")
		for k, d := range s.IssueNotices {
			if d < limit {
				delete(s.IssueNotices, k)
			}
		}
	}
	s.IssueNotices[key] = day
	s.mu.Unlock()
	return true, s.save()
}

// summaryDue reports whether the (daily or weekly) summary for day hasn't been sent yet, and marks it as sent.
func (s *State) summaryDue(weekly bool, day string) (bool, error) {
	s.mu.Lock()
	latest := &s.LastDailySummary
	if weekly {
		latest = &s.LastWeeklySummary
	}
	if *latest == day {
		s.mu.Unlock()
		return false, nil
	}
	*latest = day
	s.mu.Unlock()
	return true, s.save()
}
