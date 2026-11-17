package bot

import (
	"context"
	"log"

	"skydropx-automator/internal/telegram"
)

// /ayuda texts for each group, meant to be pinned in the chat. If commands are added,
// update them here and pin the /ayuda answer again.

const helpOps = `📌 <b>GESTIÓN — guía rápida</b>
<i>Toca una sección para verla completa. Los comandos en gris se copian al tocarlos: cambia el número y envía.</i>

🚚 <b>DESPACHAR</b>
<blockquote expandable>/despachos — guías que faltan por llevar a Interrapidísimo
/imprimir — guías de hoy, 4 por hoja carta
<code>/imprimir 3</code> — las de los últimos 3 días (hasta 15), útil después de un festivo
<code>/guia 240012345678</code> — dónde va un paquete y cuánto falta por cobrar</blockquote>

🔔 <b>AVISOS QUE LLEGAN SOLOS</b>
<blockquote expandable>📦 <b>Orden nueva</b> → toca 🚚 para generar la guía
⚠️ <b>Cliente con historial</b> → ya no pagó o tardó 3+ días en recoger: confirma antes de generar
🚚 <b>Recibido</b> → la transportadora ya lo tiene, va en camino
🛵 <b>En ruta</b> → llega hoy; trae el enlace para avisarle al cliente por WhatsApp
🏢 <b>En oficina</b> → avísale al cliente que lo recoja y pague
⚠️ <b>No pudieron entregar</b> → llama al cliente, la transportadora reintenta
🚨 <b>Novedad</b> → revisa con el cliente o la transportadora
💵 <b>¡Pagaron!</b> → entregado y cobrado
❌ <b>No se pudo cobrar</b> → va en devolución</blockquote>

⏰ <b>HORARIO</b>
<blockquote expandable>🌅 <b>8 a. m., lunes</b> → 🗄️ respaldo de clientes y 📊 resumen de la semana
🌅 <b>8 a. m., lunes a sábado</b> (sin festivos) → 📦 lo que falta por despachar, si hay algo
🌙 <b>8 p. m., todos los días</b> → 📊 resumen del día
🏢 <b>Oficina Interrapidísimo</b>: lunes a viernes hasta 6:30 p. m. · sábados hasta 5 p. m. · domingos y festivos cerrada</blockquote>

🆘 <b>CLIENTES CON PROBLEMAS</b>
<blockquote expandable>/novedades — envíos de los últimos 45 días para gestionar: devoluciones, intentos fallidos, en oficina y con novedad. Con teléfono, ciudad y guía
<code>/cliente 1234567890</code> — historial por cédula o teléfono: si pagó, si devolvió y si tardó en recoger</blockquote>

📊 <b>RESÚMENES</b>
<blockquote expandable>/resumen — lo de hoy y lo que va del mes
<code>/resumen semana</code> — la semana anterior, de lunes a domingo</blockquote>

🤝 <b>VENDEDOR</b> · comisión, pagos y publicidad
<blockquote expandable><b>Cuánto se le debe</b>
/deuda — últimos 7 días, día por día
<code>/deuda hoy</code> · <code>/deuda ayer</code> · <code>/deuda 3</code> · <code>/deuda semana</code> · <code>/deuda mes</code>
<code>/deuda 2026-11-03</code> · <code>/deuda 2026-11-01 2026-11-10</code>

<b>Pagos</b>
<code>/pago 150000</code> — registra lo que le pagaste: cubre lo cobrado de lo más antiguo a lo más nuevo
<code>/pago 150000 hasta ayer</code> — solo guías hasta esa fecha
/pagos — pagos y saldo · <code>/pago borrar</code> — deshace el último

<b>Publicidad</b> (mitad y mitad)
<code>/publicidad 40000</code> — la pagaste tú: se le descuenta la mitad
<code>/publicidad 40000 vendedor</code> — la pagó él: se le suma la mitad
/publicidad — ver lo registrado · <code>/publicidad borrar</code> — deshace la última

<b>Comisión por datáfono</b>
/comision — valor actual y cambios
<code>/comision 12000</code> — desde hoy · <code>/comision 12000 2026-12-01</code> — desde esa fecha
Lo vendido antes conserva el valor de su momento.</blockquote>

👥 <b>BASE DE CLIENTES</b>
<blockquote expandable>/clientes — resumen y archivo para Excel
<code>/autoriza 3001234567 respondió SÍ por WhatsApp</code> — el cliente aceptó recibir publicidad
<code>/baja 3001234567</code> — ya no quiere más mensajes
<code>/clientes respaldo</code> — copia completa; para recuperar, responde a ese archivo con <code>/clientes restaurar</code>
<code>/clientes migrar</code> — traer los clientes antiguos de Skydropx (una vez; si se repite no duplica)
Cada pedido guarda al cliente solo. Si el pedido trae <code>Autoriza datos: SÍ</code> queda autorizado, y <code>Fuente: Facebook</code> dice de dónde llegó.</blockquote>

/ayuda — ver esta guía otra vez`

