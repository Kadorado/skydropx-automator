package skydropx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
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
