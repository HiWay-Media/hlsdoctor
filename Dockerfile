# hlsdoctor as a container: the same static binary the releases ship, on distroless.
#
#   docker build --build-arg VERSION=v0.1.0 -t hlsdoctor .
#   docker run --rm hlsdoctor check https://cdn.example.com/live/master.m3u8
#   docker run --rm -v "$PWD/streams.txt:/streams.txt:ro" hlsdoctor check --from /streams.txt --json
#
# The build stage runs on the builder's own platform and cross-compiles with GOOS/GOARCH,
# so a multi-arch build needs no emulation: the final stage has no RUN.

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/hiway-media/hlsdoctor/internal/version.Version=$VERSION" \
      -o /out/hlsdoctor ./cmd/hlsdoctor

# static-debian13: CA certificates for https and rtmps, tzdata, a nonroot user, no shell.
FROM gcr.io/distroless/static-debian13:nonroot
ARG VERSION=dev
LABEL org.opencontainers.image.title="hlsdoctor" \
      org.opencontainers.image.description="Probes an HLS or RTMP stream the way a player would; read-only" \
      org.opencontainers.image.source="https://github.com/hiway-media/hlsdoctor" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="$VERSION"
COPY --from=build /out/hlsdoctor /usr/local/bin/hlsdoctor
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/hlsdoctor"]
CMD ["version"]
