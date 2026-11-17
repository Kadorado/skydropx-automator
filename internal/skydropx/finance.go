package skydropx

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Limits to stay under the 2 requests per second limit in a single dashboard load.
const (
	maxFinancePages         = 10 // pages of movements and extra charges (20 per page)
	maxFinanceQuantityFetch = 40 // new orders to look up per load; the rest stays cached for the next one
)

// Period is a range of days in Colombia time, both inclusive. Empty from = from the beginning.
type Period struct {
	From time.Time // start of the "desde" day (zero = no limit)
	To   time.Time // end of the "hasta" day
}

// NewPeriod builds the range from "2006-01-02" dates. Empty desde sets no start limit; empty hasta is today.
func NewPeriod(from, until string) (Period, error) {
	var p Period
	if strings.TrimSpace(from) != "" {
		d, err := time.ParseInLocation("2006-01-02", from, colombia)
		if err != nil {
			return p, fmt.Errorf("fecha desde inválida: %s", from)
		}
		p.From = d
	}
	end := time.Now().In(colombia)
	if strings.TrimSpace(until) != "" {
		h, err := time.ParseInLocation("2006-01-02", until, colombia)
		if err != nil {
			return p, fmt.Errorf("fecha hasta inválida: %s", until)
		}
		end = h
	}
	y, m, d := end.Date()
	p.To = time.Date(y, m, d, 23, 59, 59, 0, colombia)
	if !p.From.IsZero() && p.From.After(p.To) {
		return p, fmt.Errorf("la fecha desde es posterior a la fecha hasta")
	}
	return p, nil
}

func (p Period) contains(t time.Time) bool {
	return (p.From.IsZero() || !t.Before(p.From)) && !t.After(p.To)
}

// Money is a number of shipments and their sum in pesos.
type Money struct {
	Count  int     `json:"count"`
	Amount float64 `json:"amount"`
}

// Commission is what is owed to the seller per terminal, according to the collection state.
type Commission struct {
	PerUnit       float64 `json:"per_unit"`
	PaidUnits     int     `json:"paid_units"`    // terminals of shipments already collected: owed
	PaidAmount    float64 `json:"paid_amount"`   //
	PendingUnits  int     `json:"pending_units"` // terminals of shipments to collect: owed once collected
	PendingAmount float64 `json:"pending_amount"`
	Estimated     int     `json:"estimated"` // shipments with unknown quantity, counted as 1 terminal
}

// Movement is a Skydropx account movement (top-up, label charge, refund…).
type Movement struct {
	Date     string  `json:"date"`
	Kind     string  `json:"kind"`     // deposit | withdrawal | refund
	Category string  `json:"category"` // recarga | guia | proteccion | servicio | fee_recarga | devolucion | otro
	Amount   float64 `json:"amount"`
	Tracking string  `json:"tracking"`
	Comment  string  `json:"comment"`
	Status   string  `json:"status"`
	Related  string  `json:"related"` // type of what caused the movement (Shipment, Order…)
}

// ExtraCharge is an extra charge on a shipment (overweight, uncollected shipment…).
type ExtraCharge struct {
	Date     string  `json:"date"`
	Type     string  `json:"type"`
	Amount   float64 `json:"amount"`
	Tracking string  `json:"tracking"`
	Status   string  `json:"status"`
}

// ShipmentMoney is a shipment's account: what the label cost, what the customer pays
// and what went back to the credits with the same tracking number.
type ShipmentMoney struct {
	Date         string             `json:"date"`
	Tracking     string             `json:"tracking"`
	Carrier      string             `json:"carrier"`
	Customer     string             `json:"customer"`
	Status       string             `json:"status"`        // pending | collected | failed | cancelled | empty
	LabelCost    float64            `json:"label_cost"`    // label cost according to the shipment (what is added to the customer)
	Charged      float64            `json:"charged"`       // everything deducted from the credits with this label
	ChargedParts map[string]float64 `json:"charged_parts"` // what was deducted per concept: guia, proteccion, servicio, otro
	CustomerPays float64            `json:"customer_pays"` // what the carrier charges the customer (product + shipping)
	Product      float64            `json:"product"`       // product value according to the order (0 if unknown)
	Returned     float64            `json:"returned"`      // credits and refunds to the credits with this label
	ReturnedAt   string             `json:"returned_at"`   // date of the last credit or refund
	Extra        float64            `json:"extra"`         // extra charges on this label
	Units        int                `json:"units"`         // terminals in the order (1 if unknown)
	Commission   float64            `json:"commission"`    // seller's commission for this shipment, at its day's rate
}

