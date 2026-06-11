FROM vicoslab/ccc-fuse-sidecar:client-latest AS ccc_fuse_client
FROM vicoslab/ccc-fuse-sidecar:latest AS ccc_fuse

FROM ubuntu:20.04

ENV DEBIAN_FRONTEND noninteractive

ENV LANG=C.UTF-8 LC_ALL=C.UTF-8
ENV BASE=/home
ENV CCC_FUSE_SIDECAR_SOCKET=/run/ccc-fuse-sidecar/fuse.sock

RUN apt-get update && \
    apt-get install -yf archivemount tar && \
    mkdir -p /tmp/archive-src /mnt/archive && \
    echo hello > /tmp/archive-src/hello.txt && \
    tar -cf /tmp/test.tar -C /tmp/archive-src .

RUN mkdir -p /run/ccc-fuse-sidecar

COPY --from=ccc_fuse /usr/local/bin/ccc-fuse-sidecar /usr/local/bin/ccc-fuse-sidecar
COPY --from=ccc_fuse_client /usr/local/bin/fusermount3 /opt/ccc-fuse-sidecar/bin/fusermount3

RUN set -eux; \
    for dir in /usr/local/bin /usr/bin /bin; do \
        mkdir -p "$dir"; \
        ln -sfn /opt/ccc-fuse-sidecar/bin/fusermount3 "$dir/fusermount3"; \
        ln -sfn /opt/ccc-fuse-sidecar/bin/fusermount3 "$dir/fusermount"; \
        ln -sfn /opt/ccc-fuse-sidecar/bin/fusermount3 "$dir/fusemount3"; \
        ln -sfn /opt/ccc-fuse-sidecar/bin/fusermount3 "$dir/fusemount"; \
    done