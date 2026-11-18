package bot

import (
	"strings"
	"testing"

	"skydropx-automator/internal/domain"
)

func TestCarrierWarnings(t *testing.T) {
	margarita := domain.ShipmentData{City: "Margarita", Department: "Bolívar"}
	if w := carrierOpsWarning(margarita); !strings.Contains(w, "no hace contraentrega en <b>Margarita</b>") {
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
