#!/bin/sh
set -eu

flatpak=/usr/bin/flatpak.real
# Fail before any downloads if this Flatpak lacks the inventory columns.
"$flatpak" list --system --columns=ref,active:full >/dev/null
"$flatpak" config --system --set languages en
"$flatpak" remote-add --system --if-not-exists flathub /etc/workspace/flathub.flatpakrepo
"$flatpak" install --system -y --noninteractive flathub org.libretro.RetroArch

# A plain Docker build cannot create the namespaces used by Flatpak triggers.
# Installation itself works; rebuild the exported icon/MIME caches directly.
for trigger in /usr/share/flatpak/triggers/*.trigger; do
    [ -x "$trigger" ] || continue
    "$trigger" /var/lib/flatpak
done
