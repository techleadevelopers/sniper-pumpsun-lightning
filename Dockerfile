# ============================================================
# Etapa 1 — Build
# ============================================================
FROM golang:1.25-alpine AS builder

# Ferramentas mínimas para módulos com CGO desativado
RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Copia primeiro os ficheiros de dependências para aproveitar cache do Docker
COPY go.mod go.sum ./
RUN go mod download

# Copia o resto do código
COPY . .

# Compila o binário estático
# - CGO_ENABLED=0: binário estático, corre em Alpine sem glibc
# - -ldflags="-s -w": remove símbolos e debug info (imagem mais pequena)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/sniper ./cmd/server

# ============================================================
# Etapa 2 — Runtime
# ============================================================
FROM alpine:3.21

# ca-certificates: necessário para HTTPS à PumpPortal
# tzdata: para timestamps corretos
RUN apk add --no-cache ca-certificates tzdata wget

# Utilizador não-root (boa prática)
RUN addgroup -S sniper && adduser -S sniper -G sniper

WORKDIR /app
COPY --from=builder /out/sniper /app/sniper

USER sniper

# O Railway/Render injeta a porta via $PORT; o teu código usa :8080 por defeito.
EXPOSE 8080

# Healthcheck interno
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/api/health || exit 1

ENTRYPOINT ["/app/sniper"]