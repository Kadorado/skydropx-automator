package skydropx

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

// Status is a shipment's logistics and collection state, computed from its last tracking event.
type Status struct {
	Key       string `json:"key"`        // en_camino | entregado | intento_entrega | creado | en_proceso | cancelado | sin_guia | sin_estado
	Label     string `json:"label"`      // text for the screen
	LastEvent string `json:"last_event"` // description of the last event
	LastDate  string `json:"last_date"`  // date of the last event (ISO)
	CODState  string `json:"cobro"`      // cobrado | por_cobrar | no_aplica
	CODLabel  string `json:"cobro_label"`
	Raw       string `json:"raw,omitempty"` // technical state of the last event (delivery_attempt, in_return…)
	// Days the package waited at the carrier's office until the customer picked it up
	// (0 if it didn't go through an office or hasn't been picked up).
	DaysAtOffice int          `json:"dias_en_oficina,omitempty"`
	History      []TrackEvent `json:"-"` // latest tracking events, newest first
}

// TrackEvent is a carrier tracking event.
type TrackEvent struct {
	Date        time.Time
	Description string
	Status      string
}

// maxHistory is how many recent events are kept per label.
const maxHistory = 4

type cachedStatus struct {
	status Status
	exp    time.Time
}

var (
	statusMu    sync.Mutex
	statusCache = map[string]cachedStatus{} // key: tracking number
	lastTrackAt time.Time
)

const (
	trackSpacing = 550 * time.Millisecond // below the limit of 2 requests per second
	finalTTL     = 24 * time.Hour         // delivered and cancelled don't change
	activeTTL    = 10 * time.Minute       // in transit may change
)

type listItem struct {
	ID         string `json:"id"`
	Attributes struct {
		MasterTrackingNumber string `json:"master_tracking_number"`
		CarrierName          string `json:"carrier_name"`
		WorkflowStatus       string `json:"workflow_status"`
		OnDeliveryStatus     string `json:"on_delivery_status"`
	} `json:"attributes"`
}

// ShipmentStatuses returns the state of every shipment in the account, keyed by shipment ID.
// It caches to avoid repeating tracking lookups and respects the API limit.
func (c *Client) ShipmentStatuses(ctx context.Context) (map[string]Status, error) {
	list, err := c.ListShipments(ctx)
	if err != nil {
		return nil, err
	}

	statusMu.Lock()
	defer statusMu.Unlock()

	out := make(map[string]Status, len(list.Data))
	for _, raw := range list.Data {
		var item listItem
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		a := item.Attributes
		cod, codLabel := codOf(a.OnDeliveryStatus, a.WorkflowStatus)

		if a.WorkflowStatus == "cancelled" {
			out[item.ID] = Status{Key: "cancelado", Label: "Cancelado", CODState: cod, CODLabel: codLabel}
			continue
		}
		if a.MasterTrackingNumber == "" {
			out[item.ID] = Status{Key: "sin_guia", Label: "Sin guía", CODState: cod, CODLabel: codLabel}
			continue
		}

		st, err := c.trackingStatus(ctx, a.MasterTrackingNumber, a.CarrierName)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			st = Status{Key: "sin_estado", Label: "Sin rastreo"}
		}
		st.CODState, st.CODLabel = cod, codLabel
		out[item.ID] = st
	}
	return out, nil
}

// TrackStatus returns a label's state with its latest events (cached and respecting the API limit).
func (c *Client) TrackStatus(ctx context.Context, tracking, carrier string) (Status, error) {
	if c.isMock() {
		return Status{}, ErrMockMode
	}
	statusMu.Lock()
	defer statusMu.Unlock()
	return c.trackingStatus(ctx, tracking, carrier)
}

// trackingStatus looks up (or takes from cache) a label's last event.
func (c *Client) trackingStatus(ctx context.Context, tracking, carrier string) (Status, error) {
	if cached, ok := statusCache[tracking]; ok && time.Now().Before(cached.exp) {
		return cached.status, nil
	}

	// Wait as needed to stay under the API limit.
	if wait := trackSpacing - time.Since(lastTrackAt); wait > 0 {
		select {
		case <-ctx.Done():
			return Status{}, ctx.Err()
		case <-time.After(wait):
		}
	}
	lastTrackAt = time.Now()

	body, err := c.Track(ctx, tracking, carrier)
	if err != nil {
		return Status{}, err
	}

	var resp struct {
		Data []struct {
			Attributes struct {
				Description string `json:"description"`
				Status      string `json:"status"`
				Date        string `json:"date"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Status{}, err
	}

	var history []TrackEvent
	var (
		lastAt, stAt, delAt time.Time
		branchAt            time.Time // first time it was at the office
		lastDesc, lastDate  string
		stStatus            string
		delivered           bool
	)
	for _, ev := range resp.Data {
		t, perr := time.Parse(time.RFC3339Nano, ev.Attributes.Date)
		if perr != nil {
			continue
		}
		history = append(history, TrackEvent{Date: t, Description: ev.Attributes.Description, Status: ev.Attributes.Status})
		if lastAt.IsZero() || t.After(lastAt) {
			lastAt, lastDesc, lastDate = t, ev.Attributes.Description, ev.Attributes.Date
		}
		if ev.Attributes.Status == "delivered_to_branch" && (branchAt.IsZero() || t.Before(branchAt)) {
			branchAt = t
		}
		if ev.Attributes.Status == "delivered" && (delAt.IsZero() || t.After(delAt)) {
			delivered, delAt = true, t
		}
		// Last event that does have a technical state (administrative events like "Archivada" don't).
		if ev.Attributes.Status != "" && (stAt.IsZero() || t.After(stAt)) {
			stAt, stStatus = t, ev.Attributes.Status
		}
	}

	sort.SliceStable(history, func(i, j int) bool { return history[i].Date.After(history[j].Date) })
	if len(history) > maxHistory {
		history = history[:maxHistory]
	}
	st := Status{LastEvent: lastDesc, LastDate: lastDate, Key: "creado", Label: "Creado", Raw: stStatus, History: history}
	if delivered && !branchAt.IsZero() && delAt.After(branchAt) {
		st.DaysAtOffice = int(delAt.Sub(branchAt).Hours() / 24)
	}
	if delivered {
		st.Raw = "delivered"
		st.Key, st.Label = "entregado", "Entregado"
	} else if stStatus != "" {
		st.Key, st.Label = keyOf(stStatus)
	}

	ttl := activeTTL
	if st.Key == "entregado" || st.Key == "cancelado" {
		ttl = finalTTL
	}
	statusCache[tracking] = cachedStatus{status: st, exp: time.Now().Add(ttl)}
	return st, nil
}

// keyOf translates Skydropx's technical state into a key and a label in Spanish.
func keyOf(status string) (string, string) {
	switch status {
	case "delivered":
		return "entregado", "Entregado"
	case "delivery_attempt":
		return "intento_entrega", "Intento de entrega"
	case "in_transit":
		return "en_camino", "En camino"
	case "created":
		return "creado", "Creado"
	default:
		return "en_proceso", "En proceso"
	}
}

func codOf(onDelivery, workflow string) (string, string) {
	if workflow == "cancelled" {
		return "no_aplica", "—"
	}
	switch onDelivery {
	case "collected":
		return "cobrado", "Cobrado"
	case "pending":
		return "por_cobrar", "Por cobrar"
	default:
		return "no_aplica", "—"
	}
}
