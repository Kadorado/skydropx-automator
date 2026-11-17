package customers

import (
	"context"
	"sync"
	"time"
)

// memoryRepo keeps customers in a map. The mutex makes every write atomic, and the document check
// mirrors MongoDB's unique index.
type memoryRepo struct {
	mu    sync.Mutex
	byID  map[string]Customer
	since time.Time
}

func newMemoryRepo() *memoryRepo { return &memoryRepo{byID: map[string]Customer{}} }

func (m *memoryRepo) findByKeys(_ context.Context, documentID, phone string) (*Customer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if documentID != "" {
		for _, c := range m.byID {
			if c.DocumentID == documentID {
				return &c, nil
			}
		}
	}
	if phone != "" {
		for _, c := range m.byID {
			if c.Phone == phone && (documentID == "" || c.DocumentID == "") {
				return &c, nil
			}
		}
	}
	return nil, nil
}

func (m *memoryRepo) addOrder(_ context.Context, id string, in Customer, o Order, historical bool, now time.Time) (Customer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var existing *Customer
	if c, ok := m.byID[id]; ok {
		existing = &c
	}
	c := merge(existing, id, in, o, historical, now)
	if c.DocumentID != "" {
		for otherID, other := range m.byID {
			if otherID != id && other.DocumentID == c.DocumentID {
				return Customer{}, errDuplicate
			}
		}
	}
	m.byID[id] = c
	return c, nil
}

func (m *memoryRepo) update(id string, f func(*Customer)) (Customer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.byID[id]
	if !ok {
		return Customer{}, ErrNotFound
	}
	f(&c)
	m.byID[id] = c
	return c, nil
}

func (m *memoryRepo) setConsent(_ context.Context, id string, consent Consent, now time.Time) (Customer, error) {
	return m.update(id, func(c *Customer) { c.Consent, c.OptOutAt, c.UpdatedAt = consent, nil, now })
}

func (m *memoryRepo) setOptOut(_ context.Context, id string, now time.Time) (Customer, error) {
	return m.update(id, func(c *Customer) { c.OptOutAt, c.UpdatedAt = &now, now })
}

func (m *memoryRepo) insertIfMissing(_ context.Context, c Customer) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[c.ID]; ok {
		return false, nil
	}
	if c.DocumentID != "" {
		for _, other := range m.byID {
			if other.DocumentID == c.DocumentID {
				return false, errDuplicate
			}
		}
	}
	m.byID[c.ID] = c
	return true, nil
}

func (m *memoryRepo) restoreSince(_ context.Context, t time.Time) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.since.IsZero() || t.Before(m.since) {
		m.since = t
	}
	return m.since, nil
}

func (m *memoryRepo) all(_ context.Context) ([]Customer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Customer, 0, len(m.byID))
	for _, c := range m.byID {
		out = append(out, c)
	}
	return out, nil
}
