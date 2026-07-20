FROM mcr.microsoft.com/playwright:v1.54.1-noble
WORKDIR /opt/tma-browser-gateway
ENV NODE_ENV=production \
    PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 \
    TMA_BROWSER_GATEWAY_HOST=0.0.0.0 \
    TMA_BROWSER_GATEWAY_PORT=8090 \
    TMA_BROWSER_PROFILE_ROOT=/var/lib/tma-browser/profiles
COPY package.json package-lock.json ./
RUN npm ci --omit=dev
COPY src ./src
RUN mkdir -p /var/lib/tma-browser/profiles && chown -R pwuser:pwuser /var/lib/tma-browser
USER pwuser
EXPOSE 8090
ENTRYPOINT ["node", "src/server.mjs"]
