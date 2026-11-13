# Compila el servidor Go y lo ejecuta junto con la interfaz (carpeta web/).
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM alpine:3.20
RUN adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /out/server /app/server
COPY web /app/web
USER app
# El servicio de hosting define PORT; 8080 es el valor por defecto.
ENV PORT=8080
EXPOSE 8080
CMD ["/app/server"]
