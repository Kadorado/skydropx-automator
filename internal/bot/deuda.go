package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"skydropx-automator/internal/settings"
	"skydropx-automator/internal/skydropx"
)

// maxDiasDeuda limita el rango de /deuda para que el mensaje quepa en Telegram.
const maxDiasDeuda = 62

var diasCortos = [...]string{"dom", "lun", "mar", "mié", "jue", "vie", "sáb"}

// rangoDeuda entiende el rango de /deuda (fechas en hora de Colombia, ambas incluidas):
// vacío = últimos 7 días, "5" = últimos 5 días, "hoy", "ayer", "semana" = la anterior (lunes a domingo),
// "mes" = el mes en curso, "2026-11-03" = ese día, "2026-11-01 2026-11-10" = ese rango.
func rangoDeuda(args string, now time.Time) (desde, hasta time.Time, err error) {
	now = now.In(colombiaTZ)
	hoy := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, colombiaTZ)
	f := strings.Fields(strings.ToLower(args))
	switch {
	case len(f) == 0:
		return hoy.AddDate(0, 0, -6), hoy, nil
	case len(f) == 1 && f[0] == "hoy":
		return hoy, hoy, nil
	case len(f) == 1 && f[0] == "ayer":
		return hoy.AddDate(0, 0, -1), hoy.AddDate(0, 0, -1), nil
	case len(f) == 1 && f[0] == "semana":
		offset := (int(hoy.Weekday()) + 6) % 7 // días desde el lunes
		lunes := hoy.AddDate(0, 0, -offset-7)
		return lunes, lunes.AddDate(0, 0, 6), nil
	case len(f) == 1 && f[0] == "mes":
		return hoy.AddDate(0, 0, 1-hoy.Day()), hoy, nil
	case len(f) == 1:
		if n, err := strconv.Atoi(f[0]); err == nil {
			if n < 1 || n > maxDiasDeuda {
				return desde, hasta, fmt.Errorf("los días van de 1 a %d", maxDiasDeuda)
			}
			return hoy.AddDate(0, 0, 1-n), hoy, nil
		}
		d, err := time.ParseInLocation("2006-01-02", f[0], colombiaTZ)
		if err != nil {
			return desde, hasta, fmt.Errorf("no entendí %q", f[0])
		}
		return d, d, nil
	case len(f) == 2:
		d, err1 := time.ParseInLocation("2006-01-02", f[0], colombiaTZ)
		h, err2 := time.ParseInLocation("2006-01-02", f[1], colombiaTZ)
		if err1 != nil || err2 != nil {
			return desde, hasta, errors.New("las fechas van como AAAA-MM-DD")
		}
		if d.After(h) {
			d, h = h, d
		}
		if h.Sub(d) >= maxDiasDeuda*24*time.Hour {
			return desde, hasta, fmt.Errorf("el rango puede ser de hasta %d días", maxDiasDeuda)
		}
		return d, h, nil
	}
	return desde, hasta, errors.New("demasiados datos")
}

// comandoDeuda atiende /deuda: lo que se le debe al vendedor en un rango de días, día por día.
func (b *Bot) comandoDeuda(ctx context.Context, chatID int64, text string) {
	desde, hasta, err := rangoDeuda(strings.TrimPrefix(text, "/deuda"), time.Now())
	if err != nil {
		b.say(ctx, chatID, "No entendí el rango: "+err.Error()+".\n\n"+ayudaDeuda)
		return
	}
	b.say(ctx, chatID, "⏳ Sumando la comisión y revisando el rastreo de lo que falta por cobrar…")
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	p, err := skydropx.NewPeriod(desde.Format("2006-01-02"), hasta.Format("2006-01-02"))
	if err != nil {
		b.say(ctx, chatID, "No se pudo armar el resumen: "+err.Error())
		return
	}
	f, err := b.sky.FinanceCompleta(ctx, p, b.comisionRate())
	if err != nil {
		b.say(ctx, chatID, "No se pudo armar el resumen: "+err.Error())
		return
	}
	estados := map[string]string{} // guía -> camino | oficina | novedad
	for _, r := range f.PerShipment {
		if r.Status == "pending" {
			estados[r.Tracking] = b.sky.EstadoPorCobrar(ctx, r.Tracking, r.Carrier)
		}
	}
	var c cuentaVendedor
	if b.opts.Settings != nil {
		c = cuentaVendedor{b.opts.Settings.PaidTrackings(), b.opts.Settings.AdExpenses(), b.opts.Settings.VendorBalance()}
	}
	b.say(ctx, chatID, deudaTexto(desde, hasta, f, estados, c))
}

