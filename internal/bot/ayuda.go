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

⏰ <b>MENSAJES AUTOMÁTICOS</b> (llegan en este orden)
8 a. m., lunes: 🗄️ respaldo de clientes y 📊 resumen de la semana
8 a. m., lunes a sábado sin festivos: 📦 guías por despachar (solo si hay alguna)
8 p. m., todos los días: 📊 resumen del día
Oficina de Interrapidísimo: lunes a viernes hasta las 6:30 p. m., sábados hasta las 5 p. m.; domingos y festivos cerrada.

📊 <b>RESÚMENES</b>
/resumen → lo de hoy y lo que va del mes
/resumen semana → la semana anterior (lunes a domingo)

📋 <b>NOVEDADES PARA GESTIONAR</b>
/despachos → guías creadas que todavía no se han llevado a Interrapidísimo
/novedades → envíos de los últimos 45 días que hay que gestionar con el cliente: en devolución o sin cobrar, intentos de entrega fallidos, en oficina y con novedad. Trae teléfono, ciudad, guía y último evento.
/guia 240012345678 → estado de esa guía, últimos eventos y cuánto falta por cobrar
/cliente 1234567890 → historial de un cliente por cédula o teléfono: qué pagó, qué devolvió y si tardó en recoger en oficina

👥 <b>BASE DE CLIENTES</b>
/clientes → resumen y archivo para Excel con todos los clientes
/clientes migrar → traer los clientes de los envíos anteriores en Skydropx (se hace una vez; si se repite no duplica)
/clientes respaldo → copia completa de la base; para recuperar, responde a ese archivo con /clientes restaurar
/autoriza 3001234567 respondió SÍ por WhatsApp → registrar que el cliente autorizó publicidad
/baja 3001234567 → el cliente no quiere más mensajes
Cada pedido nuevo guarda al cliente solo; si el pedido trae "Autoriza datos: SÍ", queda autorizado, y "Fuente:" dice de dónde llegó.

🤝 <b>COMISIÓN DEL VENDEDOR</b>
/deuda → lo que se le debe en los últimos 7 días, día por día
/deuda 3 → últimos 3 días · /deuda hoy · /deuda ayer
/deuda semana → la semana anterior · /deuda mes → lo que va del mes
/deuda 2026-11-03 → ese día · /deuda 2026-11-01 2026-11-10 → ese rango
/pago 150000 → registrar lo que le pagaste: marca como pagado lo cobrado, de lo más antiguo a lo más nuevo, hasta donde alcance
/pago 150000 hasta ayer → solo lo cobrado de guías creadas hasta ayer (o hasta 2026-11-15)
/pago borrar → borrar el último pago · /pagos → ver los pagos y el saldo
/publicidad 40000 → publicidad que pagaste tú: él pone la mitad (se descuenta de su comisión)
/publicidad 40000 vendedor → la pagó él: se le suma la mitad · se puede agregar fecha y nota
/publicidad → ver lo registrado · /publicidad borrar → borrar la última
/comision → ver el valor actual por datáfono y los cambios
/comision 12000 → cambiarlo desde hoy
/comision 12000 2026-12-01 → cambiarlo desde esa fecha
Las ventas anteriores conservan el valor de su momento.

🔔 <b>AVISOS AUTOMÁTICOS</b>
📦 Orden nueva → con el botón 🚚 para generar la guía
⚠️ Si el cliente ya no pagó o tardó 3 días o más en recoger en oficina, la orden llega con advertencia y hay que confirmar antes de generar la guía
🚚 La transportadora recibió el paquete → ya va en camino
🛵 En ruta de entrega → llega hoy, con el enlace para avisarle al cliente por WhatsApp
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
Valor: 60000
Autoriza datos: SÍ
Fuente: Facebook</code>

• "Valor" es por datáfono. Si no lo pones, se usa 60.000.
• Correo y código postal son opcionales.
• "Autoriza datos": pregúntale al cliente si acepta que guardemos sus datos para enviarle ofertas, y pon SÍ o NO. Si no lo preguntaste, quita la línea.
• "Fuente": de dónde llegó el cliente (Facebook, WhatsApp, referido…). Es opcional.

