# Build context is the REPOSITORY ROOT, not services/backend: the runtime image
# needs packages/config/connectors.yaml, which lives outside the Go module.
#
#   docker build -f services/backend/Dockerfile .

# ---- build ------------------------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /src

# Copy manifests first so the module download layer caches independently of
# source edits.
COPY services/backend/go.mod services/backend/go.sum ./
RUN go mod download

COPY services/backend/ ./

# CGO_ENABLED=0 produces a static binary, which is what lets the runtime stage
# be distroless/static rather than a full libc image.
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w -X github.com/getnyx/influaudit/backend/internal/app.Version=${VERSION}" \
        -o /out/api ./cmd/api \
 && CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w -X github.com/getnyx/influaudit/backend/internal/app.Version=${VERSION}" \
        -o /out/worker ./cmd/worker \
 && CGO_ENABLED=0 GOOS=linux go build \
        -trimpath -ldflags="-s -w" \
        -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=0 GOOS=linux go build \
        -trimpath -ldflags="-s -w" \
        -o /out/healthcheck ./cmd/healthcheck

# ---- runtime ----------------------------------------------------------------
# distroless/static holds no shell and no package manager, so a compromised
# process has nothing to pivot to. :nonroot runs as uid 65532.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

WORKDIR /app

COPY --from=build /out/api /out/worker /out/migrate /out/healthcheck /usr/local/bin/

# Runtime data. Paths match the defaults in platform/config, which are resolved
# relative to the working directory.
COPY services/backend/migrations/ /app/migrations/
COPY packages/config/ /app/packages/config/
# The generated OpenAPI document, served at /openapi.yaml and behind /swagger
# outside production. Regenerate with `go run ./cmd/openapigen`.
COPY packages/contracts/openapi/ /app/packages/contracts/openapi/

USER nonroot:nonroot

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/api"]
