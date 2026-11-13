# Cómo correr la app

## Requisitos

- Go 1.22 o superior.
- Una cuenta de Skydropx con credenciales OAuth (Client ID y Client Secret).
- Un bot de Telegram creado con @BotFather.

## Variables de entorno

Se leen desde el archivo `.env` (en local) o desde el panel de Render (en producción). Nunca van en el código ni en el repositorio.

| Variable | Obligatoria | Descripción |
|---|---|---|
| `SKYDROPX_MODE` | Sí | `mock` (simulación, no crea nada) o `live` (crea órdenes reales). |
| `SKYDROPX_CLIENT_ID` | Solo en live | Client ID de Skydropx. |
| `SKYDROPX_CLIENT_SECRET` | Solo en live | Secret de Skydropx. |
| `SKYDROPX_BASE_URL` | No | Por defecto `https://pro.skydropx.com`. |
| `APP_PASSWORD` | Recomendada | Contraseña para entrar a la app. Sin ella, no pide login (solo para uso local). |
| `TELEGRAM_BOT_TOKEN` | Para el bot | Token que te da @BotFather. Sin él, el bot queda desactivado. |
| `TELEGRAM_ALLOWED_CHAT_IDS` | Para el bot | IDs de chat autorizados, separados por coma. |
| `DEFAULT_VALOR_UNITARIO` | No | Valor unitario si el mensaje no trae `Valor:`. Por defecto `60000`. |
| `GUIA_CHECK_MINUTES` | No | Cada cuántos minutos se revisan guías nuevas. Por defecto `5`. |
| `BOT_STATE_FILE` | No | Archivo de estado del bot. Local: `data/bot_state.json`. Render: `/var/data/bot_state.json`. |
| `PORT` | No | Puerto del servidor. Por defecto `8080`. Render lo asigna solo. |

Ejemplo de `.env` para local (los valores son tuyos; no los compartas):

```
SKYDROPX_MODE=mock
SKYDROPX_CLIENT_ID=...
SKYDROPX_CLIENT_SECRET=...
APP_PASSWORD=...
TELEGRAM_BOT_TOKEN=...
TELEGRAM_ALLOWED_CHAT_IDS=...
```

## Correr en local

```
cd skydropx-automator
go run ./cmd/server
```

Abre http://localhost:8080. Si `APP_PASSWORD` está configurada, pide la contraseña.

Para ver la interfaz sin crear nada, deja `SKYDROPX_MODE=mock`. Para crear órdenes reales, cambia a `live`.

Pruebas automáticas:

```
go test ./...
```

## Configurar el bot de Telegram

1. En Telegram, habla con **@BotFather**, escribe `/newbot` y sigue los pasos. Copia el token en `TELEGRAM_BOT_TOKEN`.
2. Con `TELEGRAM_ALLOWED_CHAT_IDS` vacío, escribe al bot desde la cuenta que quieres autorizar. Te responde con tu ID de chat.
3. Pon ese ID en `TELEGRAM_ALLOWED_CHAT_IDS`. Para varias personas, sepáralos por coma: `ID_TU_CHAT,ID_CHAT_VENDEDOR`.
4. Reinicia la app.

Cualquier chat que no esté en la lista recibe su ID y no puede crear órdenes.

## Uso del bot

El vendedor escribe los datos del cliente en el chat del bot. Puede incluir una línea `Valor: 25000`; si no, se usa `DEFAULT_VALOR_UNITARIO`.

1. El bot crea la orden en Skydropx y responde en el chat.
2. Alguien finaliza el envío en Skydropx.
3. En máximo `GUIA_CHECK_MINUTES`, el chat recibe la guía, el cliente, el destino y el enlace del PDF.

## Desplegar en Render

Pasos detallados en `DEPLOY.md`. Resumen:

1. Sube el código a un repositorio privado.
2. En Render: **New > Blueprint** y elige el repositorio. Lee `render.yaml`.
3. Carga en el panel los valores marcados como secretos: credenciales de Skydropx, `APP_PASSWORD`, `TELEGRAM_BOT_TOKEN` y `TELEGRAM_ALLOWED_CHAT_IDS`.
4. **Apaga la app local** antes de usar el bot en Render.

## Problemas comunes

- **El bot no responde:** revisa que `TELEGRAM_BOT_TOKEN` esté bien copiado y que la app esté encendida.
- **Los registros dicen `error consultando Telegram` con código 409:** hay otra instancia del bot con el mismo token. Apaga la que no uses (la local o la de Render).
- **Responde con "No estás autorizado":** tu chat no está en `TELEGRAM_ALLOWED_CHAT_IDS`. Agrega el ID que te muestra el bot.
- **"Modo simulación" en lugar de orden real:** `SKYDROPX_MODE` está en `mock`. Cámbialo a `live`.
- **La guía no llega:** la orden tiene que estar finalizada en Skydropx. Revisa `GUIA_CHECK_MINUTES` y los registros del servidor.
- **Render no arranca:** revisa el build en el panel. El health check usa `/login`.
