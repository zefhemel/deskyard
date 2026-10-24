# Throwaway overlay; build from this folder. The normal desktop image is unchanged.
ARG BASE_IMAGE=deskyard-debian-xfce:test
FROM ${BASE_IMAGE}
USER root
ARG TARGETARCH
RUN case "$TARGETARCH" in \
      arm64) arch=aarch64; checksum=2a2c0621e6f3b11ecbb05d66939a48000a2508604620aa20727a5fd61c49f451 ;; \
      amd64) arch=x86_64; checksum=d006c521dcb475a6005d741e9dd7c5758e5a23b28dd60a5c10cebfa4876319dd ;; \
      *) exit 1 ;; \
    esac \
    && curl -fsSL "https://github.com/zellij-org/zellij/releases/download/v0.45.1/zellij-${arch}-unknown-linux-musl.tar.gz" -o /tmp/zellij.tar.gz \
    && tar -xzf /tmp/zellij.tar.gz -C /usr/local/bin zellij \
    && echo "$checksum  /usr/local/bin/zellij" | sha256sum -c - \
    && rm /tmp/zellij.tar.gz
COPY zellij.kdl /etc/deskyard/zellij-prototype.kdl
COPY shared-terminal /usr/local/bin/shared-terminal
COPY shared-terminal.desktop /usr/local/share/applications/shared-terminal.desktop
COPY terminal.conf /etc/supervisor/conf.d/terminal-prototype.conf
RUN chmod 755 /usr/local/bin/shared-terminal

# Presentation-only overrides, served on their own loopback origin.
COPY prepare_web.py /tmp/prepare-terminal-web.py
RUN python3 /tmp/prepare-terminal-web.py /opt/deskyard/terminal-web \
    && rm /tmp/prepare-terminal-web.py
COPY terminal.Caddyfile /etc/deskyard/terminal.Caddyfile
COPY terminal-layout.kdl /etc/deskyard/terminal-layout.kdl
COPY web-icons/ /opt/deskyard/terminal-web/assets/
COPY terminal_attach.py /usr/local/bin/terminal-attach
RUN chmod 755 /usr/local/bin/terminal-attach
COPY THIRD_PARTY_NOTICES.txt /usr/share/doc/deskyard-terminal/THIRD_PARTY_NOTICES.txt
