ARG NODE_BASE_IMAGE=public.ecr.aws/docker/library/node:20-bookworm-slim
FROM ${NODE_BASE_IMAGE}

ARG PLAYWRIGHT_NPM_VERSION=1.54.1

ENV NODE_ENV=production
ENV NODE_PATH=/opt/tma-browser-runner/node_modules
ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
ENV TMA_BROWSER_EXECUTABLE_PATH=/usr/bin/chromium

WORKDIR /opt/tma-browser-runner

RUN npm init -y >/dev/null \
  && apt-get update \
  && apt-get install -y --no-install-recommends \
    chromium \
    fonts-noto-cjk \
    fonts-noto-color-emoji \
    ca-certificates \
  && ln -sf /usr/bin/chromium /usr/bin/chromium-browser \
  && ln -sf /usr/bin/chromium /usr/bin/google-chrome \
  && npm install --omit=dev "playwright-core@${PLAYWRIGHT_NPM_VERSION}" \
  && chromium --version \
  && node -e 'require("playwright-core").chromium' \
  && npm cache clean --force \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace
