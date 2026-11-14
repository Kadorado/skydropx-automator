package bot

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"testing"
)

func TestValidWebhookAuth(t *testing.T) {
	body := []byte(`{"data":{"type":"packages"}}`)
	mac := hmac.New(sha512.New, []byte("secreto"))
	mac.Write(body)
	firma := hex.EncodeToString(mac.Sum(nil))
	alterada := "ff" + firma[2:]
	if firma[:2] == "ff" {
		alterada = "00" + firma[2:]
	}

	casos := []struct {
		nombre, header, secret string
		want                   bool
	}{
		{"hmac correcto", "HMAC " + firma, "secreto", true},
		{"hmac con otra clave", "HMAC " + firma, "otra", false},
		{"hmac alterado", "HMAC " + alterada, "secreto", false},
		{"bearer correcto", "Bearer secreto", "secreto", true},
		{"bearer incorrecto", "Bearer otro", "secreto", false},
		{"sin encabezado", "", "secreto", false},
		{"sin clave configurada", "Bearer ", "", false},
	}
	for _, c := range casos {
		if got := validWebhookAuth(c.header, body, c.secret); got != c.want {
			t.Errorf("%s: got %v, want %v", c.nombre, got, c.want)
		}
	}
}
