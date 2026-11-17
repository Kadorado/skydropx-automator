package parser

import "testing"

func TestSource(t *testing.T) {
	cases := map[string]string{
		"Fuente: Facebook":           "facebook",
		"Origen: *Campaña WhatsApp*": "campaña whatsapp",
		"Llegó por: referido":        "referido",
		"":                           "",
	}
	for lineText, want := range cases {
		msg := "Nombre: Ana Pérez\nTeléfono: 3001234567\n" + lineText
		if got := ParseWhatsAppMessage(msg).Data.Source; got != want {
			t.Errorf("%q: %q, quería %q", lineText, got, want)
		}
	}
}

func TestDataConsent(t *testing.T) {
	cases := map[string]string{
		"Autoriza datos: SÍ":    "si",
		"autoriza: si":          "si",
		"Acepta datos: Sí.":     "si",
		"Autoriza datos: No":    "no",
		"Autorización: x":       "si",
		"Autoriza datos: luego": "",
		"":                      "",
	}
	for lineText, want := range cases {
		msg := "Nombre: Ana Pérez\nTeléfono: 3001234567\n" + lineText
		if got := ParseWhatsAppMessage(msg).Data.DataConsent; got != want {
			t.Errorf("%q: %q, quería %q", lineText, got, want)
		}
	}
}
