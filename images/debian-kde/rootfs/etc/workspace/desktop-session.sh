# Plasma's X11 session can run without systemd as the user service manager.
session_configure() {
    local config="$HOME/.config/ksmserverrc"
    if ! kreadconfig6 --file "$config" --group General --key loginMode | grep -q .; then
        kwriteconfig6 --file "$config" --group General --key loginMode restorePreviousLogout
    fi
}
session_start() { startplasma-x11; }
session_logout() { qdbus6 org.kde.Shutdown /Shutdown logout; }
session_ready() {
    pgrep -u 1000 -x plasmashell >/dev/null && pgrep -u 1000 -x kwin_x11 >/dev/null
}
