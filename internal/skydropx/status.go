package skydropx

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Status es el estado logístico y de cobro de un envío, calculado desde su último evento de rastreo.
type Status struct {
	Key        string `json:"key"`        // en_camino | entregado | intento_entrega | creado | en_proceso | cancelado | sin_guia | sin_estado
	Label      string `json:"label"`      // texto para la pantalla
	LastEvent  string `json:"last_event"` // descripción del último evento
	LastDate   string `json:"last_date"`  // fecha del último evento (ISO)
	Cobro      string `json:"cobro"`      // cobrado | por_cobrar | no_aplica
	CobroLabel string `json:"cobro_label"`
	Raw        string `json:"raw,omitempty"` // estado técnico del último evento (delivery_attempt, in_return…)
}

type cachedStatus struct {
	status Status
	exp    time.Time
}

var (
	statusMu    sync.Mutex
	statusCache = map[string]cachedStatus{} // clave: número de guía
	lastTrackAt time.Time
)

const (
	trackSpacing = 550 * time.Millisecond // por debajo del límite de 2 solicitudes por segundo
	finalTTL     = 24 * time.Hour         // entregado y cancelado no cambian
	activeTTL    = 10 * time.Minute       // en tránsito puede cambiar
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

// ShipmentStatuses devuelve el estado de cada envío de la cuenta, indexado por ID de envío.
// Usa caché para no repetir consultas de rastreo y respeta el límite de la API.
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
		cobro, cobroLabel := cobroOf(a.OnDeliveryStatus, a.WorkflowStatus)

		if a.WorkflowStatus == "cancelled" {
			out[item.ID] = Status{Key: "cancelado", Label: "Cancelado", Cobro: cobro, CobroLabel: cobroLabel}
			continue
		}
		if a.MasterTrackingNumber == "" {
			out[item.ID] = Status{Key: "sin_guia", Label: "Sin guía", Cobro: cobro, CobroLabel: cobroLabel}
			continue
		}

		st, err := c.trackingStatus(ctx, a.MasterTrackingNumber, a.CarrierName)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			st = Status{Key: "sin_estado", Label: "Sin rastreo"}
		}
		st.Cobro, st.CobroLabel = cobro, cobroLabel
		out[item.ID] = st
	}
	return out, nil
}

// trackingStatus consulta (o toma de caché) el último evento de una guía.
func (c *Client) trackingStatus(ctx context.Context, tracking, carrier string) (Status, error) {
	if cached, ok := statusCache[tracking]; ok && time.Now().Before(cached.exp) {
		return cached.status, nil
	}

	// Espera lo necesario para no superar el límite de la API.
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

	var (
		lastAt, stAt, delAt time.Time
		lastDesc, lastDate  string
		stStatus            string
		delivered           bool
	)
	for _, ev := range resp.Data {
		t, perr := time.Parse(time.RFC3339Nano, ev.Attributes.Date)
		if perr != nil {
			continue
		}
		if lastAt.IsZero() || t.After(lastAt) {
			lastAt, lastDesc, lastDate = t, ev.Attributes.Description, ev.Attributes.Date
		}
		if ev.Attributes.Status == "delivered" && (delAt.IsZero() || t.After(delAt)) {
			delivered, delAt = true, t
		}
		// Último evento que sí trae estado técnico (los eventos administrativos como "Archivada" no lo traen).
		if ev.Attributes.Status != "" && (stAt.IsZero() || t.After(stAt)) {
			stAt, stStatus = t, ev.Attributes.Status
		}
	}

	st := Status{LastEvent: lastDesc, LastDate: lastDate, Key: "creado", Label: "Creado", Raw: stStatus}
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

// keyOf traduce el estado técnico de Skydropx a una clave y etiqueta en español.
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

func cobroOf(onDelivery, workflow string) (string, string) {
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
