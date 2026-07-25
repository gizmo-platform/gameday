FROM golang:1.24-alpine AS build
RUN --mount=type=cache,target=/var/lib/apk \
    apk add tini-static sqlite-dev build-base

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -o /gameday -ldflags '-extldflags "-static"' .

FROM scratch
COPY --from=build /sbin/tini-static /tini
COPY --from=build /gameday /gameday
ENV GAMEDAY_DB=/data/gameday.db
ENTRYPOINT ["/tini", "/gameday"]
