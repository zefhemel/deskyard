# User-installed tools live in /workspace and survive image upgrades.
# (Debian's /etc/profile resets PATH for login shells, so set it again.)
export PATH="/workspace/.local/bin:/workspace/.npm-global/bin:/workspace/.local/share/mise/shims:/opt/mise/shims:/opt/runtimes/node/bin:/opt/runtimes/python/bin:$PATH"
export XDG_DATA_DIRS="/workspace/.local/share/flatpak/exports/share:/var/lib/flatpak/exports/share:${XDG_DATA_DIRS:-/usr/local/share:/usr/share}"
