package skydropx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"skydropx-automator/internal/domain"
)

// maxTemplatePages limits how many pages of saved addresses are checked (20 per page).
const maxTemplatePages = 5

// originTemplate keeps the ID of the origin address once found so it isn't looked up on every order.
type originTemplate struct {
	mu sync.Mutex
	id string
}

// OriginTemplateID returns the ID of the origin address saved in Skydropx (Mis direcciones).
// It uses ORIGIN_ADDRESS_TEMPLATE_ID if set; otherwise it looks for the origin address whose
// alias sea ORIGIN_ADDRESS_ALIAS (GET /api/v1/address_templates).
func (c *Client) OriginTemplateID(ctx context.Context) (string, error) {
	if id := strings.TrimSpace(c.cfg.OriginAddressTemplateID); id != "" {
		return id, nil
	}
	c.origin.mu.Lock()
	defer c.origin.mu.Unlock()
	if c.origin.id != "" {
		return c.origin.id, nil
	}

	alias := strings.TrimSpace(c.cfg.OriginAddressAlias)
	for page := 1; page <= maxTemplatePages; page++ {
		body, status, err := c.get(ctx, fmt.Sprintf("%s/api/v1/address_templates?page=%d&per_page=20", c.cfg.SkydropxBaseURL, page))
		if err != nil {
			return "", err
		}
		if status < 200 || status >= 300 {
			return "", fmt.Errorf("Skydropx respondió HTTP %d al consultar las direcciones guardadas", status)
		}
		var list struct {
			Data []struct {
				ID          string `json:"id"`
				AliasName   string `json:"alias_name"`
				AddressType string `json:"address_type"`
			} `json:"data"`
			Meta struct {
				NextPage *int `json:"next_page"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			return "", fmt.Errorf("respuesta inesperada de Skydropx al consultar las direcciones guardadas: %w", err)
		}
		for _, t := range list.Data {
			if t.AddressType == "from" && strings.EqualFold(strings.TrimSpace(t.AliasName), alias) {
				c.origin.id = t.ID
				return t.ID, nil
			}
		}
		if list.Meta.NextPage == nil {
			break
		}
	}
	return "", fmt.Errorf("no se encontró en Skydropx la dirección de origen con alias %q (Mis direcciones)", alias)
}

// SaveRecipientAddress saves the customer's address in Skydropx's Mis direcciones with their name as alias
// (POST /api/v1/address_templates). If a destination address with the same alias and street exists, it isn't repeated.
func (c *Client) SaveRecipientAddress(ctx context.Context, data domain.ShipmentData, postal string) error {
	if c.isMock() {
		return ErrMockMode
	}
	alias := truncate(data.Name, 50)
	exists, err := c.recipientTemplateExists(ctx, alias, data.Address)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	email := strings.TrimSpace(data.Email)
	if email == "" {
		email = c.cfg.OriginEmail // email is required
	}
	reference := strings.TrimSpace(data.Neighborhood)
	if reference == "" {
		reference = "Sin referencia"
	}
	body := map[string]any{
		"address_template": map[string]any{
			"alias_name":   alias,
			"address_type": "to",
			"address_attributes": map[string]any{
				"name":         truncate(data.Name, 30),
				"company":      truncate(data.Name, 60),
				"street1":      data.Address,
				"postal_code":  postal,
				"area_level1":  data.Department,
				"area_level2":  data.City,
				"country_code": "CO",
				"phone":        data.Phone,
				"email":        email,
				"reference":    truncate(reference, 40),
			},
		},
	}
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return err
	}
	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.SkydropxBaseURL+"/api/v1/address_templates/", bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("error de conexión con Skydropx al guardar la dirección: %w", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Skydropx no guardó la dirección: %s", quotationErrorMessage(respBody, resp.StatusCode))
	}
	return nil
}

// recipientTemplateExists looks in Mis direcciones for a destination address with the same alias and street.
func (c *Client) recipientTemplateExists(ctx context.Context, alias, street string) (bool, error) {
	for page := 1; page <= maxTemplatePages; page++ {
		body, status, err := c.get(ctx, fmt.Sprintf("%s/api/v1/address_templates?page=%d&per_page=20", c.cfg.SkydropxBaseURL, page))
		if err != nil {
			return false, err
		}
		if status < 200 || status >= 300 {
			return false, fmt.Errorf("Skydropx respondió HTTP %d al consultar las direcciones guardadas", status)
		}
		var list struct {
			Data []struct {
				AliasName   string `json:"alias_name"`
				AddressType string `json:"address_type"`
				Address     struct {
					Street1 string `json:"street1"`
				} `json:"address"`
			} `json:"data"`
			Meta struct {
				NextPage *int `json:"next_page"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			return false, fmt.Errorf("respuesta inesperada de Skydropx al consultar las direcciones guardadas: %w", err)
		}
		for _, t := range list.Data {
			if t.AddressType != "from" && strings.EqualFold(strings.TrimSpace(t.AliasName), alias) &&
				strings.EqualFold(strings.TrimSpace(t.Address.Street1), strings.TrimSpace(street)) {
				return true, nil
			}
		}
		if list.Meta.NextPage == nil {
			break
		}
	}
	return false, nil
}
