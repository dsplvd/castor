# Trixie's ffmpeg encodes on VA-API (pass --device /dev/dri); TARGETARCH comes from buildx.
FROM debian:trixie-slim
ARG TARGETARCH

RUN set -eux; \
    if [ "$TARGETARCH" = "amd64" ]; then \
      # i965-va-driver-shaders is non-free: Debian's free i965 build lacks the media kernels its encoder needs.
      sed -i 's/^Components: main$/Components: main non-free/' /etc/apt/sources.list.d/debian.sources; \
      grep -q 'non-free' /etc/apt/sources.list.d/debian.sources; \
    fi; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      ca-certificates chromium ffmpeg fonts-liberation libgomp1 libstdc++6 libva2; \
    if [ "$TARGETARCH" = "amd64" ]; then \
      # iHD serves recent Intel iGPUs; i965 (with its shaders) serves older ones such as Atom Cherry Trail and Bay Trail.
      apt-get install -y --no-install-recommends intel-media-va-driver i965-va-driver-shaders vainfo; \
    fi; \
    apt-get clean; \
    rm -rf /var/lib/apt/lists/*

ENV CASTOR_SERVERS__SCRAPING__BROWSER__CHROME_PATH=/usr/bin/chromium \
    CASTOR_SERVERS__SCRAPING__BROWSER__NO_SANDBOX=true

COPY --chmod=0755 docker/${TARGETARCH}/castor /usr/local/bin/castor
ENTRYPOINT ["castor"]
