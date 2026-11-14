package parser

import "testing"

func TestCodigoPostalExplicito(t *testing.T) {
	msg := "*Receptor:* Ana\n*Ciudad:* Cartagena\n*Código postal:* 130010\n*Dirección:* Cr 92#43-75"
	if got := ParseWhatsAppMessage(msg).Data.CodigoPostal; got != "130010" {
		t.Fatalf("CodigoPostal = %q, want 130010", got)
	}
	sinCP := "*Receptor:* Ana\n*Ciudad:* Cartagena\n*Dirección:* Cr 92#43-75"
	if got := ParseWhatsAppMessage(sinCP).Data.CodigoPostal; got != "130001" {
		t.Fatalf("CodigoPostal = %q, want 130001 de la tabla", got)
	}
}
