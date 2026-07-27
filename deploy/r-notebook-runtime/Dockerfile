FROM rocker/r-ver:4.5.1

ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        libcurl4-openssl-dev \
        libfontconfig1-dev \
        libfreetype6-dev \
        libfribidi-dev \
        libharfbuzz-dev \
        libjpeg-dev \
        libpng-dev \
        libssl-dev \
        libtiff5-dev \
        libxml2-dev \
        pandoc \
        python3 \
        python3-pip \
        python3-venv \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.lock /tmp/requirements.lock
RUN python3 -m venv /opt/jupyter \
    && /opt/jupyter/bin/pip install --no-cache-dir --upgrade pip==25.1.1 \
    && /opt/jupyter/bin/pip install --no-cache-dir -r /tmp/requirements.lock

COPY install-r-packages.R /tmp/install-r-packages.R
RUN Rscript /tmp/install-r-packages.R

RUN useradd --create-home --uid 1000 --shell /bin/bash tma \
    && mkdir -p /workspace /etc/jupyter \
    && chown -R tma:tma /workspace /home/tma

COPY jupyter_server_config.py /etc/jupyter/jupyter_server_config.py

ENV PATH="/opt/jupyter/bin:${PATH}" \
    JUPYTER_CONFIG_DIR=/etc/jupyter \
    RENV_PATHS_CACHE=/home/tma/.cache/R/renv

USER tma
WORKDIR /workspace
EXPOSE 8888

HEALTHCHECK --interval=15s --timeout=5s --retries=20 \
  CMD curl --fail --silent "http://127.0.0.1:8888${TMA_NOTEBOOK_BASE_URL:-/}api" >/dev/null || exit 1

CMD ["jupyter", "lab", "--config=/etc/jupyter/jupyter_server_config.py"]