const helpSeller = `📌 <b>CÓMO CREAR UN PEDIDO</b>
<i>Toca el formato para copiarlo, cambia los datos del cliente y envíalo en un solo mensaje.</i>

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

✏️ <b>CÓMO LLENARLO</b>
<blockquote expandable><b>Cantidad</b> → cuántos datáfonos lleva el pedido
<b>Valor</b> → precio por datáfono. Si no lo pones, se usa 60.000
<b>Correo</b> y <b>código postal</b> → opcionales
<b>Autoriza datos</b> → pregúntale al cliente si acepta que guardemos sus datos para enviarle ofertas, y pon SÍ o NO. Si no le preguntaste, borra la línea
<b>Fuente</b> → de dónde llegó el cliente: Facebook, WhatsApp, referido… (opcional; sin ella queda «skydropx»)
🔁 ¿Es igual a un pedido reciente pero sí es nuevo? Escribe <code>REPETIR</code> en la primera línea</blockquote>

🔔 <b>LLEGAN SOLOS</b> · no tienes que escribir nada
<blockquote expandable>📦 <b>Orden creada</b> → el pedido entró bien
✅ <b>Guía lista</b> → número, rastreo y PDF para enviarle al cliente
⚠️ <b>Cliente con historial</b> → ya no pagó o tardó en recoger antes: te aviso al crear el pedido</blockquote>

🔎 <b>PARA CONSULTAR</b> · escribe el comando
<blockquote expandable><code>/guia 240012345678</code> — en qué va un envío
<code>/cliente 1234567890</code> — historial de un cliente por cédula o teléfono, antes de venderle</blockquote>

/ayuda — ver esta guía otra vez`

// helpLabelsGroup is the help of the group where the seller receives the labels (no orders are created there).
const helpLabelsGroup = `📌 <b>GRUPO DE GUÍAS</b>
<i>Aquí llega cada guía lista para reenviársela al comprador, y todo lo que pasa con el envío.</i>

🔔 <b>LLEGAN SOLOS</b> · no tienes que escribir nada
<blockquote expandable><b>Pedido y guía</b>
📦 <b>Orden creada</b> → el pedido entró
⚠️ <b>Cliente con historial</b> → la orden llega con advertencia: ya no pagó o tardó en recoger antes
✅ <b>Guía lista</b> → número, enlace de rastreo y PDF: reenvíaselo al comprador

<b>Mientras viaja</b>
🛵 <b>En ruta</b> → llega hoy; trae el enlace para avisarle al cliente por WhatsApp
🏢 <b>En oficina</b> → llama al cliente para que lo recoja y pague
⚠️ <b>No pudieron entregar</b> → llama al cliente antes de que el paquete se devuelva
🚨 <b>Novedad</b> → revisa con el cliente

<b>Al final</b>
💵 <b>¡Pagaron!</b> → entregado y cobrado
❌ <b>No se pudo cobrar</b> → no lo recibió; trae su teléfono para llamarlo y saber por qué</blockquote>

🔎 <b>PARA CONSULTAR</b> · escribe el comando
<blockquote expandable><code>/guia 240012345678</code> — en qué va un envío, sus últimos eventos y cuánto falta por cobrar
<code>/cliente 1234567890</code> — historial de un cliente por cédula o teléfono</blockquote>

🛒 Los pedidos se crean escribiéndole al bot por chat privado.
/ayuda — ver esta guía otra vez`

// Command menus of each group (what Telegram suggests when typing "/").
var (
	opsCommands = []telegram.Command{
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
	sellerCommands = []telegram.Command{
		{Command: "guia", Description: "Ver en qué va un envío (/guia 240012345678)"},
		{Command: "cliente", Description: "Historial de un cliente (/cliente 1234567890)"},
		{Command: "ayuda", Description: "Cómo enviar un pedido"},
	}
	labelsGroupCommands = []telegram.Command{
		{Command: "guia", Description: "Ver en qué va un envío (/guia 240012345678)"},
		{Command: "cliente", Description: "Historial de un cliente (/cliente 1234567890)"},
		{Command: "ayuda", Description: "Qué llega a este grupo"},
	}
)

// setupCommands sets the command menu of your groups and the seller's at startup.
func (b *Bot) setupCommands(ctx context.Context) {
	for _, id := range b.opsChats() {
		if err := b.tg.SetChatCommands(ctx, id, opsCommands); err != nil {
			log.Printf("bot: no se pudo fijar el menú de comandos del grupo de gestión: %v", err)
		}
	}
	seller := append(append([]int64(nil), b.opts.AdminChatIDs...), b.opts.AllowedChatIDs...)
	seen := map[int64]bool{}
	for _, id := range seller {
		if seen[id] || b.isOps(id) {
			continue
		}
		seen[id] = true
		cmds := sellerCommands
		if !b.isAllowed(id) {
			cmds = labelsGroupCommands // group only for receiving labels
		}
		if err := b.tg.SetChatCommands(ctx, id, cmds); err != nil {
			log.Printf("bot: no se pudo fijar el menú de comandos del chat %d: %v", id, err)
		}
	}
}
