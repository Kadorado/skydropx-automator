package bot

import (
	"strings"
	"testing"

	"skydropx-automator/internal/domain"
)

func TestCarrierWarnings(t *testing.T) {
	nuqui := domain.ShipmentData{City: "Nuqui", Department: "Chocó"}
	if w := carrierOpsWarning(nuqui); !strings.Contains(w, "no hace contraentrega en <b>Nuqui</b>") {
		t.Fatalf("ops warning: %q", w)
	}
	if w := carrierSellerWarning(domain.ShipmentData{City: "Riosucio", Department: "Chocó"}); !strings.Contains(w, "no llega a <b>Riosucio</b>") {
		t.Fatalf("seller warning: %q", w)
	}
	quibdo := domain.ShipmentData{City: "Quibdó", Department: "Chocó"}
	if carrierOpsWarning(quibdo) != "" || carrierSellerWarning(quibdo) != "" {
		t.Fatal("Quibdó has Interrapidísimo: no warning")
	}
}
