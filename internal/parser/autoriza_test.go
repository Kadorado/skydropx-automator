package parser

import "testing"

func TestFuente(t *testing.T) {
	casos := map[string]string{
		"Fuente: Facebook":           "facebook",
		"Origen: *Campaña WhatsApp*": "campaña whatsapp",
		"Llegó por: referido":        "referido",
		"":                           "",
	}
	for linea, want := range casos {
		msg := "Nombre: Ana Pérez\nTeléfono: 3001234567\n" + linea
		if got := ParseWhatsAppMessage(msg).Data.Fuente; got != want {
			t.Errorf("%q: %q, quería %q", linea, got, want)
		}
	}
}

func TestAutorizaDatos(t *testing.T) {
	casos := map[string]string{
		"Autoriza datos: SÍ":    "si",
		"autoriza: si":          "si",
		"Acepta datos: Sí.":     "si",
		"Autoriza datos: No":    "no",
		"Autorización: x":       "si",
		"Autoriza datos: luego": "",
		"":                      "",
	}
	for linea, want := range casos {
		msg := "Nombre: Ana Pérez\nTeléfono: 3001234567\n" + linea
		if got := ParseWhatsAppMessage(msg).Data.AutorizaDatos; got != want {
			t.Errorf("%q: %q, quería %q", linea, got, want)
		}
	}
}
