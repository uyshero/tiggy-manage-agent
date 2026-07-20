ARG ONLYBOXES_BASE_IMAGE=coolfan1024/onlyboxes-runtime:default
FROM ${ONLYBOXES_BASE_IMAGE}

ARG REQUESTS_VERSION=2.34.2
RUN python3 -m pip install --no-cache-dir "requests==${REQUESTS_VERSION}" \
    && python3 -c "import requests; assert requests.__version__ == '${REQUESTS_VERSION}'"

LABEL org.opencontainers.image.description="TMA Onlyboxes runtime with Python requests"
