package skydropx

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

// Undispatched is a shipment whose label exists but the carrier hasn't received the package yet.
type Undispatched struct {
	ShipmentID string
	Tracking   string
	Carrier    string
	CreatedAt  time.Time
	Name       string
	City       string
	Department string
	// Unknown means the tracking couldn't be checked: it may already be dispatched.
	Unknown bool
}

var (
	dispatchedMu sync.Mutex
	dispatched   = map[string]bool{} // tracking numbers already received by the carrier: never undispatched again
)

// Undispatched lists the shipments created since `since` that have a label, weren't cancelled and whose
// tracking shows nothing after "created", oldest first. Shipments seen past "created" are remembered, so
// only new ones and the ones still waiting are looked up again.
func (c *Client) Undispatched(ctx context.Context, since time.Time) ([]Undispatched, error) {
	if c.isMock() {
		return nil, ErrMockMode
	}
	list, err := c.ListShipments(ctx)
	if err != nil {
		return nil, err
	}
	candidates := undispatchedCandidates(list, since)

	var out []Undispatched
	for _, u := range candidates {
		dispatchedMu.Lock()
		done := dispatched[u.Tracking]
		dispatchedMu.Unlock()
		if done {
			continue
		}
		st, err := c.Rastreo(ctx, u.Tracking, u.Carrier)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			u.Unknown = true
			out = append(out, u)
			continue
		}
		if st.Raw != "" && st.Raw != "created" {
			dispatchedMu.Lock()
			dispatched[u.Tracking] = true
			dispatchedMu.Unlock()
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

// undispatchedCandidates picks from the list the shipments since `since` with a label and not cancelled.
func undispatchedCandidates(list ShipmentList, since time.Time) []Undispatched {
	type address struct {
		Name       string `json:"name"`
		Department string `json:"area_level1"`
		City       string `json:"area_level2"`
	}
	addresses := map[string]address{}
	for _, raw := range list.Included {
		var inc struct {
			ID         string  `json:"id"`
			Type       string  `json:"type"`
			Attributes address `json:"attributes"`
		}
		if json.Unmarshal(raw, &inc) == nil && inc.Type == "address" {
			addresses[inc.ID] = inc.Attributes
		}
	}
	var out []Undispatched
	for _, raw := range list.Data {
		var s struct {
			ID         string `json:"id"`
			Attributes struct {
				CreatedAt      string `json:"created_at"`
				Tracking       string `json:"master_tracking_number"`
				Carrier        string `json:"carrier_name"`
				WorkflowStatus string `json:"workflow_status"`
			} `json:"attributes"`
			Relationships struct {
				AddressTo struct {
					Data *struct {
						ID string `json:"id"`
					} `json:"data"`
				} `json:"address_to"`
			} `json:"relationships"`
		}
		if json.Unmarshal(raw, &s) != nil || s.Attributes.WorkflowStatus == "cancelled" || strings.TrimSpace(s.Attributes.Tracking) == "" {
			continue
		}
		created, err := time.Parse(time.RFC3339, s.Attributes.CreatedAt)
		if err != nil || created.Before(since) {
			continue
		}
		u := Undispatched{ShipmentID: s.ID, Tracking: s.Attributes.Tracking, Carrier: s.Attributes.Carrier, CreatedAt: created}
		if s.Relationships.AddressTo.Data != nil {
			a := addresses[s.Relationships.AddressTo.Data.ID]
			u.Name, u.City, u.Department = strings.TrimSpace(a.Name), strings.TrimSpace(a.City), strings.TrimSpace(a.Department)
		}
		out = append(out, u)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}
