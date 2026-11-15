package bot

import (
	"path/filepath"
	"strings"
	"testing"

	"skydropx-automator/internal/skydropx"
)

func TestMarcarCobroNoAvisaDosVeces(t *testing.T) {
	st, err := loadState(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if nuevo, _ := st.marcarCobro("s1", "collected"); !nuevo {
		t.Fatal("la entrega debe avisar el pago")
	}
	if nuevo, _ := st.marcarCobro("s1", "collected"); nuevo {
		t.Fatal("un webhook repetido no debe avisar otra vez")
	}
	// El estado sobrevive al reinicio.
	again, err := loadState(st.path)
	if err != nil {
		t.Fatal(err)
	}
	if nuevo, _ := again.marcarCobro("s1", "collected"); nuevo {
		t.Fatal("después de reiniciar no debe repetir el aviso")
	}
}

func TestCobroMensaje(t *testing.T) {
	e := skydropx.CODShipment{Tracking: "240065822548", Customer: "Aracely cedeño macias", Status: "collected", Cost: 16532}
	msg := cobroMensaje(e, 136532, 120000)
	for _, want := range []string{"¡Pagaron!", "Aracely cedeño macias", "240065822548", "$136.532", "producto $120.000 + envío $16.532"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
	e.Status = "failed"
	if msg := cobroMensaje(e, 136532, 0); !strings.Contains(msg, "No se pudo cobrar") || !strings.Contains(msg, "producto + envío") {
		t.Errorf("mensaje de no cobrado inesperado:\n%s", msg)
	}
}
