FROM --platform=$BUILDPLATFORM busybox:latest AS css
RUN wget -O /bulma.min.css https://cdn.jsdelivr.net/npm/bulma@1.0.4/css/bulma.min.css

FROM golang:1.25-alpine AS build
RUN --mount=type=cache,target=/var/lib/apk \
    apk add tini-static sqlite-dev build-base

WORKDIR /app
COPY . .
COPY --from=css /bulma.min.css pkg/web/ui/static/css/

# Build metadata baked into pkg/buildinfo, matching the goreleaser
# ldflags.  The release workflow passes these; a plain local build
# reports "dev".
ARG VERSION=dev
ARG COMMIT=UNKNOWN
ARG BUILD_DATE=UNKNOWN
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -o /gameday -ldflags "\
      -extldflags '-static' \
      -X github.com/gizmo-platform/gameday/pkg/buildinfo.Version=${VERSION} \
      -X github.com/gizmo-platform/gameday/pkg/buildinfo.Commit=${COMMIT} \
      -X github.com/gizmo-platform/gameday/pkg/buildinfo.BuildDate=${BUILD_DATE}" .

FROM scratch
LABEL org.opencontainers.image.source="https://github.com/gizmo-platform/gameday"
COPY --from=build /sbin/tini-static /tini
COPY --from=build /gameday /gameday
ENV GAMEDAY_DB=/data/gameday.db
ENTRYPOINT ["/tini", "/gameday"]
