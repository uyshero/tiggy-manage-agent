ARG ONLYBOXES_IMAGE=coolfan1024/onlyboxes-runtime@sha256:ee441e91ca4e58b0a933055122eb5995b5bd20abfa3d440ad0baff0de44d03c1
FROM ${ONLYBOXES_IMAGE}

ARG EDITPPT_VERSION=v0.3.2
ARG EDITPPT_COMMIT=4d5d935bf6d81929e28d316a3b90feadd5da527a

LABEL org.opencontainers.image.title="TMA image-to-editable-ppt sandbox"
LABEL org.opencontainers.image.description="CPU-only TMA cloud sandbox for editable PPT reconstruction"
LABEL org.opencontainers.image.source="https://github.com/ningzimu/image-to-editable-ppt-skill"
LABEL org.opencontainers.image.version="${EDITPPT_VERSION}"
LABEL tma.sandbox.capability="image-to-editable-ppt"

ENV DEBIAN_FRONTEND=noninteractive \
    EDITPPT_CONFIG_HOME=/mnt/data/.editppt \
    EDITPPT_UPSTREAM_ROOT=/opt/image-to-editable-ppt \
    LANG=C.UTF-8 \
    LC_ALL=C.UTF-8 \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    SAL_USE_VCLPLUGIN=svp \
    TMA_PPT_SANDBOX=1

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        fontconfig \
        fonts-dejavu-core \
        fonts-liberation2 \
        fonts-noto-cjk \
        fonts-noto-core \
        git \
        libreoffice-core \
        libreoffice-impress \
        poppler-utils \
        python3-venv \
    && rm -rf /var/lib/apt/lists/*

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        python3-numpy \
        python3-requests \
        python3-yaml \
    && rm -rf /var/lib/apt/lists/*

ARG PYPI_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple
ARG EDITPPT_ARCHIVE_SHA256=c04a03298a4d34041f85c40389aca32b6208efb62b9cba3c7daace65b9a2c4e9

COPY deploy/image-to-editable-ppt-sandbox/requirements.lock /tmp/requirements.lock

RUN export PIP_INDEX_URL="${PYPI_INDEX_URL}" \
    && python3 -m pip install --no-cache-dir -r /tmp/requirements.lock \
    && rm -f /tmp/requirements.lock

RUN mkdir -p "${EDITPPT_UPSTREAM_ROOT}" \
    && curl -fsSL --retry 5 --retry-all-errors \
        "https://codeload.github.com/ningzimu/image-to-editable-ppt-skill/tar.gz/${EDITPPT_COMMIT}" \
        -o /tmp/image-to-editable-ppt.tar.gz \
    && echo "${EDITPPT_ARCHIVE_SHA256}  /tmp/image-to-editable-ppt.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/image-to-editable-ppt.tar.gz --strip-components=1 -C "${EDITPPT_UPSTREAM_ROOT}" \
    && export PIP_INDEX_URL="${PYPI_INDEX_URL}" \
    && python3 -m pip install --no-cache-dir --no-deps \
        "${EDITPPT_UPSTREAM_ROOT}/skills/image-to-editable-ppt/cli" \
    && rm -f /tmp/image-to-editable-ppt.tar.gz \
    && fc-cache -f

COPY deploy/image-to-editable-ppt-sandbox/tma-ppt-sandbox-doctor /usr/local/bin/tma-ppt-sandbox-doctor

RUN chmod 0755 /usr/local/bin/tma-ppt-sandbox-doctor \
    && tma-ppt-sandbox-doctor

WORKDIR /workspace

CMD ["/bin/bash"]
