# Automatización de Envíos Skydropx desde WhatsApp (MVP)

Aplicación web monolítica desarrollada en **Go** (sin dependencias externas pesadas) para extraer automáticamente los datos de destinatarios desde mensajes de WhatsApp, validar la información, prevenir duplicados mediante idempotencia y generar guías de envío en **Skydropx Colombia** (priorizando **Interrapidísimo** desde **Bello, Antioquia**).

## Características principales
- **3 Pantallas integradas (SPA):**
  1. **Pegar mensaje:** Validación de entrada no vacía.
  2. **Revisar y editar:** Formulario editable con alertas de campos obligatorios faltantes, normalización automática de ciudades/departamentos de Colombia y códigos postales DANE.
  3. **Resultado y guía:** Visualización del estado, transportadora, número de guía, enlace de descarga/impresión y manejo de errores.
- **Seguridad y control:**
  - Documentos y teléfonos tratados estrictamente como texto (`string`) para conservar ceros a la izquierda.
  - Control de **Idempotencia** mediante `Idempotency-Key` (UUID v4) para evitar cobros o guías dobles por doble clic o reintentos.
  - Credenciales aisladas en el backend (nunca expuestas al navegador ni en logs).
  - Modo `mock` integrado para probar el flujo completo sin necesidad de credenciales reales de Skydropx.

## Requisitos previos
- **Go 1.22** o superior instalado.

## Ejecución rápida (Modo Pruebas / Mock)

1. Descarga dependencias:
   ```bash
   go mod tidy
   ```

2. Crea tu archivo `.env` a partir de la plantilla (opcional, por defecto arranca en modo `mock`):
   ```bash
   cp .env.example .env
   ```

3. Inicia el servidor:
   ```bash
   go run ./cmd/server
   ```

4. Abre tu navegador en:
   `http://localhost:8080`

## Conexión con Skydropx en Producción
1. Verifica en tu cuenta de [Skydropx Colombia](https://app.skydropx.com/co/es-CO/api-docs) tu `API Key` y el `template_id` (si aplica).
2. Edita las variables en tu entorno o `.env`:
   - `SKYDROPX_MODE=live`
   - `SKYDROPX_API_KEY=<tu_llave_secreta>`
   - Verifica la dirección exacta y código postal de origen en **Bello, Antioquia** (`051050`).
3. Revisa `internal/skydropx/client.go` para ajustar los campos exactos del payload JSON según la versión de API habilitada en tu cuenta (cotización + selección de tarifa Interrapidísimo + emisión de etiqueta).
# skydropx-automator
# skydropx-automator