const ayudaDeuda = `Ejemplos:
<code>/deuda</code> → últimos 7 días
<code>/deuda 3</code> → últimos 3 días
<code>/deuda hoy</code> · <code>/deuda ayer</code>
<code>/deuda semana</code> → la semana anterior (lunes a domingo)
<code>/deuda mes</code> → lo que va del mes
<code>/deuda 2026-11-03</code> → ese día
<code>/deuda 2026-11-01 2026-11-10</code> → ese rango`

// monto son los pedidos, sus datáfonos y la comisión que suman.
type monto struct {
	pedidos, unidades int
	valor             float64
}

func (m *monto) sumar(r skydropx.ShipmentMoney) {
	m.pedidos++
	m.unidades += r.Units
	m.valor += r.Commission
}

func (m *monto) juntar(o monto) {
	m.pedidos += o.pedidos
	m.unidades += o.unidades
	m.valor += o.valor
}

// cuenta dice cuántos pedidos y datáfonos son, sin repetir cuando cada pedido es de un datáfono.
func (m monto) cuenta() string {
	if m.pedidos == m.unidades {
		return datafonos(m.unidades)
	}
	return pedidos(m.pedidos) + " = " + datafonos(m.unidades)
}

// cuentaVendedor es lo registrado a mano con el vendedor: pagos, publicidad compartida y el saldo de ambos.
type cuentaVendedor struct {
	pagadas map[string]string // guía -> día en que se le pagó
	ads     []settings.AdExpense
	saldo   float64
}

