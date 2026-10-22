# Gamepads for the desktop's apps: Selkies' Input Interposer serves
# /dev/input/js0-3 and event1000-1003 from the sockets Selkies binds in
# $XDG_RUNTIME_DIR, and fake-udev lists those pads to libudev users (SDL).
# Sourced by start-session (XFCE and everything started from it) and
# start-chromium; never by Selkies, which is the other end of the sockets.
# ($LIB is expanded by the dynamic loader, to the multiarch directory.)
export SELKIES_INTERPOSER='/usr/$LIB/selkies_input_interposer.so'
export SELKIES_JS_SOCKET_PATH="$XDG_RUNTIME_DIR"
case ":${LD_PRELOAD:-}:" in
    *:"$SELKIES_INTERPOSER":*) ;;  # inherited already (Chromium opened from the session)
    *) export LD_PRELOAD="$SELKIES_INTERPOSER:/opt/selkies/lib/fake-udev/libudev.so.1${LD_PRELOAD:+:$LD_PRELOAD}" ;;
esac
