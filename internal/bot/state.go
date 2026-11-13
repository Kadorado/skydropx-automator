package bot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Pending es una orden creada por el bot que todavía no tiene guía.
type Pending struct {
	OrderID   string    `json:"order_id"`
	ChatID    int64     `json:"chat_id"`
	Nombre    string    `json:"nombre"`
	Destino   string    `json:"destino"`
	CreatedAt time.Time `json:"created_at"`
}

// State se guarda en disco para no perder el progreso al reiniciar.
type State struct {
	Offset  int64     `json:"offset"`
	Pending []Pending `json:"pending"`

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

// save escribe el estado de forma atómica (archivo temporal + renombrado).
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

func (s *State) setOffset(offset int64) error {
	s.mu.Lock()
	s.Offset = offset
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
