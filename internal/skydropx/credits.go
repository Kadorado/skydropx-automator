package skydropx

import (
	"context"
	"encoding/json"
	"fmt"
)

// Credits is the Skydropx account balance, used to pay for labels.
type Credits struct {
	Balance  float64
	Currency string
}

// Credits looks up the account's current balance (GET /api/v1/finance/credits).
func (c *Client) Credits(ctx context.Context) (Credits, error) {
	if c.isMock() {
		return Credits{}, ErrMockMode
	}
	body, status, err := c.get(ctx, c.cfg.SkydropxBaseURL+"/api/v1/finance/credits")
	if err != nil {
		return Credits{}, err
	}
	if status < 200 || status >= 300 {
		return Credits{}, fmt.Errorf("Skydropx respondió HTTP %d al consultar el saldo", status)
	}
	var out struct {
		Data struct {
			Balance  json.RawMessage `json:"balance"`
			Currency string          `json:"currency"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return Credits{}, fmt.Errorf("respuesta inesperada de Skydropx al consultar el saldo")
	}
	return Credits{Balance: parseAmount(out.Data.Balance), Currency: out.Data.Currency}, nil
}
