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
	signature := hex.EncodeToString(mac.Sum(nil))
	tampered := "ff" + signature[2:]
	if signature[:2] == "ff" {
		tampered = "00" + signature[2:]
	}

	cases := []struct {
		fullName, header, secret string
		want                     bool
	}{
		{"hmac correcto", "HMAC " + signature, "secreto", true},
		{"hmac con otra clave", "HMAC " + signature, "otra", false},
		{"hmac alterado", "HMAC " + tampered, "secreto", false},
		{"bearer correcto", "Bearer secreto", "secreto", true},
		{"bearer incorrecto", "Bearer otro", "secreto", false},
		{"sin encabezado", "", "secreto", false},
		{"sin clave configurada", "Bearer ", "", false},
	}
	for _, c := range cases {
		if got := validWebhookAuth(c.header, body, c.secret); got != c.want {
			t.Errorf("%s: got %v, want %v", c.fullName, got, c.want)
		}
	}
}
