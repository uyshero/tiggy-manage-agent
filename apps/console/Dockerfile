ARG NODE_BASE_IMAGE=public.ecr.aws/docker/library/node:22-alpine
ARG NGINX_BASE_IMAGE=public.ecr.aws/docker/library/nginx:1.27-alpine

FROM ${NODE_BASE_IMAGE} AS build
WORKDIR /src

COPY sdk/typescript ./sdk/typescript
RUN npm --prefix sdk/typescript ci

COPY apps/console ./apps/console
RUN npm --prefix apps/console ci

ARG VITE_TMA_API_BASE_URL=
ARG TMA_CONSOLE_BASE_PATH=/
ENV VITE_TMA_API_BASE_URL=${VITE_TMA_API_BASE_URL}
ENV TMA_CONSOLE_BASE_PATH=${TMA_CONSOLE_BASE_PATH}
RUN npm --prefix apps/console run build

FROM ${NGINX_BASE_IMAGE} AS runtime
COPY apps/console/nginx.conf /etc/nginx/nginx.conf
COPY --from=build /src/apps/console/dist /usr/share/nginx/html
USER nginx
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q -O - http://127.0.0.1:8080/healthz || exit 1
CMD ["nginx", "-g", "daemon off;"]
