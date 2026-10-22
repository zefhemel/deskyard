FROM sha256:0123456789abcdef
USER root

# mise tools, with their executables linked into /opt/deskyard/bin
RUN set -eux; export MISE_YES=1 MISE_CACHE_DIR=/tmp/mise-cache MISE_DATA_DIR=/opt/mise MISE_CONFIG_DIR=/tmp/mise-config; mise install 'node@24' 'npm:@openai/codex@latest' 'go@latest'; for spec in 'node@24' 'npm:@openai/codex@latest' 'go@latest'; do n=0; paths=$(mise bin-paths "$spec"); for d in $paths; do for f in "$d"/*; do if [ -f "$f" ] && [ -x "$f" ]; then ln -sf "$f" "/opt/deskyard/bin/$(basename "$f")"; n=$((n+1)); fi; done; done; if [ "$n" -eq 0 ]; then echo "mise tool $spec has no executables to link" >&2; exit 1; fi; done; rm -rf /tmp/mise-cache /tmp/mise-config

# Flatpak remotes
RUN set -eux; curl -fsSL -o '/etc/deskyard/flatpak-remotes/gnome-nightly.flatpakrepo' 'https://nightly.gnome.org/gnome-nightly.flatpakrepo'; /usr/bin/flatpak.real --system remote-add --if-not-exists 'gnome-nightly' '/etc/deskyard/flatpak-remotes/gnome-nightly.flatpakrepo'
RUN set -eux; printf '[Flatpak Repo]\nTitle=%s\nUrl=%s\nGPGKey=%s\n' 'myrepo' 'https://repo.example.com/repo' 'QUJDREVG' > '/etc/deskyard/flatpak-remotes/myrepo.flatpakrepo'; /usr/bin/flatpak.real --system remote-add --if-not-exists 'myrepo' '/etc/deskyard/flatpak-remotes/myrepo.flatpakrepo'

# Starting home for new desktops
COPY home-template/ /opt/deskyard/home-template/
RUN chown -R 1000:1000 /opt/deskyard/home-template

# Build commands (base64 keeps them out of the Dockerfile's own syntax)
RUN set -eux; printf %s 'YXB0LWdldCB1cGRhdGUKYXB0LWdldCBpbnN0YWxsIC15IHJpcGdyZXA=' | base64 -d > /tmp/deskyard-commands.sh; bash -eux /tmp/deskyard-commands.sh; rm -f /tmp/deskyard-commands.sh
