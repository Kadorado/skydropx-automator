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

func TestPostalCodeFromTable(t *testing.T) {
	cases := []struct {
		city, wantCity, wantDept, wantPostal string
	}{
		{"Banco Magdalena", "El Banco", "Magdalena", "473040"},
		{"Banco", "El Banco", "Magdalena", "473040"},
		{"EL BANCO", "El Banco", "Magdalena", "473040"},
		{"Tadó", "Tadó", "Chocó", "271070"}, // the old table had 273010
		{"Tad", "Tadó", "Chocó", "271070"},
		{"Condoto", "Condoto", "Chocó", "273030"}, // the old table had 274030
		{"Itagüí", "Itagüí", "Antioquia", "055410"},
		{"Bogotá", "Bogotá D.C.", "Bogotá D.C.", "110111"},
		{"La Unión Nariño", "La Union", "Nariño", "521520"},
		{"La Unión", "La Unión", "", ""}, // exists in several departments
	}
	for _, c := range cases {
		msg := "*Receptor:* Ana\n*Ciudad:* " + c.city + "\n*Dirección:* Cr 1"
		d := ParseWhatsAppMessage(msg).Data
		if d.City != c.wantCity || d.Department != c.wantDept || d.PostalCode != c.wantPostal {
			t.Errorf("%q -> (%q, %q, %q), want (%q, %q, %q)", c.city, d.City, d.Department, d.PostalCode, c.wantCity, c.wantDept, c.wantPostal)
		}
	}
}

func TestPostalCodeWithDepartmentLine(t *testing.T) {
	msg := "*Receptor:* Ana\n*Ciudad:* La Unión\n*Departamento:* Nariño\n*Dirección:* Cr 1"
	if got := ParseWhatsAppMessage(msg).Data.PostalCode; got != "521520" {
		t.Fatalf("CodigoPostal = %q, want 521520", got)
	}
}
