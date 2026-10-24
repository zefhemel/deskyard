#!/bin/bash
# Build-time restrictions after all desktop/store packages are installed.
set -euo pipefail
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# libkf6su6 hard-depends on sudo. Retain valid package dependencies, but no
# callable sudo or privileged executable reachable by the desktop user.
if [ -e /usr/bin/sudo ]; then
    install -d -m 700 /usr/lib/deskyard-disabled
    dpkg-divert --local --rename --divert /usr/lib/deskyard-disabled/sudo --add /usr/bin/sudo
    chmod 000 /usr/lib/deskyard-disabled/sudo
fi
! command -v sudo

# Hardware/accounts/time management belongs to the host, not a desktop.
# Don't let desktop queries activate newly installed root services on D-Bus.
for service in org.freedesktop.Accounts org.freedesktop.UDisks2 org.freedesktop.UPower \
    org.freedesktop.PackageKit org.mate.SettingsDaemon.DateTimeMechanism; do
    rm -f "/usr/share/dbus-1/system-services/$service.service"
done

# Discover hard-depends on PackageKit in Debian. Keep package metadata valid,
# but disable both its backend plugin and daemon; only Flatpak is offered.
for daemon in /usr/libexec/packagekitd /usr/lib/packagekit/packagekitd; do
    if [ -e "$daemon" ]; then
        install -d -m 700 /usr/lib/deskyard-disabled
        dpkg-divert --local --rename --divert /usr/lib/deskyard-disabled/packagekitd --add "$daemon"
        chmod 000 /usr/lib/deskyard-disabled/packagekitd
    fi
done
find /usr/lib -path '*/discover/*' -name '*packagekit*' -delete
! command -v sudo
test ! -e /usr/libexec/packagekitd
test ! -e /usr/lib/packagekit/packagekitd

# Generate one identity per container on first workspace-init, visible to
# both D-Bus and graphical software that reads the standard /etc path.
rm -f /etc/machine-id /var/lib/dbus/machine-id
ln -s /var/lib/dbus/machine-id /etc/machine-id
