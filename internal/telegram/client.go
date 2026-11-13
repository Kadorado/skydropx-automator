// Package telegram es un cliente mínimo de la API de bots de Telegram (long polling y envío de mensajes).
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultBase = "https://api.telegram.org"

type Chat struct {
	ID int64 `json:"id"`
}

type User struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	Chat      Chat   `json:"chat"`
	From      *User  `json:"from"`
	Text      string `json:"text"`
}

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

type Client struct {
	token string
	base  string
	http  *http.Client
}

// New crea el cliente. base puede cambiarse para pruebas; vacío usa la API oficial.
func New(token, base string) *Client {
	if base == "" {
		base = defaultBase
	}
	return &Client{
		token: token,
		base:  strings.TrimRight(base, "/"),
		// Mayor que el timeout del long polling (25 s).
		http: &http.Client{Timeout: 40 * time.Second},
	}
}

func (c *Client) url(method string) string {
	return fmt.Sprintf("%s/bot%s/%s", c.base, c.token, method)
}

// GetUpdates espera hasta 25 s por mensajes nuevos a partir de offset.
func (c *Client) GetUpdates(ctx context.Context, offset int64) ([]Update, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s?offset=%d&timeout=25&allowed_updates=%s", c.url("getUpdates"), offset, `["message"]`), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram getUpdates HTTP %d", resp.StatusCode)
	}
	var out struct {
		OK     bool     `json:"ok"`
		Result []Update `json:"result"`
	}
	if err := json.Unmarshal(body, &out); err != nil || !out.OK {
		return nil, fmt.Errorf("respuesta inesperada de Telegram")
	}
	return out.Result, nil
}

// SendMessage envía texto en HTML a un chat.
func (c *Client) SendMessage(ctx context.Context, chatID int64, html string) error {
	payload, _ := json.Marshal(map[string]any{
		"chat_id":                  chatID,
		"text":                     html,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("sendMessage"), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram sendMessage HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