// Finance is the money summary of a period.
type Finance struct {
	Balance      *float64         `json:"balance"` // nil if it couldn't be looked up
	Currency     string           `json:"currency"`
	BalanceError string           `json:"balance_error,omitempty"`
	Shipments    int              `json:"shipments"`
	Cancelled    int              `json:"cancelled"`
	Collected    Money            `json:"collected"`  // cash on delivery already collected by the carrier
	Pending      Money            `json:"pending"`    // cash on delivery to collect
	Failed       Money            `json:"failed"`     // cash on delivery that couldn't be collected
	LabelCost    Money            `json:"label_cost"` // cost of the labels created in the period
	Spending     map[string]Money `json:"spending"`   // movements grouped by category
	Extra        Money            `json:"extra"`
	Commission   Commission       `json:"commission"`
	Movements    []Movement       `json:"movements"`
	ExtraCharges []ExtraCharge    `json:"extra_charges"`
	PerShipment  []ShipmentMoney  `json:"per_shipment"` // each shipment's account in the period, newest first
	Returned     Money            `json:"returned"`     // what went back to the credits for the period's shipments
	Truncated    bool             `json:"truncated"`    // there are more than 300 shipments: the oldest ones are left out
	Warnings     []string         `json:"warnings"`
}

// Finance builds the period's money summary: balance, cash on delivery, expenses, movements and commission.
// CommissionRate gives the per-terminal commission in effect on a date: if it changes, each sale uses its day's.
type CommissionRate func(t time.Time) float64

// FlatCommission is a commission that doesn't change with the date.
func FlatCommission(v float64) CommissionRate { return func(time.Time) float64 { return v } }

func (c *Client) Finance(ctx context.Context, p Period, rate CommissionRate) (Finance, error) {
	return c.finance(ctx, p, rate, maxFinanceQuantityFetch)
}

// FullFinance is Finance looking up the quantity of every order in the period, without a cap:
// it takes longer (2 requests per second) but the commission isn't estimated. The context cuts it off.
func (c *Client) FullFinance(ctx context.Context, p Period, rate CommissionRate) (Finance, error) {
	return c.finance(ctx, p, rate, math.MaxInt)
}

func (c *Client) finance(ctx context.Context, p Period, rate CommissionRate, maxLookups int) (Finance, error) {
	if c.isMock() {
		return Finance{}, ErrMockMode
	}
	list, err := c.ListShipments(ctx)
	if err != nil {
		return Finance{}, err
	}

	shipments := withLabel(financeShipments(list, p))
	var orderIDs []string
	for _, s := range shipments {
		if s.OrderID != "" && !s.Cancelled {
			orderIDs = append(orderIDs, s.OrderID)
		}
	}
	qty, _ := c.orderQuantitiesUpTo(ctx, orderIDs, maxLookups)
	products := orderProducts(orderIDs)
	for i := range shipments {
		s := &shipments[i]
		s.Product = products[s.OrderID]
		if !s.Cancelled {
			s.Units, _ = orderUnits(qty, s.OrderID)
			s.Commission = float64(s.Units) * rate(s.createdAt())
		}
	}

	f := summarizeShipments(shipments, qty, rate)
	f.Truncated = list.Truncated

	if cr, err := c.Credits(ctx); err == nil {
		f.Balance, f.Currency = &cr.Balance, cr.Currency
	} else {
		f.BalanceError = err.Error()
	}

	// Movements and charges are fetched up to today: a refund may arrive days after the shipment.
	// The lists and the expense summary only show the period's.
	untilNow := Period{From: p.From, To: time.Now().In(colombia)}
	if untilNow.To.Before(p.To) {
		untilNow.To = p.To
	}
	var allMovs []Movement
	var allExtras []ExtraCharge
	if movs, err := c.movements(ctx, untilNow); err == nil {
		allMovs = movs
		for _, m := range movs {
			if !p.containsDate(m.Date) {
				continue
			}
			f.Movements = append(f.Movements, m)
			agg := f.Spending[m.Category]
			agg.Count++
			agg.Amount += m.Amount
			f.Spending[m.Category] = agg
		}
	} else {
		f.Warnings = append(f.Warnings, "No se pudieron consultar los movimientos: "+err.Error())
	}

	if extras, err := c.extraCharges(ctx, untilNow); err == nil {
		allExtras = extras
		for _, e := range extras {
			if !p.containsDate(e.Date) {
				continue
			}
			f.ExtraCharges = append(f.ExtraCharges, e)
			f.Extra.Count++
			f.Extra.Amount += e.Amount
		}
	} else {
		f.Warnings = append(f.Warnings, "No se pudieron consultar los cargos extra: "+err.Error())
	}
	f.PerShipment, f.Returned = perShipment(shipments, allMovs, allExtras)
	return f, nil
}

