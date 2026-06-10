# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS builder
WORKDIR /src

COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal

ARG VERSION=unknown
ARG BUILD_DATE=unknown
# CGO_ENABLED=0 is required: CCC copies fusermount3 into Ubuntu-based images
# without Alpine/musl libraries, so published binaries must be fully static.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.buildDate=${BUILD_DATE}" -o /out/ccc-fuse-sidecar ./cmd/ccc-fuse-sidecar && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.buildDate=${BUILD_DATE}" -o /out/client/fusermount3 ./cmd/fusermount3 && \
    ln -s fusermount3 /out/client/fusermount && \
    ln -s fusermount3 /out/client/fusemount3 && \
    ln -s fusermount3 /out/client/fusemount

FROM scratch AS sidecar
COPY --from=builder /out/ccc-fuse-sidecar /usr/local/bin/ccc-fuse-sidecar
ENTRYPOINT ["/usr/local/bin/ccc-fuse-sidecar"]

FROM scratch AS client
COPY --from=builder /out/client/ /usr/local/bin/
