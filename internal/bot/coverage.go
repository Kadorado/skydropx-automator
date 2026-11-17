package bot

import (
	"html"

	"skydropx-automator/internal/domain"
	"skydropx-automator/internal/parser"
)

// carrierProblem explains in one line why Interrapidísimo probably won't work for the destination
// ("" if there is no known problem).
func carrierProblem(data domain.ShipmentData) string {
	city := html.EscapeString(data.City)
	switch parser.InterrapidisimoCoverage(data.City, data.Department) {
	case parser.CoverageNoCOD:
		return "Interrapidísimo no hace contraentrega en <b>" + city + "</b>"
	case parser.CoverageNone:
		return "Interrapidísimo no llega a <b>" + city + "</b>"
	case parser.CoverageNoCode:
		return "Skydropx no reconoce ningún código postal de <b>" + city + "</b>"
	}
	return ""
}

// carrierOpsWarning is the block shown on top of the order in the ops group.
func carrierOpsWarning(data domain.ShipmentData) string {
	p := carrierProblem(data)
	if p == "" {
		return ""
	}
	return "🚫 <b>DESTINO SIN INTERRAPIDÍSIMO</b>\n" + p + ".\n<i>La guía con el bot probablemente va a fallar: revisa otra transportadora a mano en Skydropx.</i>\n━━━━━━━━━━━━━━━\n\n"
}

// carrierSellerWarning is the short line the seller sees.
func carrierSellerWarning(data domain.ShipmentData) string {
	p := carrierProblem(data)
	if p == "" {
		return ""
	}
	return "\n\n🚫 " + p + ": la guía puede demorarse mientras se busca otra transportadora."
}
