package skydropx

import (
	"context"
	"strings"
	"time"
)

// CODShipment is the cash-on-delivery collection state of a shipment with a label.
type CODShipment struct {
	ID       string
	Date     string // label creation (RFC 3339)
	Tracking string
	Carrier  string
	Customer string
	Phone    string
	City     string
	TaxID    string // recipient's ID document
	OrderID  string
	Status   string  // pending | collected | failed | cancelled | empty
	Reported float64 // cash-on-delivery amount reported by Skydropx (sometimes with shipping, sometimes without)
	Cost     float64 // label cost
}

// CODShipments returns the collection state of every shipment with a label in the account.
func (c *Client) CODShipments(ctx context.Context) ([]CODShipment, error) {
	if c.isMock() {
		return nil, ErrMockMode
	}
	list, err := c.ListShipments(ctx)
	if err != nil {
		return nil, err
	}
	all := Period{To: time.Now().In(colombia).Add(24 * time.Hour)}
	var out []CODShipment
	for _, s := range withLabel(financeShipments(list, all)) {
		status := s.OnDelivery
		if s.Cancelled {
			status = "cancelled"
		}
		out = append(out, CODShipment{
			ID: s.ID, Date: s.Date, Tracking: s.Tracking, Carrier: s.Carrier, Customer: s.Customer, Phone: s.Phone, City: s.City,
			TaxID: s.TaxID, OrderID: s.OrderID,
			Status: status, Reported: s.CODAmount, Cost: s.Cost,
		})
	}
	return out, nil
}

// CODAmounts returns what the carrier charges the customer and the product's value
// (0 if the order couldn't be looked up), with the same math as Finanzas.
func (c *Client) CODAmounts(ctx context.Context, s CODShipment) (pays, product float64) {
	if s.OrderID != "" {
		_, _ = c.orderQuantitiesUpTo(ctx, []string{s.OrderID}, 1)
		product = orderProducts([]string{s.OrderID})[s.OrderID]
	}
	fs := financeShipment{CODAmount: s.Reported, Cost: s.Cost, Product: product}
	return fs.customerPays(), product
}

// Problem is an open shipment that needs follow-up with the customer.
type Problem struct {
	CODShipment
	Kind      string // devolucion | intento | oficina | novedad
	LastEvent string // last tracking event
	LastDate  string
	History   []TrackEvent // latest events, newest first
}

// Problems checks shipments with a label created since since that haven't been delivered or collected and returns
// the ones being returned or not collected, with a failed delivery attempt, at the office or with an incident.
// It looks up each one's tracking (cached and respecting the API limit).
func (c *Client) Problems(ctx context.Context, since time.Time) ([]Problem, int, error) {
	if c.isMock() {
		return nil, 0, ErrMockMode
	}
	list, err := c.ListShipments(ctx)
	if err != nil {
		return nil, 0, err
	}
	p := Period{From: since, To: time.Now().In(colombia).Add(24 * time.Hour)}
	statusMu.Lock()
	defer statusMu.Unlock()
	var out []Problem
	checked := 0
	for _, s := range withLabel(financeShipments(list, p)) {
		if s.Cancelled || s.OnDelivery == "collected" {
			continue
		}
		pr := Problem{CODShipment: CODShipment{
			ID: s.ID, Date: s.Date, Tracking: s.Tracking, Carrier: s.Carrier, Customer: s.Customer, Phone: s.Phone, City: s.City,
			TaxID: s.TaxID, OrderID: s.OrderID,
			Status: s.OnDelivery, Reported: s.CODAmount, Cost: s.Cost,
		}}
		checked++
		st, err := c.trackingStatus(ctx, s.Tracking, s.Carrier)
		if err != nil && ctx.Err() != nil {
			return out, checked, ctx.Err()
		}
		pr.LastEvent, pr.LastDate, pr.History = st.LastEvent, st.LastDate, st.History
		pr.Kind = problemKind(s.OnDelivery, st.Raw, st.LastEvent)
		if pr.Kind != "" {
			out = append(out, pr)
		}
	}
	return out, checked, nil
}

// PendingCODState says where a shipment to collect is according to its tracking: "oficina" (to pick up),
// "novedad" (failed attempt, incident or return) or "camino". If it can't be tracked, "camino".
func (c *Client) PendingCODState(ctx context.Context, tracking, carrier string) string {
	if c.isMock() || tracking == "" {
		return "camino"
	}
	statusMu.Lock()
	defer statusMu.Unlock()
	st, err := c.trackingStatus(ctx, tracking, carrier)
	if err != nil {
		return "camino"
	}
	switch problemKind("pending", st.Raw, st.LastEvent) {
	case "oficina":
		return "oficina"
	case "":
		return "camino"
	default:
		return "novedad"
	}
}

// problemKind classifies a shipment by its collection and last tracking event ("" if it's fine).
func problemKind(onDelivery, raw, lastEvent string) string {
	switch {
	case raw == "delivered":
		return ""
	case onDelivery == "failed" || raw == "in_return" || strings.Contains(strings.ToLower(lastEvent), "devoluci"):
		return "devolucion"
	case raw == "delivery_attempt":
		return "intento"
	case raw == "delivered_to_branch":
		return "oficina"
	case raw == "exception":
		return "novedad"
	}
	return ""
}