// containsDate reports whether a Skydropx date (with time or day only) falls in the period.
// Dates that can't be parsed count as inside, so no movements are hidden.
func (p Period) containsDate(s string) bool {
	if t, ok := parseTime(s); ok {
		return p.contains(t.In(colombia))
	}
	if d, err := time.ParseInLocation("2006-01-02", s, colombia); err == nil {
		return p.contains(d)
	}
	return true
}

// perShipment builds each shipment's account by gathering, per tracking number, the credits and refunds
// to the credits and the extra charges.
func perShipment(shipments []financeShipment, movs []Movement, extras []ExtraCharge) ([]ShipmentMoney, Money) {
	type back struct {
		amount float64
		last   string
	}
	returned := map[string]back{}
	charged := map[string]map[string]float64{}
	for _, m := range movs {
		if m.Tracking == "" {
			continue
		}
		if m.Kind == "withdrawal" {
			if charged[m.Tracking] == nil {
				charged[m.Tracking] = map[string]float64{}
			}
			charged[m.Tracking][m.Category] += m.Amount
			continue
		}
		if m.Kind != "refund" && m.Kind != "deposit" {
			continue
		}
		b := returned[m.Tracking]
		b.amount += m.Amount
		if m.Date > b.last {
			b.last = m.Date
		}
		returned[m.Tracking] = b
	}
	extra := map[string]float64{}
	for _, e := range extras {
		if e.Tracking != "" {
			extra[e.Tracking] += e.Amount
		}
	}

	var total Money
	out := make([]ShipmentMoney, 0, len(shipments))
	for _, s := range shipments {
		status := s.OnDelivery
		if s.Cancelled {
			status = "cancelled"
		}
		row := ShipmentMoney{
			Date: s.Date, Tracking: s.Tracking, Carrier: s.Carrier, Customer: s.Customer, Status: status,
			LabelCost: s.Cost, CustomerPays: s.customerPays(), Product: s.Product,
			Units: s.Units, Commission: s.Commission,
		}
		if s.Tracking != "" {
			b := returned[s.Tracking]
			row.Returned, row.ReturnedAt = b.amount, b.last
			row.Extra = extra[s.Tracking]
			row.ChargedParts = charged[s.Tracking]
			for _, v := range row.ChargedParts {
				row.Charged += v
			}
		}
		if row.Returned > 0 {
			total.Count++
			total.Amount += row.Returned
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date > out[j].Date })
	return out, total
}

// customerPays is what the carrier charges the customer: the product plus shipping.
// Skydropx sometimes reports cash on delivery already including shipping (60.000 + 12.573 = 72.573) and sometimes
// only the product (120.000): if the product is known, the larger of the reported amount and
// product + shipping is used. Without the product, the reported amount is trusted.
func (s financeShipment) customerPays() float64 {
	if s.Product > 0 && s.Product+s.Cost > s.CODAmount {
		return s.Product + s.Cost
	}
	return s.CODAmount
}

// withLabel leaves out shipments without a tracking number: failed attempts that were never generated or paid.
func withLabel(shipments []financeShipment) []financeShipment {
	out := shipments[:0:0]
	for _, s := range shipments {
		if !s.NoLabel {
			out = append(out, s)
		}
	}
	return out
}

// financeShipment is what the summary needs from each shipment.
type financeShipment struct {
	ID         string
	Date       string
	Tracking   string
	Carrier    string
	Customer   string
	Phone      string
	City       string
	TaxID      string // recipient's ID document
	OrderID    string
	Cancelled  bool
	NoLabel    bool    // has no tracking number: failed or wasn't generated, wasn't paid
	OnDelivery string  // pending | collected | failed | cancelled | empty
	CODAmount  float64 // cash-on-delivery amount reported by Skydropx: sometimes includes shipping and sometimes not
	Product    float64 // product value according to the order (0 if unknown)
	Cost       float64 // label cost
	Units      int     // terminals in the order (1 if unknown)
	Commission float64 // seller's commission for this shipment
}

