package customers

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

// ErrNotFound is returned when no customer matches a document or phone.
var ErrNotFound = errors.New("customer not found")

// errDuplicate means the write would break uniqueness (another customer already has that document, or
// two writers created the same customer at once). RecordOrder resolves the customer again and retries.
var errDuplicate = errors.New("duplicate customer")

// repo is the storage behind Store: MongoDB in production, memory in tests. Every write is atomic on
// one customer: it changes only the fields it names, so concurrent writes never overwrite each other.
type repo interface {
	// findByKeys returns the customer with that document or, failing that, that phone (nil if none).
	findByKeys(ctx context.Context, documentID, phone string) (*Customer, error)
	// addOrder creates the customer with that ID or updates it with the contact data and the order,
	// following the rules of merge, and returns it as stored.
	addOrder(ctx context.Context, id string, in Customer, o Order, historical bool, now time.Time) (Customer, error)
	// setConsent records the authorization and clears a previous opt-out.
	setConsent(ctx context.Context, id string, c Consent, now time.Time) (Customer, error)
	// setOptOut records that the customer doesn't want more messages.
	setOptOut(ctx context.Context, id string, now time.Time) (Customer, error)
	all(ctx context.Context) ([]Customer, error)
}

// Store is the customer list.
type Store struct {
	repo  repo
	now   func() time.Time
	since time.Time // when the bot started recording orders: the migration only brings older ones
}

// NewMemoryStore is a Store that lives only in memory (for tests and local runs).
func NewMemoryStore() *Store { return &Store{repo: newMemoryRepo(), now: time.Now, since: time.Now()} }

// Since is when the bot started recording orders in the list.
func (s *Store) Since() time.Time { return s.since }

// errNoKeys means the customer can't be identified.
var errNoKeys = errors.New("the customer has neither document nor phone")

// RecordOrder saves a customer and one of their orders: creates the customer or updates the existing one
// (matched by document, then phone).
func (s *Store) RecordOrder(ctx context.Context, in Customer, o Order) (Customer, error) {
	c, _, err := s.record(ctx, in, o, false)
	return c, err
}

// record writes one order. existing is the customer as it was before (nil if new).
func (s *Store) record(ctx context.Context, in Customer, o Order, historical bool) (c Customer, existing *Customer, err error) {
	in.DocumentID, in.Phone = NormalizeDocument(in.DocumentID), NormalizePhone(in.Phone)
	if in.DocumentID == "" && in.Phone == "" {
		return Customer{}, nil, errNoKeys
	}
	for attempt := 0; ; attempt++ {
		existing, err = s.repo.findByKeys(ctx, in.DocumentID, in.Phone)
		if err != nil {
			return Customer{}, nil, err
		}
		id := newID(in.DocumentID, in.Phone)
		if existing != nil {
			id = existing.ID
		}
		c, err = s.repo.addOrder(ctx, id, in, o, historical, s.now())
		if !errors.Is(err, errDuplicate) || attempt == 2 {
			return c, existing, err
		}
	}
}

// SourceSkydropx is the source of the customers migrated from Skydropx.
const SourceSkydropx = "skydropx"

// MigrationResult summarizes a migration.
type MigrationResult struct {
	Shipments   int // shipments read
	New         int // customers created
	OrdersAdded int // orders added (new or existing customers)
	Repeated    int // orders already migrated before
	NoKeys      int // shipments without document nor phone
	AfterSince  int // shipments after Since: the bot already recorded them
}

// Migrate loads past customers from their shipments. Only shipments before Since are taken (later ones
// were recorded by the bot), nobody gets consent (they never gave it), data already stored isn't
// overwritten, and running it again doesn't duplicate anything.
func (s *Store) Migrate(ctx context.Context, in []Sighting) (MigrationResult, error) {
	var r MigrationResult
	// Newest first: when a customer appears several times, their most recent data fills the fields.
	sort.SliceStable(in, func(i, j int) bool { return in[i].Order.Date.After(in[j].Order.Date) })
	for _, sg := range in {
		r.Shipments++
		if !sg.Order.Date.Before(s.since) {
			r.AfterSince++
			continue
		}
		cust := sg.Customer
		cust.Consent, cust.Source = Consent{}, SourceSkydropx
		c, existing, err := s.record(ctx, cust, sg.Order, true)
		switch {
		case errors.Is(err, errNoKeys):
			r.NoKeys++
			continue
		case err != nil:
			return r, err
		}
		before := 0
		if existing == nil {
			r.New++
		} else {
			before = len(existing.Orders)
		}
		if len(c.Orders) > before {
			r.OrdersAdded++
		} else {
			r.Repeated++
		}
	}
	return r, nil
}

// Sighting is a customer seen in one past shipment.
type Sighting struct {
	Customer Customer
	Order    Order
}

// find looks a customer up by a document or phone typed by a person. The key is tried as a document
// first and then as a phone of any customer: a typed phone must find a customer that also has a document.
func (s *Store) find(ctx context.Context, key string) (Customer, error) {
	c, err := s.repo.findByKeys(ctx, NormalizeDocument(key), "")
	if err == nil && c == nil {
		c, err = s.repo.findByKeys(ctx, "", NormalizePhone(key))
	}
	if err != nil {
		return Customer{}, err
	}
	if c == nil {
		return Customer{}, ErrNotFound
	}
	return *c, nil
}

// Find returns the customer with that document or phone.
func (s *Store) Find(ctx context.Context, key string) (Customer, error) { return s.find(ctx, key) }

// GrantConsent records that the customer authorized the use of their data, and clears a previous opt-out.
func (s *Store) GrantConsent(ctx context.Context, key, evidence, recordedBy string) (Customer, error) {
	c, err := s.find(ctx, key)
	if err != nil {
		return c, err
	}
	now := s.now()
	return s.repo.setConsent(ctx, c.ID, Consent{Given: true, Date: now, Evidence: evidence, RecordedBy: recordedBy}, now)
}

// OptOut records that the customer asked not to be contacted again. The authorization stays as evidence
// of what happened, but Contactable becomes false.
func (s *Store) OptOut(ctx context.Context, key string) (Customer, error) {
	c, err := s.find(ctx, key)
	if err != nil {
		return c, err
	}
	return s.repo.setOptOut(ctx, c.ID, s.now())
}

// Stats summarizes the list.
type Stats struct {
	Customers   int
	Contactable int // authorized and not opted out
	OptedOut    int
	Repeat      int            // with more than one order
	Orders      int            // total orders
	ByCity      map[string]int // customers per city
	BySource    map[string]int // customers per source ("" = not given)
}

// All returns every customer, most recent buyers first.
func (s *Store) All(ctx context.Context) ([]Customer, error) {
	all, err := s.repo.all(ctx)
	sort.SliceStable(all, func(i, j int) bool { return all[i].LastOrderAt.After(all[j].LastOrderAt) })
	return all, err
}

// Summary computes the stats of a list.
func Summary(all []Customer) Stats {
	st := Stats{ByCity: map[string]int{}, BySource: map[string]int{}}
	for _, c := range all {
		st.Customers++
		st.Orders += len(c.Orders)
		if c.Contactable() {
			st.Contactable++
		}
		if c.OptOutAt != nil {
			st.OptedOut++
		}
		if len(c.Orders) > 1 {
			st.Repeat++
		}
		city := strings.TrimSpace(c.City)
		if city == "" {
			city = "Sin ciudad"
		}
		st.ByCity[city]++
		st.BySource[c.Source]++
	}
	return st
}
