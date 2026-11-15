package bot

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

🤝 <b>COMISIÓN DEL VENDEDOR</b>
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
