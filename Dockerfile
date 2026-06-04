# syntax=docker/dockerfile:1

FROM golang:1.23-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/family-finances ./cmd/server

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata curl \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --home-dir /app --shell /usr/sbin/nologin app

WORKDIR /app
COPY --from=build /out/family-finances /app/family-finances

ENV SERVER_ADDR=:8787 \
    DATABASE_PATH=/data/family.db \
    TZ=Asia/Shanghai

RUN mkdir -p /data && chown -R app:app /data /app
USER app

EXPOSE 8787
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -fsS http://127.0.0.1:8787/healthz >/dev/null || exit 1

ENTRYPOINT ["/app/family-finances"]