🔁 Si es un pedido igual a uno reciente y SÍ es nuevo, escribe REPETIR en la primera línea.

🔔 <b>AQUÍ TE LLEGA</b>
📦 La orden creada
✅ La guía lista, con el número, el rastreo y el PDF para enviarle al cliente

⚠️ Si el cliente ya no pagó o tardó en recoger en oficina antes, te aviso al crear el pedido

/guia 240012345678 → ver en qué va un envío
/cliente 1234567890 → ver el historial de un cliente (cédula o teléfono) antes de venderle
/ayuda → ver esta ayuda`

// ayudaGrupoGuias es la ayuda del grupo donde el vendedor recibe las guías (no crea pedidos ahí).
const ayudaGrupoGuias = `📌 <b>GRUPO DE GUÍAS</b>

Aquí llega cada guía lista para que se la reenvíes al comprador:
📦 La orden creada
✅ La guía con el número, el enlace de rastreo y el PDF
🛵 En ruta de entrega → el paquete llega hoy, con el enlace para avisarle al cliente por WhatsApp
💵 ¡Pagaron! → el paquete se entregó y el cliente pagó
⚠️ No pudieron entregar · 🏢 Quedó en la oficina · 🚨 Novedad → llama al cliente antes de que el paquete se devuelva
❌ No se pudo cobrar → el cliente no recibió; trae su teléfono para llamarlo y saber por qué

/guia 240012345678 → ver en qué va un envío, sus últimos eventos y cuánto falta por cobrar
/cliente 1234567890 → historial de un cliente por cédula o teléfono

🛒 Los pedidos se crean escribiéndole al bot por chat privado, con los datos del cliente.

/ayuda → ver esta ayuda`

// Menús de comandos de cada grupo (lo que Telegram sugiere al escribir "/").
var (
	comandosGestion = []telegram.Command{
		{Command: "despachos", Description: "Guías que faltan por llevar a Interrapidísimo"},
		{Command: "novedades", Description: "Envíos para gestionar: devoluciones, intentos fallidos, oficina"},
		{Command: "guia", Description: "Estado de una guía (/guia 240012345678)"},
		{Command: "cliente", Description: "Historial de un cliente por cédula o teléfono"},
		{Command: "clientes", Description: "Base de clientes: resumen y archivo para Excel"},
		{Command: "autoriza", Description: "Registrar que un cliente autorizó publicidad"},
		{Command: "baja", Description: "Un cliente no quiere más mensajes"},
		{Command: "resumen", Description: "Resumen de hoy y del mes (/resumen semana: la anterior)"},
		{Command: "imprimir", Description: "Guías de hoy en hojas de 4 (/imprimir 3: últimos 3 días)"},
		{Command: "deuda", Description: "Lo que se le debe al vendedor (/deuda 3, semana, mes o fechas)"},
		{Command: "pago", Description: "Registrar un pago al vendedor (/pago 150000 hasta ayer)"},
		{Command: "pagos", Description: "Ver los pagos al vendedor y el saldo"},
		{Command: "publicidad", Description: "Registrar publicidad mitad y mitad (/publicidad 40000)"},
		{Command: "comision", Description: "Ver o cambiar la comisión del vendedor por datáfono"},
		{Command: "ayuda", Description: "Ver todos los comandos y avisos"},
	}
	comandosVendedor = []telegram.Command{
		{Command: "guia", Description: "Ver en qué va un envío (/guia 240012345678)"},
		{Command: "cliente", Description: "Historial de un cliente (/cliente 1234567890)"},
		{Command: "ayuda", Description: "Cómo enviar un pedido"},
	}
	comandosGrupoGuias = []telegram.Command{
		{Command: "guia", Description: "Ver en qué va un envío (/guia 240012345678)"},
		{Command: "cliente", Description: "Historial de un cliente (/cliente 1234567890)"},
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
