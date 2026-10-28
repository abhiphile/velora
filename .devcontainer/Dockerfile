FROM mcr.microsoft.com/devcontainers/go:1.25-bookworm

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        dnsutils \
        iproute2 \
        iptables \
        iputils-ping \
        kmod \
        procps \
        util-linux \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
