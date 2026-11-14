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

// maxTemplatePages limita cuántas páginas de direcciones guardadas se revisan (20 por página).
const maxTemplatePages = 5

// originTemplate guarda el ID de la dirección de origen ya encontrada para no buscarla en cada orden.
type originTemplate struct {
	mu sync.Mutex
	id string
}

// OriginTemplateID devuelve el ID de la dirección de origen guardada en Skydropx (Mis direcciones).
// Usa ORIGIN_ADDRESS_TEMPLATE_ID si está configurado; si no, busca la dirección de origen cuyo
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

// SaveRecipientAddress guarda la dirección del cliente en Mis direcciones de Skydropx con su nombre como alias
// (POST /api/v1/address_templates). Si ya hay una dirección de destino con el mismo alias y calle, no la repite.
func (c *Client) SaveRecipientAddress(ctx context.Context, data domain.ShipmentData, postal string) error {
	if c.isMock() {
		return ErrMockMode
	}
	alias := truncate(data.Nombre, 50)
	exists, err := c.recipientTemplateExists(ctx, alias, data.Direccion)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	email := strings.TrimSpace(data.Correo)
	if email == "" {
		email = c.cfg.OriginEmail // el correo es obligatorio
	}
	reference := strings.TrimSpace(data.Barrio)
	if reference == "" {
		reference = "Sin referencia"
	}
	body := map[string]any{
		"address_template": map[string]any{
			"alias_name":   alias,
			"address_type": "to",
			"address_attributes": map[string]any{
				"name":         truncate(data.Nombre, 30),
				"company":      truncate(data.Nombre, 60),
				"street1":      data.Direccion,
				"postal_code":  postal,
				"area_level1":  data.Departamento,
				"area_level2":  data.Ciudad,
				"country_code": "CO",
				"phone":        data.Telefono,
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

// recipientTemplateExists busca en Mis direcciones una dirección de destino con el mismo alias y calle.
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
