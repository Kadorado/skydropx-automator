package skydropx

import (
	"context"
	"encoding/json"
	"strings"
)

// Recipient is the customer a shipment was sent to: only contact data and the shipment's date.
type Recipient struct {
	Date         string // label creation (RFC 3339)
	Name         string
	DocumentID   string
	Phone        string
	Email        string
	Department   string
	City         string
	Neighborhood string
	Address      string
}

// Recipients downloads up to maxPages pages of shipments (20 per page, bypassing the shared cache) and
// returns the recipient of every shipment that got a label and wasn't cancelled. truncated reports that
// there were more pages than maxPages.
func (c *Client) Recipients(ctx context.Context, maxPages int) (out []Recipient, truncated bool, err error) {
	if c.isMock() {
		return nil, false, ErrMockMode
	}
	list, err := c.fetchShipments(ctx, maxPages)
	if err != nil {
		return nil, false, err
	}
	return recipientsFromList(list), list.Truncated, nil
}

func recipientsFromList(list ShipmentList) []Recipient {
	type address struct {
		Name         string `json:"name"`
		Phone        string `json:"phone"`
		Email        string `json:"email"`
		TaxID        string `json:"tax_id_number"`
		Department   string `json:"area_level1"`
		City         string `json:"area_level2"`
		Neighborhood string `json:"area_level3"`
		Street       string `json:"street1"`
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
	var out []Recipient
	for _, raw := range list.Data {
		var s struct {
			Attributes struct {
				CreatedAt      string `json:"created_at"`
				Tracking       string `json:"master_tracking_number"`
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
		if json.Unmarshal(raw, &s) != nil || s.Attributes.WorkflowStatus == "cancelled" ||
			strings.TrimSpace(s.Attributes.Tracking) == "" || s.Relationships.AddressTo.Data == nil {
			continue
		}
		a := addresses[s.Relationships.AddressTo.Data.ID]
		out = append(out, Recipient{
			Date: s.Attributes.CreatedAt, Name: strings.TrimSpace(a.Name), DocumentID: strings.TrimSpace(a.TaxID),
			Phone: strings.TrimSpace(a.Phone), Email: strings.TrimSpace(a.Email), Department: strings.TrimSpace(a.Department),
			City: strings.TrimSpace(a.City), Neighborhood: strings.TrimSpace(a.Neighborhood), Address: strings.TrimSpace(a.Street),
		})
	}
	return out
}
