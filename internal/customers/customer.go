// Package customers keeps the customer list: everyone who has bought, with their orders and whether
// they authorized the use of their data for marketing (Colombian Law 1581 of 2012).
package customers

import (
	"strings"
	"time"
	"unicode"
)

// Consent is the customer's authorization to use their data for marketing.
type Consent struct {
	Given      bool      `bson:"given" json:"given"`
	Date       time.Time `bson:"date,omitempty" json:"date,omitempty"`
	Evidence   string    `bson:"evidence,omitempty" json:"evidence,omitempty"`       // how it was given, e.g. what the customer answered
	RecordedBy string    `bson:"recorded_by,omitempty" json:"recorded_by,omitempty"` // who registered it
}

// Order is one purchase of the customer.
type Order struct {
	Date      time.Time `bson:"date" json:"date"`
	Units     int       `bson:"units,omitempty" json:"units,omitempty"`
	UnitPrice float64   `bson:"unit_price,omitempty" json:"unit_price,omitempty"`
	CreatedBy string    `bson:"created_by,omitempty" json:"created_by,omitempty"`
}

// Customer is one person, identified by their ID document or, if there is none, by their phone.
type Customer struct {
	ID           string     `bson:"_id" json:"id"`
	DocumentID   string     `bson:"document_id,omitempty" json:"document_id,omitempty"` // digits only
	Name         string     `bson:"name" json:"name"`
	Phone        string     `bson:"phone,omitempty" json:"phone,omitempty"` // last 10 digits
	Email        string     `bson:"email,omitempty" json:"email,omitempty"`
	Department   string     `bson:"department,omitempty" json:"department,omitempty"`
	City         string     `bson:"city,omitempty" json:"city,omitempty"`
	Neighborhood string     `bson:"neighborhood,omitempty" json:"neighborhood,omitempty"`
	Address      string     `bson:"address,omitempty" json:"address,omitempty"`
	Consent      Consent    `bson:"consent" json:"consent"`
	OptOutAt     *time.Time `bson:"opt_out_at,omitempty" json:"opt_out_at,omitempty"` // asked not to be contacted
	Orders       []Order    `bson:"orders" json:"orders"`
	FirstOrderAt time.Time  `bson:"first_order_at" json:"first_order_at"`
	LastOrderAt  time.Time  `bson:"last_order_at" json:"last_order_at"`
	Source       string     `bson:"source,omitempty" json:"source,omitempty"` // where the customer came from (facebook, referido…), lowercase
	UpdatedAt    time.Time  `bson:"updated_at" json:"updated_at"`
}

// Contactable reports whether the customer may receive marketing: authorized and didn't opt out.
func (c Customer) Contactable() bool { return c.Consent.Given && c.OptOutAt == nil }

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NormalizeDocument keeps the digits of an ID document ("" if too short to be one).
func NormalizeDocument(s string) string {
	if d := digits(s); len(d) >= 5 {
		return d
	}
	return ""
}

// NormalizePhone keeps the last 10 digits of a phone ("" if it has fewer than 7).
func NormalizePhone(s string) string {
	d := digits(s)
	if len(d) > 10 {
		d = d[len(d)-10:]
	}
	if len(d) < 7 {
		return ""
	}
	return d
}

// newID builds the ID of a new customer from their document, or their phone if there is no document.
func newID(documentID, phone string) string {
	if documentID != "" {
		return "cc:" + documentID
	}
	return "tel:" + phone
}

// merge folds an order and the contact data that came with it into the stored customer (existing may be
// nil). It is the reference for what addOrder does atomically in MongoDB:
//   - non-empty contact data overwrites the stored one (it comes from the newest order); empty fields
//     never erase anything. A historical order (migrated from Skydropx) only fills empty fields, because
//     it is older than what is stored, and it isn't added twice: same date and time, same order,
//   - consent is only granted here, never revoked (revoking is opt-out),
//   - the source is the first one known: where the customer originally came from,
//   - the order is appended and the first/last order dates are widened.
func merge(existing *Customer, id string, in Customer, o Order, historical bool, now time.Time) Customer {
	c := Customer{ID: id}
	if existing != nil {
		c = *existing
		c.Orders = append([]Order(nil), existing.Orders...)
	}
	for _, f := range contactFields(&c, in) {
		if f.value != "" && (!historical || *f.dst == "") {
			*f.dst = f.value
		}
	}
	if src := normalizeSource(in.Source); c.Source == "" {
		c.Source = src
	}
	if in.Consent.Given && !c.Consent.Given {
		c.Consent = in.Consent
	}
	if !historical || !hasOrderAt(c.Orders, o.Date) {
		c.Orders = append(c.Orders, o)
	}
	if c.FirstOrderAt.IsZero() || o.Date.Before(c.FirstOrderAt) {
		c.FirstOrderAt = o.Date
	}
	if o.Date.After(c.LastOrderAt) {
		c.LastOrderAt = o.Date
	}
	c.UpdatedAt = now
	return c
}

// contactField pairs a stored field (and its name in MongoDB) with the incoming value.
type contactField struct {
	bson  string
	dst   *string
	value string
}

func contactFields(c *Customer, in Customer) []contactField {
	f := []contactField{
		{"document_id", &c.DocumentID, in.DocumentID}, {"name", &c.Name, in.Name}, {"phone", &c.Phone, in.Phone},
		{"email", &c.Email, in.Email}, {"department", &c.Department, in.Department}, {"city", &c.City, in.City},
		{"neighborhood", &c.Neighborhood, in.Neighborhood}, {"address", &c.Address, in.Address},
	}
	for i := range f {
		f[i].value = strings.TrimSpace(f[i].value)
	}
	return f
}

func hasOrderAt(orders []Order, t time.Time) bool {
	for _, o := range orders {
		if o.Date.Equal(t) {
			return true
		}
	}
	return false
}

func normalizeSource(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