// createdAt is the shipment's creation date; if it can't be parsed, now.
func (s financeShipment) createdAt() time.Time {
	if t, ok := parseTime(s.Date); ok {
		return t
	}
	return time.Now()
}

// orderUnits are the order's terminals; with no data (known false) or no quantity it counts as 1.
func orderUnits(qty map[string]int, orderID string) (units int, known bool) {
	units, known = qty[orderID]
	if !known || units < 1 {
		return 1, known
	}
	return units, true
}

// financeShipments takes the shipments created within the period.
func financeShipments(list ShipmentList, p Period) []financeShipment {
	type contact struct{ name, phone, city, taxID string }
	contacts := map[string]contact{} // address id -> recipient data
	for _, raw := range list.Included {
		var inc struct {
			ID         string `json:"id"`
			Type       string `json:"type"`
			Attributes struct {
				Name  string `json:"name"`
				Phone string `json:"phone"`
				City  string `json:"area_level2"`
				TaxID string `json:"tax_id_number"`
			} `json:"attributes"`
		}
		if json.Unmarshal(raw, &inc) == nil && inc.Type == "address" {
			contacts[inc.ID] = contact{inc.Attributes.Name, inc.Attributes.Phone, inc.Attributes.City, inc.Attributes.TaxID}
		}
	}
	var out []financeShipment
	for _, raw := range list.Data {
		var s struct {
			ID            string `json:"id"`
			Relationships struct {
				AddressTo struct {
					Data *struct {
						ID string `json:"id"`
					} `json:"data"`
				} `json:"address_to"`
			} `json:"relationships"`
			Attributes struct {
				CreatedAt        string          `json:"created_at"`
				Tracking         string          `json:"master_tracking_number"`
				Carrier          string          `json:"carrier_name"`
				OrderID          string          `json:"order_id"`
				WorkflowStatus   string          `json:"workflow_status"`
				OnDeliveryStatus string          `json:"on_delivery_status"`
				OnDeliveryAmount json.RawMessage `json:"on_delivery_amount"`
				Total            json.RawMessage `json:"total"`
			} `json:"attributes"`
		}
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		created, ok := parseTime(s.Attributes.CreatedAt)
		if !ok || !p.contains(created.In(colombia)) {
			continue
		}
		var to contact
		if ref := s.Relationships.AddressTo.Data; ref != nil {
			to = contacts[ref.ID]
		}
		out = append(out, financeShipment{
			ID:         s.ID,
			Date:       s.Attributes.CreatedAt,
			Tracking:   s.Attributes.Tracking,
			Carrier:    s.Attributes.Carrier,
			Customer:   to.name,
			Phone:      to.phone,
			City:       to.city,
			TaxID:      to.taxID,
			OrderID:    s.Attributes.OrderID,
			Cancelled:  s.Attributes.WorkflowStatus == "cancelled",
			NoLabel:    strings.TrimSpace(s.Attributes.Tracking) == "",
			OnDelivery: s.Attributes.OnDeliveryStatus,
			CODAmount:  parseAmount(s.Attributes.OnDeliveryAmount),
			Cost:       parseAmount(s.Attributes.Total),
		})
	}
	return out
}

// summarizeShipments adds up cash on delivery, label cost and the seller's commission.
// qty has each order's terminals; shipments without data count as 1 and are flagged as estimated.
// Each shipment uses the commission in effect on the day it was created.
func summarizeShipments(shipments []financeShipment, qty map[string]int, rate CommissionRate) Finance {
	now := time.Now()
	f := Finance{Spending: map[string]Money{}, Commission: Commission{PerUnit: rate(now)}}
	for _, s := range shipments {
		f.Shipments++
		if s.Cancelled {
			f.Cancelled++
			continue
		}
		f.LabelCost.Count++
		f.LabelCost.Amount += s.Cost

		units, known := orderUnits(qty, s.OrderID)
		perUnit := rate(s.createdAt())
		switch s.OnDelivery {
		case "collected":
			f.Collected.Count++
			f.Collected.Amount += s.customerPays()
			f.Commission.PaidUnits += units
			f.Commission.PaidAmount += float64(units) * perUnit
			if !known {
				f.Commission.Estimated++
			}
		case "pending":
			f.Pending.Count++
			f.Pending.Amount += s.customerPays()
			f.Commission.PendingUnits += units
			f.Commission.PendingAmount += float64(units) * perUnit
			if !known {
				f.Commission.Estimated++
			}
		case "failed":
			f.Failed.Count++
			f.Failed.Amount += s.customerPays()
		}
	}
	return f
}

