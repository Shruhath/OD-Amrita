# OD-Amrita API container. River runs in-process with the API, so this image has
# one executable and one deployment entrypoint.
FROM golang:1.26 AS build
WORKDIR /src
# The api module references contracts/generated/go via go.work at repo root, so
# the build context is the repo root (see docker-compose.yml / CI).
COPY go.work go.work
COPY api/ api/
COPY contracts/ contracts/
# go.work also `use`s ./test/skeleton (the ATDD harness); it must exist in the
# build context or `go build` fails to load the workspace. Cheap to include —
# it stays in the build layer only, never the final distroless image.
COPY test/ test/
WORKDIR /src/api
RUN CGO_ENABLED=0 go build -o /out/api ./cmd/api

FROM gcr.io/distroless/static-debian12 AS runtime
COPY --from=build /out/api /app/entrypoint
# TLS-only (NFR15): the server listens on 8443; no plain-HTTP :80 listener.
EXPOSE 8443
USER 65532:65532
ENTRYPOINT ["/app/entrypoint"]
