ARG GO_BASE_IMAGE=public.ecr.aws/docker/library/golang:1.25.0-alpine
ARG ALPINE_BASE_IMAGE=public.ecr.aws/docker/library/alpine:3.22
ARG POSTGRES_BASE_IMAGE=public.ecr.aws/docker/library/postgres:16-alpine

FROM ${GO_BASE_IMAGE} AS build
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/tma-server ./cmd/tma-server \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/tma-worker ./cmd/tma-worker \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/tma-biography-voice-gateway ./cmd/tma-biography-voice-gateway \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/tma ./cmd/tma

FROM ${ALPINE_BASE_IMAGE} AS runtime-base
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 10001 -S tma \
    && adduser -u 10001 -S -D -H -G tma tma
WORKDIR /opt/tma

FROM runtime-base AS server-base
ARG APK_REPOSITORY=
USER root
RUN if [ -n "${APK_REPOSITORY}" ]; then \
        sed -i "s#https://dl-cdn.alpinelinux.org/alpine#${APK_REPOSITORY}#g" /etc/apk/repositories; \
    fi \
    && apk add --no-cache font-noto-cjk libreoffice-writer
USER 10001:10001

FROM server-base AS server
COPY --from=build /out/tma-server /usr/local/bin/tma-server
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/tma-server"]

# Docker single-host deployments use this target for the current cloud_sandbox
# provider. Access to the Docker socket is equivalent to host-root access.
FROM server-base AS server-docker
USER root
RUN apk add --no-cache docker-cli
COPY --from=build /out/tma-server /usr/local/bin/tma-server
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/tma-server"]

FROM runtime-base AS biography-voice-gateway
COPY --from=build /out/tma-biography-voice-gateway /usr/local/bin/tma-biography-voice-gateway
USER 10001:10001
EXPOSE 8091
ENTRYPOINT ["/usr/local/bin/tma-biography-voice-gateway"]

FROM runtime-base AS worker
USER root
RUN apk add --no-cache bash curl git jq python3
COPY --from=build /out/tma-worker /usr/local/bin/tma-worker
USER 10001:10001
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/tma-worker"]

FROM worker AS browser-extension-worker
USER root
COPY extensions/browser-tool-plugin/browser-plugin.py /opt/tma/plugins/browser-plugin.py
RUN chmod 0555 /opt/tma/plugins/browser-plugin.py
ENV TMA_WORKER_PLUGINS=/opt/tma/plugins/browser-plugin.py
USER 10001:10001

FROM runtime-base AS cli
COPY --from=build /out/tma /usr/local/bin/tma
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/tma"]

FROM ${POSTGRES_BASE_IMAGE} AS migrate
COPY sql/baselines/000101_baseline.sql /opt/tma/sql/000101_baseline.sql
COPY deploy/postgres/runtime-grants.sql /opt/tma/sql/runtime-grants.sql
