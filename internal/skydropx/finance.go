package skydropx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Límites para no pasarse del límite de 2 solicitudes por segundo en una sola carga del panel.
const (
	maxFinancePages         = 10 // páginas de movimientos y de cargos extra (20 por página)
	maxFinanceQuantityFetch = 40 // órdenes nuevas a consultar por carga; el resto queda en caché para la próxima
)

// Period es un rango de días en hora de Colombia, ambos incluidos. Desde vacío = desde el principio.
type Period struct {
	From time.Time // inicio del día "desde" (cero = sin límite)
	To   time.Time // fin del día "hasta"
}

// NewPeriod arma el rango desde fechas "2006-01-02". desde vacío no pone límite inicial; hasta vacío es hoy.
func NewPeriod(desde, hasta string) (Period, error) {
	var p Period
	if strings.TrimSpace(desde) != "" {
		d, err := time.ParseInLocation("2006-01-02", desde, colombia)
		if err != nil {
			return p, fmt.Errorf("fecha desde inválida: %s", desde)
		}
		p.From = d
	}
	end := time.Now().In(colombia)
	if strings.TrimSpace(hasta) != "" {
		h, err := time.ParseInLocation("2006-01-02", hasta, colombia)
		if err != nil {
			return p, fmt.Errorf("fecha hasta inválida: %s", hasta)
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

// Money es una cantidad de envíos y su suma en pesos.
type Money struct {
	Count  int     `json:"count"`
	Amount float64 `json:"amount"`
}

// Commission es lo que se le debe al vendedor por datáfono, según el estado del cobro.
type Commission struct {
	PerUnit       float64 `json:"per_unit"`
	PaidUnits     int     `json:"paid_units"`    // datáfonos de envíos ya cobrados: se le deben
	PaidAmount    float64 `json:"paid_amount"`   //
	PendingUnits  int     `json:"pending_units"` // datáfonos de envíos por cobrar: se le deberán al cobrarse
	PendingAmount float64 `json:"pending_amount"`
	Estimated     int     `json:"estimated"` // envíos sin cantidad conocida, contados como 1 datáfono
}

// Movement es un movimiento de la cuenta de Skydropx (recarga, cobro de guía, devolución…).
type Movement struct {
	Date     string  `json:"date"`
	Kind     string  `json:"kind"`     // deposit | withdrawal | refund
	Category string  `json:"category"` // recarga | guia | proteccion | devolucion | otro
	Amount   float64 `json:"amount"`
	Tracking string  `json:"tracking"`
	Comment  string  `json:"comment"`
	Status   string  `json:"status"`
}

// ExtraCharge es un cargo extra de un envío (sobrepeso, envío no recaudado…).
type ExtraCharge struct {
	Date     string  `json:"date"`
	Type     string  `json:"type"`
	Amount   float64 `json:"amount"`
	Tracking string  `json:"tracking"`
	Status   string  `json:"status"`
}

// Finance es el resumen de dinero de un periodo.
type Finance struct {
	Balance      *float64         `json:"balance"` // nil si no se pudo consultar
	Currency     string           `json:"currency"`
	BalanceError string           `json:"balance_error,omitempty"`
	Shipments    int              `json:"shipments"`
	Cancelled    int              `json:"cancelled"`
	Collected    Money            `json:"collected"`  // contra entrega ya recaudado por la transportadora
	Pending      Money            `json:"pending"`    // contra entrega por recaudar
	Failed       Money            `json:"failed"`     // contra entrega que no se pudo recaudar
	LabelCost    Money            `json:"label_cost"` // costo de las guías creadas en el periodo
	Spending     map[string]Money `json:"spending"`   // movimientos agrupados por categoría
	Extra        Money            `json:"extra"`
	Commission   Commission       `json:"commission"`
	Movements    []Movement       `json:"movements"`
	ExtraCharges []ExtraCharge    `json:"extra_charges"`
	Truncated    bool             `json:"truncated"` // hay más de 300 envíos: los más antiguos no entran
	Warnings     []string         `json:"warnings"`
}

// Finance arma el resumen de dinero del periodo: saldo, contra entrega, gastos, movimientos y comisión.
func (c *Client) Finance(ctx context.Context, p Period, perUnit float64) (Finance, error) {
	if c.isMock() {
		return Finance{}, ErrMockMode
	}
	list, err := c.ListShipments(ctx)
	if err != nil {
		return Finance{}, err
	}

	shipments := financeShipments(list, p)
	var orderIDs []string
	for _, s := range shipments {
		if s.OrderID != "" && !s.Cancelled {
			orderIDs = append(orderIDs, s.OrderID)
		}
	}
	qty, _ := c.orderQuantitiesUpTo(ctx, orderIDs, maxFinanceQuantityFetch)

	f := summarizeShipments(shipments, qty, perUnit)
	f.Truncated = list.Truncated

	if cr, err := c.Credits(ctx); err == nil {
		f.Balance, f.Currency = &cr.Balance, cr.Currency
	} else {
		f.BalanceError = err.Error()
	}

	if movs, err := c.movements(ctx, p); err == nil {
		f.Movements = movs
		for _, m := range movs {
			agg := f.Spending[m.Category]
			agg.Count++
			agg.Amount += m.Amount
			f.Spending[m.Category] = agg
		}
	} else {
		f.Warnings = append(f.Warnings, "No se pudieron consultar los movimientos: "+err.Error())
	}

	if extras, err := c.extraCharges(ctx, p); err == nil {
		f.ExtraCharges = extras
		for _, e := range extras {
			f.Extra.Count++
			f.Extra.Amount += e.Amount
		}
	} else {
		f.Warnings = append(f.Warnings, "No se pudieron consultar los cargos extra: "+err.Error())
	}
	return f, nil
}

// financeShipment es lo que el resumen necesita de cada envío.
type financeShipment struct {
	OrderID    string
	Cancelled  bool
	OnDelivery string  // pending | collected | failed | cancelled | vacío
	CODAmount  float64 // monto a cobrar al cliente
	Cost       float64 // costo de la guía
}

// financeShipments toma los envíos creados dentro del periodo.
func financeShipments(list ShipmentList, p Period) []financeShipment {
	var out []financeShipment
	for _, raw := range list.Data {
		var s struct {
			Attributes struct {
				CreatedAt        string          `json:"created_at"`
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
		out = append(out, financeShipment{
			OrderID:    s.Attributes.OrderID,
			Cancelled:  s.Attributes.WorkflowStatus == "cancelled",
			OnDelivery: s.Attributes.OnDeliveryStatus,
			CODAmount:  parseAmount(s.Attributes.OnDeliveryAmount),
			Cost:       parseAmount(s.Attributes.Total),
		})
	}
	return out
}

// summarizeShipments suma el contra entrega, el costo de las guías y la comisión del vendedor.
// qty trae los datáfonos de cada orden; los envíos sin dato cuentan como 1 y se marcan como estimados.
func summarizeShipments(shipments []financeShipment, qty map[string]int, perUnit float64) Finance {
	f := Finance{Spending: map[string]Money{}, Commission: Commission{PerUnit: perUnit}}
	for _, s := range shipments {
		f.Shipments++
		if s.Cancelled {
			f.Cancelled++
			continue
		}
		f.LabelCost.Count++
		f.LabelCost.Amount += s.Cost

		units, known := qty[s.OrderID]
		if !known || units < 1 {
			units = 1
		}
		switch s.OnDelivery {
		case "collected":
			f.Collected.Count++
			f.Collected.Amount += s.CODAmount
			f.Commission.PaidUnits += units
			if !known {
				f.Commission.Estimated++
			}
		case "pending":
			f.Pending.Count++
			f.Pending.Amount += s.CODAmount
			f.Commission.PendingUnits += units
			if !known {
				f.Commission.Estimated++
			}
		case "failed":
			f.Failed.Count++
			f.Failed.Amount += s.CODAmount
		}
	}
	f.Commission.PaidAmount = float64(f.Commission.PaidUnits) * perUnit
	f.Commission.PendingAmount = float64(f.Commission.PendingUnits) * perUnit
	return f
}

// movements trae los movimientos de la cuenta del periodo (GET /api/v1/transaction_stats), del más nuevo al más antiguo.
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
				Amount: parseAmount(a.Amount), Tracking: a.Tracking, Comment: a.Comments, Status: a.Status,
			})
		}
		if resp.Meta.NextPage == nil || len(resp.Data) == 0 {
			break
		}
	}
	return out, nil
}

// movementCategory agrupa un movimiento para el resumen de gastos.
func movementCategory(kind, typ, comments string) string {
	c := strings.ToLower(comments + " " + typ)
	switch {
	case strings.Contains(c, "protec") || strings.Contains(c, "seguro") || strings.Contains(c, "insurance"):
		return "proteccion"
	case kind == "deposit":
		return "recarga"
	case kind == "refund":
		return "devolucion"
	case kind == "withdrawal" && strings.Contains(c, "shipment"):
		return "guia"
	default:
		return "otro"
	}
}

// extraCharges trae los cargos extra detectados en el periodo (GET /api/v1/finance/extra-charges).
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