// movements fetches the account's movements for the period (GET /api/v1/transaction_stats), newest first.
func (c *Client) movements(ctx context.Context, p Period) ([]Movement, error) {
	var out []Movement
	for page := 1; page <= maxFinancePages; page++ {
		q := url.Values{}
		q.Set("page", fmt.Sprint(page))
		q.Set("sort", "desc")
		q.Set("kind", "all")
		if !p.From.IsZero() {
			q.Set("date_from", p.From.Format("2006-01-02"))
		}
		q.Set("date_to", p.To.Format("2006-01-02"))
		body, status, err := c.get(ctx, c.cfg.SkydropxBaseURL+"/api/v1/transaction_stats?"+q.Encode())
		if err != nil {
			return nil, err
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("Skydropx respondió HTTP %d", status)
		}
		var resp struct {
			Data []struct {
				Attributes struct {
					Kind     string          `json:"transaction_kind"`
					Amount   json.RawMessage `json:"transaction_amount"`
					Status   string          `json:"transaction_status"`
					Comments string          `json:"transaction_comments"`
					Created  string          `json:"transaction_created_at"`
					Type     string          `json:"transactionable_type"`
					Tracking string          `json:"shipment_tracking_number"`
				} `json:"attributes"`
			} `json:"data"`
			Meta struct {
				NextPage *int `json:"next_page"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("respuesta inesperada")
		}
		for _, d := range resp.Data {
			a := d.Attributes
			out = append(out, Movement{
				Date: a.Created, Kind: a.Kind, Category: movementCategory(a.Kind, a.Type, a.Comments),
				Amount: parseAmount(a.Amount), Tracking: a.Tracking, Comment: a.Comments, Status: a.Status, Related: a.Type,
			})
		}
		if resp.Meta.NextPage == nil || len(resp.Data) == 0 {
			break
		}
	}
	return out, nil
}

// movementCategory groups a movement for the expense summary.
func movementCategory(kind, typ, comments string) string {
	c := strings.ToLower(comments + " " + typ)
	switch {
	case kind == "refund": // refunding a protection or a fee isn't an expense
		return "devolucion"
	case strings.Contains(c, "protec") || strings.Contains(c, "seguro") || strings.Contains(c, "insurance"):
		return "proteccion"
	case strings.Contains(c, "servicefee") || strings.Contains(c, "service fee"):
		return "servicio"
	case strings.Contains(c, "mercado pago") || strings.Contains(c, "mercadopago"):
		return "fee_recarga"
	case kind == "deposit":
		return "recarga"
	case kind == "withdrawal" && strings.Contains(c, "shipment"):
		return "guia"
	default:
		return "otro"
	}
}

// extraCharges fetches the extra charges detected in the period (GET /api/v1/finance/extra-charges).
func (c *Client) extraCharges(ctx context.Context, p Period) ([]ExtraCharge, error) {
	var out []ExtraCharge
	for page := 1; page <= maxFinancePages; page++ {
		q := url.Values{}
		q.Set("page", fmt.Sprint(page))
		q.Set("per_page", "20")
		if !p.From.IsZero() {
			q.Set("start_date", p.From.Format("2006-01-02"))
		}
		q.Set("end_date", p.To.Format("2006-01-02"))
		body, status, err := c.get(ctx, c.cfg.SkydropxBaseURL+"/api/v1/finance/extra-charges?"+q.Encode())
		if err != nil {
			return nil, err
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("Skydropx respondió HTTP %d", status)
		}
		var resp struct {
			Data []struct {
				Status    string          `json:"status"`
				Tracking  string          `json:"tracking_number"`
				Detection string          `json:"detection_date"`
				Amount    json.RawMessage `json:"amount"`
				Type      string          `json:"charge_type"`
			} `json:"data"`
			Meta struct {
				NextPage *int `json:"next_page"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("respuesta inesperada")
		}
		for _, d := range resp.Data {
			out = append(out, ExtraCharge{Date: d.Detection, Type: strings.TrimPrefix(d.Type, "ExtraCharge::"),
				Amount: parseAmount(d.Amount), Tracking: d.Tracking, Status: d.Status})
		}
		if resp.Meta.NextPage == nil || len(resp.Data) == 0 {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date > out[j].Date })
	return out, nil
}
