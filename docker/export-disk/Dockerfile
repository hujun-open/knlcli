FROM debian:stable-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends qemu-utils \
    && rm -rf /var/lib/apt/lists/*
CMD ["sleep", "infinity"]
