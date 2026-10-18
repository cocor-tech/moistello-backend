FROM golang:1.25.3-alpine@sha256:aee43c3ccbf24fdffb7295693b6e33b21e01baec1b2a55acc351fde345e9ec34 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /moistello-api ./cmd/api-server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
	&& addgroup -S app \
	&& adduser -S -G app app
COPY --from=builder --chown=app:app /moistello-api /usr/bin/moistello-api
COPY --chown=app:app config/ /etc/moistello/
COPY --chown=app:app internal/database/migrations/ /etc/moistello/migrations/
EXPOSE 1100
USER app
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD wget -qO- http://127.0.0.1:1100/health/live >/dev/null || exit 1
ENTRYPOINT ["/usr/bin/moistello-api"]
