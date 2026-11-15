package bot

import (
	"context"
	"log"

	"skydropx-automator/internal/telegram"
)

// Textos de /ayuda de cada grupo, pensados para fijarlos en el chat. Si se agregan comandos,
// se actualizan aquí y se vuelve a fijar la respuesta de /ayuda.

const ayudaGestion = `📌 <b>GRUPO DE GESTIÓN — Comandos y avisos</b>

🖨️ <b>IMPRIMIR GUÍAS</b>
/imprimir → guías de hoy en hojas carta, 4 por hoja
/imprimir 3 → guías de los últimos 3 días (hasta 15). Útil después de un festivo

📊 <b>RESÚMENES</b>
/resumen → lo de hoy y lo que va del mes
/resumen semana → la semana anterior (lunes a domingo)
Llegan solos: cada noche a las 8 p. m. y los lunes a las 8 a. m.

📋 <b>NOVEDADES PARA GESTIONAR</b>
/novedades → envíos de los últimos 45 días que hay que gestionar con el cliente: en devolución o sin cobrar, intentos de entrega fallidos, en oficina y con novedad. Trae teléfono, ciudad, guía y último evento.

🤝 <b>COMISIÓN DEL VENDEDOR</b>
/deuda → lo que se le debe en los últimos 7 días, día por día
/deuda 3 → últimos 3 días · /deuda hoy · /deuda ayer
/deuda semana → la semana anterior · /deuda mes → lo que va del mes
/deuda 2026-11-03 → ese día · /deuda 2026-11-01 2026-11-10 → ese rango
/comision → ver el valor actual por datáfono y los cambios
/comision 12000 → cambiarlo desde hoy
/comision 12000 2026-12-01 → cambiarlo desde esa fecha
Las ventas anteriores conservan el valor de su momento.

🔔 <b>AVISOS AUTOMÁTICOS</b>
📦 Orden nueva → con el botón 🚚 para generar la guía
🚚 La transportadora recibió el paquete → ya va en camino
💵 ¡Pagaron! → el paquete se entregó y el cliente pagó
❌ No se pudo cobrar → el paquete va en devolución
⚠️ No pudieron entregar → llama al cliente, la transportadora reintenta
🏢 Quedó en la oficina → avísale al cliente que lo recoja y pague
🚨 Novedad con el envío → revisa con el cliente o la transportadora

/ayuda → ver esta ayuda`

const ayudaVendedor = `📌 <b>CÓMO CREAR UN PEDIDO</b>

Envía los datos del cliente en un solo mensaje, así:

<code>Nombre: Ana Pérez
Cédula: 1234567890
Teléfono: 3001234567
Departamento: Córdoba
Ciudad: Montería
Barrio: El Centro
Dirección: Calle 10 # 5-20
Cantidad: 2
Valor: 60000</code>

• "Valor" es por datáfono. Si no lo pones, se usa 60.000.
• Correo y código postal son opcionales.

🔁 Si es un pedido igual a uno reciente y SÍ es nuevo, escribe REPETIR en la primera línea.

🔔 <b>AQUÍ TE LLEGA</b>
📦 La orden creada
✅ La guía lista, con el número, el rastreo y el PDF para enviarle al cliente

/ayuda → ver esta ayuda`

// ayudaGrupoGuias es la ayuda del grupo donde el vendedor recibe las guías (no crea pedidos ahí).
const ayudaGrupoGuias = `📌 <b>GRUPO DE GUÍAS</b>

Aquí llega cada guía lista para que se la reenvíes al comprador:
📦 La orden creada
✅ La guía con el número, el enlace de rastreo y el PDF

🛒 Los pedidos se crean escribiéndole al bot por chat privado, con los datos del cliente.

/ayuda → ver esta ayuda`

// Menús de comandos de cada grupo (lo que Telegram sugiere al escribir "/").
var (
	comandosGestion = []telegram.Command{
		{Command: "novedades", Description: "Envíos para gestionar: devoluciones, intentos fallidos, oficina"},
		{Command: "resumen", Description: "Resumen de hoy y del mes (/resumen semana: la anterior)"},
		{Command: "imprimir", Description: "Guías de hoy en hojas de 4 (/imprimir 3: últimos 3 días)"},
		{Command: "deuda", Description: "Lo que se le debe al vendedor (/deuda 3, semana, mes o fechas)"},
		{Command: "comision", Description: "Ver o cambiar la comisión del vendedor por datáfono"},
		{Command: "ayuda", Description: "Ver todos los comandos y avisos"},
	}
	comandosVendedor = []telegram.Command{
		{Command: "ayuda", Description: "Cómo enviar un pedido"},
	}
	comandosGrupoGuias = []telegram.Command{
		{Command: "ayuda", Description: "Qué llega a este grupo"},
	}
)

// configurarComandos fija el menú de comandos de tus grupos y del vendedor al arrancar.
func (b *Bot) configurarComandos(ctx context.Context) {
	for _, id := range b.gestionChats() {
		if err := b.tg.SetChatCommands(ctx, id, comandosGestion); err != nil {
			log.Printf("bot: no se pudo fijar el menú de comandos del grupo de gestión: %v", err)
		}
	}
	vendedor := append(append([]int64(nil), b.opts.AdminChatIDs...), b.opts.AllowedChatIDs...)
	visto := map[int64]bool{}
	for _, id := range vendedor {
		if visto[id] || b.isGestion(id) {
			continue
		}
		visto[id] = true
		cmds := comandosVendedor
		if !b.isAllowed(id) {
			cmds = comandosGrupoGuias // grupo solo para recibir guías
		}
		if err := b.tg.SetChatCommands(ctx, id, cmds); err != nil {
			log.Printf("bot: no se pudo fijar el menú de comandos del chat %d: %v", id, err)
		}
	}
}
