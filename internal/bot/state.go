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

// Pending es una orden creada por el bot que todavía no tiene guía.
type Pending struct {
	OrderID   string    `json:"order_id"`
	ChatID    int64     `json:"chat_id"`
	Nombre    string    `json:"nombre"`
	Destino   string    `json:"destino"`
	CreatedAt time.Time `json:"created_at"`

	// Para generar la guía desde Telegram (vacíos en órdenes creadas antes de esta función).
	Sender         string               `json:"sender,omitempty"`
	Data           *domain.ShipmentData `json:"data,omitempty"`
	Quote          *skydropx.LabelQuote `json:"quote,omitempty"`
	LabelRequested bool                 `json:"label_requested,omitempty"`
}

// Recent es una orden creada hace poco, para detectar pedidos repetidos.
type Recent struct {
	Huella    string    `json:"huella"`
	OrderID   string    `json:"order_id"`
	Nombre    string    `json:"nombre"`
	CreatedAt time.Time `json:"created_at"`
}

// State se guarda en disco para no perder el progreso al reiniciar.
type State struct {
	Offset  int64     `json:"offset"`
	Pending []Pending `json:"pending"`
	Recent  []Recent  `json:"recent"`

	// Estado de cobro ya avisado por envío (collected | failed), para no repetir el aviso.
	Cobros map[string]string `json:"cobros,omitempty"`

	// Avisos de problemas de entrega ya enviados (envío:estado:día -> día), para no repetirlos.
	Avisos map[string]string `json:"avisos,omitempty"`

	// Último día (AAAA-MM-DD) en que se mandó cada resumen automático.
	UltimoResumenDiario  string `json:"ultimo_resumen_diario,omitempty"`
	UltimoResumenSemanal string `json:"ultimo_resumen_semanal,omitempty"`

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

// getPending devuelve una copia de la orden pendiente.
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

// updatePending modifica la orden pendiente y guarda. Devuelve false si ya no está pendiente.
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

// findRecent busca una orden con la misma huella creada después de since.
func (s *State) findRecent(huella string, since time.Time) (Recent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.Recent {
		if r.Huella == huella && r.CreatedAt.After(since) {
			return r, true
		}
	}
	return Recent{}, false
}

// addRecent registra una orden y descarta las anteriores a since.
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

// marcarCobro guarda el estado de cobro de un envío. Devuelve false si ya tenía ese estado (ya se avisó).
func (s *State) marcarCobro(id, status string) (bool, error) {
	s.mu.Lock()
	if s.Cobros[id] == status {
		s.mu.Unlock()
		return false, nil
	}
	if s.Cobros == nil {
		s.Cobros = map[string]string{}
	}
	s.Cobros[id] = status
	s.mu.Unlock()
	return true, s.save()
}

// marcarAviso registra un aviso del día (AAAA-MM-DD). Devuelve false si ya se había enviado.
// Los avisos de más de 30 días se olvidan para que el archivo no crezca sin fin.
func (s *State) marcarAviso(key, day string) (bool, error) {
	s.mu.Lock()
	if _, ok := s.Avisos[key]; ok {
		s.mu.Unlock()
		return false, nil
	}
	if s.Avisos == nil {
		s.Avisos = map[string]string{}
	}
	if t, err := time.Parse("2006-01-02", day); err == nil {
		limite := t.AddDate(0, 0, -30).Format("2006-01-02")
		for k, d := range s.Avisos {
			if d < limite {
				delete(s.Avisos, k)
			}
		}
	}
	s.Avisos[key] = day
	s.mu.Unlock()
	return true, s.save()
}

// resumenPendiente dice si el resumen (diario o semanal) de day todavía no se mandó, y lo marca como enviado.
func (s *State) resumenPendiente(semanal bool, day string) (bool, error) {
	s.mu.Lock()
	ultimo := &s.UltimoResumenDiario
	if semanal {
		ultimo = &s.UltimoResumenSemanal
	}
	if *ultimo == day {
		s.mu.Unlock()
		return false, nil
	}
	*ultimo = day
	s.mu.Unlock()
	return true, s.save()
}
