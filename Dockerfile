# syntax=docker/dockerfile:1
FROM node:24.10.0-bookworm-slim AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26.0-bookworm AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -o /out/qag ./cmd/qag && \
    CGO_ENABLED=0 go build -trimpath -o /out/qag-setup ./cmd/container-setup

FROM debian:bookworm-slim@sha256:7ff6ba31527b9e336162fa061746c366c4b2032f4d2cff2078bea43f78efe3f8 AS base
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/* && \
    groupadd --gid 10001 qag && useradd --uid 10001 --gid qag --no-create-home --home-dir /nonexistent qag
WORKDIR /app

FROM base AS tools
COPY --from=go-build /out/qag-setup /usr/local/bin/qag-setup
ENTRYPOINT ["/usr/local/bin/qag-setup"]

FROM base AS runtime
COPY --from=go-build /out/qag /usr/local/bin/qag
USER 10001:10001
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=4s --start-period=30s --retries=12 CMD ["/usr/local/bin/qag", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/qag"]
