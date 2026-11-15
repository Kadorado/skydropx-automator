package bot

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// WebhookHandler recibe los eventos de Skydropx (Conexiones > Webhooks) y avisa la guía en cuanto
// se crea el envío de una orden pendiente. La revisión periódica sigue funcionando como respaldo.
// header es el nombre del encabezado de autenticación configurado en Skydropx (por defecto Authorization).
func (b *Bot) WebhookHandler(secret, header string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "cuerpo inválido", http.StatusBadRequest)
			return
		}
		if !validWebhookAuth(r.Header.Get(header), body, secret) {
			log.Printf("webhook: solicitud rechazada (autenticación inválida)")
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}

		var ev webhookEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)

		orderID := ev.Data.Relationships.Order.Data.ID
		shipmentID := ev.Data.Relationships.Shipment.Data.ID
		log.Printf("webhook: %s %s (orden %s, envío %s)", ev.Data.Type, ev.Data.Attributes.Status, orderID, shipmentID)
		if ev.Data.Type != "packages" {
			return
		}
		// En contra entrega el cliente paga al recibir: entregado es pagado, y en devolución es no cobrado.
		// "delivered_to_branch" no cuenta: el paquete espera en la oficina y todavía no se ha pagado.
		switch ev.Data.Attributes.Status {
		case "delivered":
			go b.avisoEntrega(shipmentID, ev.Data.Attributes.Tracking, "collected")
		case "in_return":
			go b.avisoEntrega(shipmentID, ev.Data.Attributes.Tracking, "failed")
		case "delivery_attempt", "exception", "delivered_to_branch":
			go b.avisoIncidencia(shipmentID, ev.Data.Attributes.Status, ev.Data.Attributes.Tracking,
				ev.Data.Attributes.EventDescription, ev.Data.Attributes.TrackingURL)
		}
		if orderID == "" {
			return
		}
		// Se responde de inmediato y la consulta a Skydropx se hace aparte.
		go b.notifyOrder(orderID, shipmentID)
	}
}

type webhookEvent struct {
	Data struct {
		Type       string `json:"type"`
		Attributes struct {
			Status           string `json:"status"`
			Tracking         string `json:"tracking_number"`
			TrackingURL      string `json:"tracking_url_provider"`
			EventDescription string `json:"event_description"`
		} `json:"attributes"`
		Relationships struct {
			Shipment struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			} `json:"shipment"`
			Order struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			} `json:"order"`
		} `json:"relationships"`
	} `json:"data"`
}

// notifyOrder avisa la guía si la orden del evento es una de las creadas por el bot.
func (b *Bot) notifyOrder(orderID, shipmentID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, p := range b.st.snapshotPending() {
		if p.OrderID != orderID {
			continue
		}
		var ids []string
		if shipmentID != "" {
			ids = []string{shipmentID}
		}
		b.checkPending(ctx, p, ids)
		return
	}
}

// validWebhookAuth acepta "HMAC <firma>" (HMAC-SHA512 en hex del cuerpo crudo) o "Bearer <token>".
func validWebhookAuth(header string, body []byte, secret string) bool {
	if secret == "" {
		return false
	}
	scheme, value, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok {
		return false
	}
	value = strings.TrimSpace(value)
	switch strings.ToUpper(scheme) {
	case "HMAC":
		mac := hmac.New(sha512.New, []byte(secret))
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))
		return hmac.Equal([]byte(strings.ToLower(value)), []byte(expected))
	case "BEARER":
		return subtle.ConstantTimeCompare([]byte(value), []byte(secret)) == 1
	}
	return false
}