// deudaTexto arma el mensaje de /deuda en tres bloques: cuánto pagarle hoy (lo cobrado sin pagarle más
// el saldo de pagos y publicidad), el resumen por estado y el día por día con los pedidos de varios
// datáfonos. Las ventas cuentan el día en que se generó la guía; los envíos por cobrar se separan en
// camino, oficina o novedad según estados.
func deudaTexto(desde, hasta time.Time, f skydropx.Finance, estados map[string]string, c cuentaVendedor) string {
	type dia struct {
		liquidado, pagado, camino, oficina, novedad monto
		fallidos                                    int
		varios                                      []string // pedidos de más de un datáfono
	}
	iconos := map[string]string{"liquidado": "💵", "pagado": "✅", "camino": "🚚", "oficina": "🏢", "novedad": "⚠️"}
	dias := map[string]*dia{}
	var total dia
	for _, r := range f.PerShipment {
		t, err := time.Parse(time.RFC3339, r.Date)
		if err != nil {
			continue
		}
		key := t.In(colombiaTZ).Format("2006-01-02")
		d := dias[key]
		if d == nil {
			d = &dia{}
			dias[key] = d
		}
		estado := ""
		switch r.Status {
		case "collected":
			estado = "pagado"
			if c.pagadas[r.Tracking] != "" {
				estado = "liquidado"
			}
		case "pending":
			estado = "camino"
			if e := estados[r.Tracking]; e == "oficina" || e == "novedad" {
				estado = e
			}
		case "failed":
			d.fallidos++
			total.fallidos++
		}
		if estado == "" {
			continue
		}
		for _, x := range []*dia{d, &total} {
			map[string]*monto{"liquidado": &x.liquidado, "pagado": &x.pagado, "camino": &x.camino,
				"oficina": &x.oficina, "novedad": &x.novedad}[estado].sumar(r)
		}
		if r.Units > 1 {
			cliente := strings.TrimSpace(r.Customer)
			if cliente == "" {
				cliente = "Guía " + r.Tracking
			}
			d.varios = append(d.varios, fmt.Sprintf("📦 %s: %s × %s %s", html.EscapeString(cliente),
				datafonos(r.Units), formatCOP(r.Commission/float64(r.Units)), iconos[estado]))
		}
	}

	var porCobrar, ganado monto
	for _, m := range []monto{total.camino, total.oficina, total.novedad} {
		porCobrar.juntar(m)
	}
	for _, m := range []monto{total.liquidado, total.pagado, porCobrar} {
		ganado.juntar(m)
	}

	var b strings.Builder
	b.WriteString("🤝 <b>COMISIÓN DEL VENDEDOR</b>\n")
	if desde.Equal(hasta) {
		fmt.Fprintf(&b, "📅 %s %s", diasCortos[desde.Weekday()], diaCorto(desde))
	} else {
		fmt.Fprintf(&b, "📅 Del %s al %s", diaCorto(desde), diaCorto(hasta))
	}
	if ganado.pedidos == 0 {
		b.WriteString("\n\nNo hay ventas en esas fechas.\n")
		if s := saldoTexto(c.saldo); s != "" {
			b.WriteString("\n" + s)
		}
		return b.String()
	}
	fmt.Fprintf(&b, " · %s\n", ganado.cuenta())

	// 1. Cuánto pagarle hoy.
	b.WriteString("\n━━━━━━━━━━━━━━━\n")
	hoy := total.pagado.valor + c.saldo
	switch {
	case hoy > 0.5:
		fmt.Fprintf(&b, "💰 <b>PARA PAGARLE HOY: %s</b>\n", formatCOP(hoy))
	case hoy < -0.5:
		fmt.Fprintf(&b, "💰 <b>HOY NO LE DEBES: él te debe %s</b>\n", formatCOP(-hoy))
	default:
		b.WriteString("💰 <b>HOY NO LE DEBES NADA</b>\n")
	}
	if c.saldo > 0.5 || c.saldo < -0.5 {
		fmt.Fprintf(&b, "   ✅ Cobrado sin pagarle: %s\n", formatCOP(total.pagado.valor))
		if c.saldo > 0 {
			fmt.Fprintf(&b, "   ↩️ Más lo que le quedó debiendo: +%s\n", formatCOP(c.saldo))
		} else {
			fmt.Fprintf(&b, "   📣 Menos lo que él te debe (publicidad o adelantos): −%s\n", formatCOP(-c.saldo))
		}
	} else if total.pagado.pedidos > 0 {
		fmt.Fprintf(&b, "   ✅ Es lo cobrado sin pagarle (%s)\n", total.pagado.cuenta())
	}
	b.WriteString("   <i>Cuenta las ventas de estas fechas; para más días: /deuda mes</i>\n")
	b.WriteString("━━━━━━━━━━━━━━━\n")

	// 2. Resumen por estado.
	b.WriteString("\n📊 <b>RESUMEN</b>\n")
	if total.liquidado.pedidos > 0 {
		fmt.Fprintf(&b, "💵 Ya le pagaste: <b>%s</b> · %s\n", formatCOP(total.liquidado.valor), datafonos(total.liquidado.unidades))
	}
	fmt.Fprintf(&b, "✅ Cobrado, por pagarle: <b>%s</b> · %s\n", formatCOP(total.pagado.valor), datafonos(total.pagado.unidades))
	if porCobrar.pedidos > 0 {
		fmt.Fprintf(&b, "⏳ Falta que el cliente pague: <b>%s</b> · %s\n", formatCOP(porCobrar.valor), datafonos(porCobrar.unidades))
		for _, x := range []struct {
			txt string
			m   monto
		}{{"🚚 En camino", total.camino}, {"🏢 En oficina (el cliente debe reclamarlo)", total.oficina},
			{"⚠️ Con novedad o en devolución", total.novedad}} {
			if x.m.pedidos > 0 {
				fmt.Fprintf(&b, "      %s: %s · %s\n", x.txt, formatCOP(x.m.valor), datafonos(x.m.unidades))
			}
		}
	}
	if total.fallidos > 0 {
		fmt.Fprintf(&b, "❌ No se cobraron: %s (no suman comisión)\n", pedidos(total.fallidos))
	}
	fmt.Fprintf(&b, "🏆 <b>Total ganado: %s</b> · %s\n", formatCOP(ganado.valor), datafonos(ganado.unidades))
	var ads, adsVendedor float64
	for _, a := range c.ads {
		if a.Date >= desde.Format("2006-01-02") && a.Date <= hasta.Format("2006-01-02") {
			ads += a.Amount
			adsVendedor += a.VendorShare()
		}
	}
	if ads > 0 {
		fmt.Fprintf(&b, "📣 Publicidad en estas fechas: %s", formatCOP(ads))
		switch {
		case adsVendedor < -0.5:
			fmt.Fprintf(&b, " (él pone %s)\n", formatCOP(-adsVendedor))
		case adsVendedor > 0.5:
			fmt.Fprintf(&b, " (le debes %s por lo que pagó de más)\n", formatCOP(adsVendedor))
		default:
			b.WriteString(" (ya pagada mitad y mitad)\n")
		}
	}

	// 3. Día por día.
	b.WriteString("\n📅 <b>DÍA POR DÍA</b>\n")
	for t := hasta; !t.Before(desde); t = t.AddDate(0, 0, -1) {
		d := dias[t.Format("2006-01-02")]
		if d == nil {
			continue
		}
		var todo monto
		for _, m := range []monto{d.liquidado, d.pagado, d.camino, d.oficina, d.novedad} {
			todo.juntar(m)
		}
		if todo.pedidos+d.fallidos == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n<b>%s %s</b>", diasCortos[t.Weekday()], diaCorto(t))
		if todo.pedidos > 0 {
			fmt.Fprintf(&b, " · %s · %s", datafonos(todo.unidades), formatCOP(todo.valor))
		}
		var partes []string
		for _, x := range []struct {
			estado string
			m      monto
		}{{"liquidado", d.liquidado}, {"pagado", d.pagado}, {"camino", d.camino}, {"oficina", d.oficina}, {"novedad", d.novedad}} {
			if x.m.pedidos > 0 {
				partes = append(partes, fmt.Sprintf("%s %s", iconos[x.estado], formatCOP(x.m.valor)))
			}
		}
		if d.fallidos > 0 {
			partes = append(partes, fmt.Sprintf("❌ %d sin cobrar", d.fallidos))
		}
		b.WriteString("\n   " + strings.Join(partes, " · ") + "\n")
		for _, v := range d.varios {
			b.WriteString("   " + v + "\n")
		}
	}

	b.WriteString("\n<i>💵 pagado · ✅ cobrado, por pagarle · 🚚 en camino · 🏢 en oficina · ⚠️ novedad. ")
	b.WriteString("Lo que falta por cobrar se le paga cuando el cliente pague.</i>\n")
	if total.pagado.pedidos > 0 {
		b.WriteString("<i>Cuando le pagues, regístralo con /pago (ej. <code>/pago 150000</code>).</i>\n")
	}
	if f.Commission.Estimated > 0 {
		fmt.Fprintf(&b, "\n⚠️ <i>No se pudo saber la cantidad de %d pedido(s) y se contaron como 1 datáfono: si alguno era de más, la deuda es mayor. Vuelve a pedir /deuda en un rato.</i>\n", f.Commission.Estimated)
	}
	if f.Truncated {
		b.WriteString("\n<i>Hay muchos envíos: los más antiguos pueden no estar incluidos.</i>\n")
	}
	return b.String()
}

func pedidos(n int) string {
	if n == 1 {
		return "1 pedido"
	}
	return fmt.Sprintf("%d pedidos", n)
}

func datafonos(n int) string {
	if n == 1 {
		return "1 datáfono"
	}
	return fmt.Sprintf("%d datáfonos", n)
}
