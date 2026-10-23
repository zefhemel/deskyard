session_configure() {
    # Native schema/layout defaults leave saved user settings intact. Seed the
    # panel stylesheet as this user, preserving existing files and symlinks.
    local css="$HOME/.config/gtk-3.0/gtk.css"
    if [ ! -e "$css" ] && [ ! -L "$css" ]; then
        mkdir -p "$(dirname "$css")" && cp -n /etc/workspace/mate-panel.css "$css"
    fi
}
session_start() { mate-session; }
session_logout() { mate-session-save --logout; }
session_ready() {
    pgrep -u 1000 -x mate-panel >/dev/null && pgrep -u 1000 -x marco >/dev/null
}
