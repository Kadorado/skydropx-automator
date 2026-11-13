package parser

import "testing"

func TestValorUnitarioFormatos(t *testing.T) {
	casos := map[string]float64{
		"Valor: 60000":           60000,
		"Valor unitario: 60.000": 60000,
		"Precio: $60,000":        60000,
		"Cantidad: 2":            0,
	}
	for linea, esperado := range casos {
		got := ParseWhatsAppMessage("Nombre: Ana\n" + linea).Data.ValorUnitario
		if got != esperado {
			t.Errorf("%q: got %v, want %v", linea, got, esperado)
		}
	}
}

func TestMensajeConFormatoDeWhatsApp(t *testing.T) {
	raw := "¡Gracias Yeison! Ya quedó todo anotado 🫡\n" +
		"*DATOS DE ENVÍO – SERVICIO TÉCNICO ISTMINA* 📦\n" +
		"*Receptor:* YEISON ROA\n" +
		"*Documento:* 1077458155\n" +
		"*Teléfono:* 3145615105\n" +
		"*Ciudad:* QUIBDO CHOCO\n" +
		"*Barrio:* cesar contó\n" +
		"*Dirección:* Carrera 7 con 30\n" +
		"*Correo electrónico:* yeisoncuesta27@gmail.com\n" +
		"*Cantidad:* 2\n" +
		"━━━━━━━━━━━━━━\n" +
		"*Valor: 25.000*\n"
	d := ParseWhatsAppMessage(raw).Data
	if d.Nombre == "" || d.Documento != "1077458155" || d.Telefono != "3145615105" {
		t.Fatalf("no leyó nombre/documento/teléfono: %+v", d)
	}
	if d.Ciudad == "" || d.Direccion != "Carrera 7 con 30" || d.Cantidad != 2 {
		t.Fatalf("no leyó ciudad/dirección/cantidad: %+v", d)
	}
	if d.ValorUnitario != 25000 {
		t.Fatalf("no leyó el valor: %v", d.ValorUnitario)
	}
}
