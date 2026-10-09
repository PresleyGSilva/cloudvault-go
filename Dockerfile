FROM golang:1.27-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /api ./cmd/api

FROM alpine:3.20
WORKDIR /app
COPY --from=build /api /api
COPY migrations ./migrations
COPY openapi.yaml ./openapi.yaml
EXPOSE 8087
HEALTHCHECK --interval=10s --timeout=3s --retries=5 CMD wget -qO- http://localhost:${PORT:-8087}/healthz || exit 1
CMD ["/api"]
