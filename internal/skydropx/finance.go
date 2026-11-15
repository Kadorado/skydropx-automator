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
	Category string  `json:"category"` // recarga | guia | proteccion | servicio | fee_recarga | devolucion | otro
	Amount   float64 `json:"amount"`
	Tracking string  `json:"tracking"`
	Comment  string  `json:"comment"`
	Status   string  `json:"status"`
	Related  string  `json:"related"` // tipo de lo que originó el movimiento (Shipment, Order…)
}

// ExtraCharge es un cargo extra de un envío (sobrepeso, envío no recaudado…).
type ExtraCharge struct {
	Date     string  `json:"date"`
	Type     string  `json:"type"`
	Amount   float64 `json:"amount"`
	Tracking string  `json:"tracking"`
	Status   string  `json:"status"`
}

// ShipmentMoney es la cuenta de un envío: lo que costó la guía, lo que paga el cliente
// y lo que volvió a los créditos con el mismo número de guía.
type ShipmentMoney struct {
	Date         string             `json:"date"`
	Tracking     string             `json:"tracking"`
	Customer     string             `json:"customer"`
	Status       string             `json:"status"`        // pending | collected | failed | cancelled | vacío
	LabelCost    float64            `json:"label_cost"`    // costo de la guía según el envío (lo que se le suma al cliente)
	Charged      float64            `json:"charged"`       // todo lo descontado de los créditos con esta guía
	ChargedParts map[string]float64 `json:"charged_parts"` // lo descontado por concepto: guia, proteccion, servicio, otro
	CustomerPays float64            `json:"customer_pays"` // lo que la transportadora le cobra al cliente (producto + envío)
	Product      float64            `json:"product"`       // valor del producto según la orden (0 si no se conoce)
	Returned     float64            `json:"returned"`      // abonos y devoluciones a los créditos con esta guía
	ReturnedAt   string             `json:"returned_at"`   // fecha del último abono o devolución
	Extra        float64            `json:"extra"`         // cargos extra de esta guía
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
	PerShipment  []ShipmentMoney  `json:"per_shipment"` // la cuenta de cada envío del periodo, del más nuevo al más antiguo
	Returned     Money            `json:"returned"`     // lo que volvió a los créditos por los envíos del periodo
	Truncated    bool             `json:"truncated"`    // hay más de 300 envíos: los más antiguos no entran
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

	shipments := withLabel(financeShipments(list, p))
	var orderIDs []string
	for _, s := range shipments {
		if s.OrderID != "" && !s.Cancelled {
			orderIDs = append(orderIDs, s.OrderID)
		}
	}
	qty, _ := c.orderQuantitiesUpTo(ctx, orderIDs, maxFinanceQuantityFetch)
	products := orderProducts(orderIDs)
	for i := range shipments {
		shipments[i].Product = products[shipments[i].OrderID]
	}

	f := summarizeShipments(shipments, qty, perUnit)
	f.Truncated = list.Truncated

	if cr, err := c.Credits(ctx); err == nil {
		f.Balance, f.Currency = &cr.Balance, cr.Currency
	} else {
		f.BalanceError = err.Error()
	}

	// Los movimientos y cargos se traen hasta hoy: una devolución puede llegar días después del envío.
	// Los listados y el resumen de gastos muestran solo los del periodo.
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

// containsDate dice si una fecha de Skydropx (con hora o solo el día) cae en el periodo.
// Las fechas que no se entienden cuentan como dentro, para no esconder movimientos.
func (p Period) containsDate(s string) bool {
	if t, ok := parseTime(s); ok {
		return p.contains(t.In(colombia))
	}
	if d, err := time.ParseInLocation("2006-01-02", s, colombia); err == nil {
		return p.contains(d)
	}
	return true
}

// perShipment arma la cuenta de cada envío juntando, por número de guía, los abonos y devoluciones
// a los créditos y los cargos extra.
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
			Date: s.Date, Tracking: s.Tracking, Customer: s.Customer, Status: status,
			LabelCost: s.Cost, CustomerPays: s.customerPays(), Product: s.Product,
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

// customerPays es lo que la transportadora le cobra al cliente: el producto más el envío.
// Skydropx a veces reporta el contra entrega ya con el envío (60.000 + 12.573 = 72.573) y a veces
// solo el producto (120.000): si se conoce el producto, se toma el mayor entre lo reportado y
// producto + envío. Sin el producto, se confía en lo reportado.
func (s financeShipment) customerPays() float64 {
	if s.Product > 0 && s.Product+s.Cost > s.CODAmount {
		return s.Product + s.Cost
	}
	return s.CODAmount
}

// withLabel deja fuera los envíos sin número de guía: intentos fallidos que nunca se generaron ni se pagaron.
func withLabel(shipments []financeShipment) []financeShipment {
	out := shipments[:0:0]
	for _, s := range shipments {
		if !s.NoLabel {
			out = append(out, s)
		}
	}
	return out
}

// financeShipment es lo que el resumen necesita de cada envío.
type financeShipment struct {
	Date       string
	Tracking   string
	Customer   string
	OrderID    string
	Cancelled  bool
	NoLabel    bool    // no tiene número de guía: falló o no se generó, no se pagó
	OnDelivery string  // pending | collected | failed | cancelled | vacío
	CODAmount  float64 // monto de contra entrega que reporta Skydropx: a veces trae el envío y a veces no
	Product    float64 // valor del producto según la orden (0 si no se conoce)
	Cost       float64 // costo de la guía
}

// financeShipments toma los envíos creados dentro del periodo.
func financeShipments(list ShipmentList, p Period) []financeShipment {
	names := map[string]string{} // id de la dirección -> nombre del destinatario
	for _, raw := range list.Included {
		var inc struct {
			ID         string `json:"id"`
			Type       string `json:"type"`
			Attributes struct {
				Name string `json:"name"`
			} `json:"attributes"`
		}
		if json.Unmarshal(raw, &inc) == nil && inc.Type == "address" {
			names[inc.ID] = inc.Attributes.Name
		}
	}
	var out []financeShipment
	for _, raw := range list.Data {
		var s struct {
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
		var customer string
		if to := s.Relationships.AddressTo.Data; to != nil {
			customer = names[to.ID]
		}
		out = append(out, financeShipment{
			Date:       s.Attributes.CreatedAt,
			Tracking:   s.Attributes.Tracking,
			Customer:   customer,
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
			f.Collected.Amount += s.customerPays()
			f.Commission.PaidUnits += units
			if !known {
				f.Commission.Estimated++
			}
		case "pending":
			f.Pending.Count++
			f.Pending.Amount += s.customerPays()
			f.Commission.PendingUnits += units
			if !known {
				f.Commission.Estimated++
			}
		case "failed":
			f.Failed.Count++
			f.Failed.Amount += s.customerPays()
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
				Amount: parseAmount(a.Amount), Tracking: a.Tracking, Comment: a.Comments, Status: a.Status, Related: a.Type,
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
	case kind == "refund": // devolver una protección o una tarifa no es un gasto
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
