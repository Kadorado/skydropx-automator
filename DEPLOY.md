# Despliegue en Render

## Antes de empezar

1. **Rota el secret de Skydropx** en Conexiones > API. El anterior se pegó en un chat, así que hay que reemplazarlo.
2. Ten a mano el **token del bot** de Telegram y los **IDs de chat** autorizados (el tuyo y el del vendedor), separados por coma.
3. Elige una **contraseña larga** para `APP_PASSWORD`.

## Pasos

1. Sube este código a un repositorio privado de GitHub.
2. En Render: **New > Blueprint** y selecciona ese repositorio. Render lee `render.yaml`.
3. Carga los valores secretos en el panel del servicio:
   - `SKYDROPX_CLIENT_ID` y `SKYDROPX_CLIENT_SECRET` (el secret nuevo)
   - `APP_PASSWORD`
   - `TELEGRAM_BOT_TOKEN`
   - `TELEGRAM_ALLOWED_CHAT_IDS` (por ejemplo `ID_TU_CHAT,ID_CHAT_VENDEDOR`)
4. Espera a que termine el despliegue. El health check usa `/login`.
5. **Apaga el servidor local** antes de probar en Render. Telegram solo permite una conexión por bot: si el computador y Render están activos a la vez, los mensajes no llegan a ninguno de los dos.

## Verificación

- Abre la URL del servicio: debe pedir la contraseña.
- Entra y revisa la pestaña **Envíos**.
- Escribe al bot desde un chat autorizado y confirma que responde.

## Notas

- El disco `/var/data` guarda `bot_state.json`, con las órdenes pendientes de guía. Si se pierde, el bot puede volver a procesar mensajes viejos.
- El plan `starter` tiene costo mensual. Sin él no hay disco persistente y el servicio se duerme.
- La imagen Docker se construye en Render a partir del `Dockerfile`.
