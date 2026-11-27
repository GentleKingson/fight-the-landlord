# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN --mount=type=secret,id=proxy_ca \
    if [ -f /run/secrets/proxy_ca ]; then export NODE_EXTRA_CA_CERTS=/run/secrets/proxy_ca; fi; npm ci
COPY web/ ./
COPY internal/protocol/proto/ /src/internal/protocol/proto/
COPY testdata/classic-rules.json /src/testdata/classic-rules.json
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=secret,id=proxy_ca \
    if [ -f /run/secrets/proxy_ca ]; then export SSL_CERT_FILE=/run/secrets/proxy_ca; fi; go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags production -trimpath -ldflags="-s -w" -o /server ./cmd/server

FROM debian:bookworm-slim
RUN mkdir -p /app/data/games && chown -R 65532:65532 /app
COPY --chown=65532:65532 --from=server /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --chown=65532:65532 --from=server /server /app/server
COPY --chown=65532:65532 --from=web /src/web/dist /app/web
COPY --chown=65532:65532 config.yaml /app/config.yaml
WORKDIR /app
ENV WEB_DIR=/app/web TRAINING_DATA_DIR=/app/data/games
USER 65532:65532
EXPOSE 1780
CMD ["/app/server"]
