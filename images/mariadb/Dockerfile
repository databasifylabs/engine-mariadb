# check=skip=InvalidDefaultArgInFrom
ARG GO_IMAGE
ARG BASE_IMAGE

FROM ghcr.io/databasifylabs/engine-sdk:1.0.0@sha256:cf066886a156193196e9d043e3f75e9084928948e40bdd950b2306f2b340d901 AS sdk

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS rclone
ARG TARGETOS
ARG TARGETARCH
ARG RCLONE_VERSION
WORKDIR /build
RUN set -eux; \
    go mod init rclone-build; \
    go get "github.com/rclone/rclone@v${RCLONE_VERSION}"; \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
        -ldflags "-s -w -X github.com/rclone/rclone/fs.Version=v${RCLONE_VERSION}" \
        -o /out/rclone github.com/rclone/rclone

FROM ${BASE_IMAGE}
ARG RELEASE
ARG SDK_VERSION
ARG LINE
ARG PACKAGES
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends $PACKAGES; \
    rm -rf /var/lib/apt/lists/* /usr/local/bin/gosu
COPY --from=rclone /out/rclone /usr/local/bin/rclone
RUN set -eux; \
    groupmod -g 64999 mysql; \
    usermod -u 64999 -g 64999 mysql; \
    find / -xdev \( -uid 999 -o -gid 999 \) -exec chown -h 64999:64999 {} +
COPY --from=sdk /databasify /usr/local/bin/databasify
LABEL org.opencontainers.image.source="https://github.com/databasifylabs/engine-mariadb" \
      io.databasify.engine="mariadb" \
      io.databasify.release="${RELEASE}" \
      io.databasify.line="${LINE}" \
      io.databasify.sdk="${SDK_VERSION}"
ENV DATABASIFY_ENGINE=mariadb \
    DATABASIFY_RELEASE=${RELEASE} \
    DATABASIFY_LINE=${LINE}
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/databasify", "serve"]
CMD []
