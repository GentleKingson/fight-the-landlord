ARG GO_VERSION=1.26
ARG NODE_VERSION=22
ARG VERSION=dev
ARG GO_REGISTRY=docker.io/library
ARG GO_VARIANT=-alpine
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot

# Build the Vite client first. The release version is written into index.html
# and is also exposed by the Go /version endpoint.
FROM node:${NODE_VERSION}-alpine AS web-builder

ARG VERSION
ENV VITE_APP_VERSION=${VERSION}
WORKDIR /src/web

COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY web/ ./
RUN npm run build

# Build a static Go server with the web distribution embedded.
FROM ${GO_REGISTRY}/golang:${GO_VERSION}${GO_VARIANT} AS server-builder

USER root
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=web-builder /src/web/dist ./web/dist

ARG VERSION
RUN CGO_ENABLED=0 GOOS=linux go build \
    -tags=webui \
    -trimpath \
    -ldflags="-w -s -X main.version=${VERSION}" \
    -o /out/server ./cmd/server

# Distroless-style runtime with CA certificates and no shell/package manager.
FROM ${RUNTIME_IMAGE}

ARG VERSION
LABEL org.opencontainers.image.title="fight-the-landlord" \
      org.opencontainers.image.version="${VERSION}"

WORKDIR /app
ENV TZ=UTC

COPY --from=server-builder /out/server /app/server
COPY config.yaml /app/config.yaml

EXPOSE 1780

HEALTHCHECK --interval=10s --timeout=5s --start-period=10s --retries=5 \
    CMD ["/app/server", "-healthcheck"]

CMD ["/app/server"]
