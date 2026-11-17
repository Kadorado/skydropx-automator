package parser

import "testing"

func TestExplicitPostalCode(t *testing.T) {
	msg := "*Receptor:* Ana\n*Ciudad:* Cartagena\n*Código postal:* 130010\n*Dirección:* Cr 92#43-75"
	if got := ParseWhatsAppMessage(msg).Data.PostalCode; got != "130010" {
		t.Fatalf("CodigoPostal = %q, want 130010", got)
	}
	noPostal := "*Receptor:* Ana\n*Ciudad:* Cartagena\n*Dirección:* Cr 92#43-75"
	if got := ParseWhatsAppMessage(noPostal).Data.PostalCode; got != "130001" {
		t.Fatalf("CodigoPostal = %q, want 130001 de la tabla", got)
	}
}
