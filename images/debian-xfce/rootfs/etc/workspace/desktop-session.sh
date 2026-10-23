# Sourced by start-session and desktop-ready, as the desktop user.
session_configure() {
    xfconf-query -c xfce4-session -p /general/SaveOnExit >/dev/null 2>&1 \
        || xfconf-query -c xfce4-session -p /general/SaveOnExit -n -t bool -s true
    /usr/local/bin/workspace-migrate-panel
}
session_start() { startxfce4; }
session_logout() { xfce4-session-logout --logout; }
session_ready() {
    pgrep -u 1000 -x xfce4-panel >/dev/null \
        && xfconf-query -c xfce4-panel -p /panels/panel-2/position >/dev/null
}
