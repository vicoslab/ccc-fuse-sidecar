ARG CCC_FUSE_CLIENT_IMAGE=ccc-fuse-sidecar:test-client

FROM ${CCC_FUSE_CLIENT_IMAGE} AS ccc_fuse_client

FROM ubuntu:20.04

ENV DEBIAN_FRONTEND=noninteractive
ENV LANG=C.UTF-8 LC_ALL=C.UTF-8
ENV CCC_FUSE_SIDECAR_SOCKET=/run/ccc-fuse-sidecar/fuse.sock

RUN apt-get update && \
    apt-get install -y --no-install-recommends archivemount tar ca-certificates && \
    rm -rf /var/lib/apt/lists/* && \
    mkdir -p /tmp/archive-src /run/ccc-fuse-sidecar /mnt/ccc-fuse && \
    printf 'hello\n' > /tmp/archive-src/hello.txt && \
    tar -cf /tmp/test.tar -C /tmp/archive-src .

COPY --from=ccc_fuse_client /usr/local/bin/fusermount3 /opt/ccc-fuse-sidecar/bin/fusermount3

RUN set -eux; \
    for dir in /usr/local/bin /usr/bin /bin; do \
        mkdir -p "$dir"; \
        ln -sfn /opt/ccc-fuse-sidecar/bin/fusermount3 "$dir/fusermount3"; \
        ln -sfn /opt/ccc-fuse-sidecar/bin/fusermount3 "$dir/fusermount"; \
        ln -sfn /opt/ccc-fuse-sidecar/bin/fusermount3 "$dir/fusemount3"; \
        ln -sfn /opt/ccc-fuse-sidecar/bin/fusermount3 "$dir/fusemount"; \
    done

CMD ["bash", "-lc", "echo test image built"]
