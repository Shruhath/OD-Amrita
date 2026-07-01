# OD-Amrita api/ container — always-on target (River needs a persistent worker,
# so this is NOT serverless). Multi-stage build; ARG selects which entrypoint
# (api server or worker) the image runs. Skeleton-ready; refined as the surface
# gains content.
FROM golang:1.26 AS build
WORKDIR /src
# The api module references contracts/generated/go via go.work at repo root, so
# the build context is the repo root (see docker-compose.yml / CI).
COPY go.work go.work
COPY api/ api/
COPY contracts/ contracts/
WORKDIR /src/api
RUN CGO_ENABLED=0 go build -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -o /out/worker ./cmd/worker

FROM gcr.io/distroless/static-debian12 AS runtime
# ENTRYPOINT_BIN selects api|worker at deploy time.
ARG ENTRYPOINT_BIN=api
COPY --from=build /out/${ENTRYPOINT_BIN} /app/entrypoint
# TLS-only (NFR15): the server listens on 8443; no plain-HTTP :80 listener.
EXPOSE 8443
USER 65532:65532
ENTRYPOINT ["/app/entrypoint"]
