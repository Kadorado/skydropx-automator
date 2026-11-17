// Package telegram es un cliente mínimo de la API de bots de Telegram (long polling, mensajes con botones y archivos).
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
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

	// A file sent in the message (Caption is its text), and the message this one replies to.
	Caption  string    `json:"caption,omitempty"`
	Document *Document `json:"document,omitempty"`
	ReplyTo  *Message  `json:"reply_to_message,omitempty"`
}

// Document is a file attached to a message.
type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
}

// MaxDownloadSize is the largest file a bot can download from Telegram (20 MB).
const MaxDownloadSize = 20 << 20

// CallbackQuery llega cuando alguien toca un botón de un mensaje del bot.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// Button es un botón bajo el mensaje; Data vuelve en CallbackQuery.Data (máximo 64 bytes).
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

// Row arma una fila de botones.
func Row(buttons ...Button) []Button { return buttons }

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
		fmt.Sprintf("%s?offset=%d&timeout=25&allowed_updates=%s", c.url("getUpdates"), offset, `["message","callback_query"]`), nil)
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
	_, err := c.SendMessageWithButtons(ctx, chatID, html, nil)
	return err
}

// SendMessageWithButtons envía texto en HTML con filas de botones y devuelve el ID del mensaje.
func (c *Client) SendMessageWithButtons(ctx context.Context, chatID int64, html string, rows [][]Button) (int64, error) {
	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     html,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if len(rows) > 0 {
		payload["reply_markup"] = map[string]any{"inline_keyboard": rows}
	}
	var msg Message
	if err := c.call(ctx, "sendMessage", payload, &msg); err != nil {
		return 0, err
	}
	return msg.MessageID, nil
}

// EditMessage reemplaza el texto y los botones de un mensaje ya enviado (rows vacío quita los botones).
func (c *Client) EditMessage(ctx context.Context, chatID, messageID int64, html string, rows [][]Button) error {
	if rows == nil {
		rows = [][]Button{}
	}
	return c.call(ctx, "editMessageText", map[string]any{
		"chat_id":                  chatID,
		"message_id":               messageID,
		"text":                     html,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
		"reply_markup":             map[string]any{"inline_keyboard": rows},
	}, nil)
}

// AnswerCallback quita el "cargando" del botón tocado; text aparece como aviso breve si no está vacío.
func (c *Client) AnswerCallback(ctx context.Context, callbackID, text string) error {
	return c.call(ctx, "answerCallbackQuery", map[string]any{
		"callback_query_id": callbackID,
		"text":              text,
	}, nil)
}

// SendDocument envía un archivo (por ejemplo, el PDF de las guías) con un texto opcional en HTML.
// Command es un comando del menú que Telegram muestra al escribir "/".
type Command struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// SetChatCommands fija el menú de comandos de un chat (grupo o privado) en particular.
func (c *Client) SetChatCommands(ctx context.Context, chatID int64, cmds []Command) error {
	payload := map[string]any{
		"commands": cmds,
		"scope":    map[string]any{"type": "chat", "chat_id": chatID},
	}
	return c.call(ctx, "setMyCommands", payload, nil)
}

func (c *Client) SendDocument(ctx context.Context, chatID int64, filename string, data []byte, caption string) error {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("chat_id", fmt.Sprintf("%d", chatID))
	if caption != "" {
		_ = w.WriteField("caption", caption)
		_ = w.WriteField("parse_mode", "HTML")
	}
	part, err := w.CreateFormFile("document", filename)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("sendDocument"), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return c.do(req, "sendDocument", nil)
}

// DownloadFile downloads a file sent to the bot (up to MaxDownloadSize).
func (c *Client) DownloadFile(ctx context.Context, fileID string) ([]byte, error) {
	var f struct {
		FilePath string `json:"file_path"`
	}
	if err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
		return nil, err
	}
	if f.FilePath == "" {
		return nil, fmt.Errorf("telegram didn't return the file path")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/file/bot%s/%s", c.base, c.token, f.FilePath), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram file download HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxDownloadSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxDownloadSize {
		return nil, fmt.Errorf("the file is larger than %d MB", MaxDownloadSize>>20)
	}
	return data, nil
}

// call hace un POST JSON a un método de la API y decodifica "result" en out (si no es nil).
func (c *Client) call(ctx context.Context, method string, payload any, out any) error {
	data, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(method), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, method, out)
}

func (c *Client) do(req *http.Request, method string, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram %s HTTP %d: %s", method, resp.StatusCode, string(body))
	}
	if out == nil {
		return nil
	}
	var wrap struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return fmt.Errorf("respuesta inesperada de Telegram en %s", method)
	}
	return json.Unmarshal(wrap.Result, out)
}
