#!/bin/sh
# Rebuild Debian's libmate-desktop source, retaining its complete patch stack,
# so its root-background calls send their server ungrab before returning
# (patches/README.md, "MATE root-background ungrab").
set -eu
cd /src/source

git apply --check /tmp/mate-desktop-ungrab-flush.diff
cp /tmp/mate-desktop-ungrab-flush.diff debian/patches/deskyard-ungrab-flush.patch
printf '\ndeskyard-ungrab-flush.patch\n' >> debian/patches/series
version="$(dpkg-parsechangelog -S Version)+deskyard1"
{
    printf 'mate-desktop (%s) trixie; urgency=medium\n\n' "$version"
    printf '  * Flush the server ungrab after drawing the root background.\n\n'
    printf ' -- Deskyard image build <nobody@invalid>  %s\n\n' "$(date -R)"
    cat debian/changelog
} > /tmp/mate-desktop-changelog
mv /tmp/mate-desktop-changelog debian/changelog
DEB_BUILD_OPTIONS=nocheck dpkg-buildpackage -us -uc -j2

mkdir -p /out/debs /out/source
# The packages of this source that the MATE desktop installs.
cp ../libmate-desktop-2-17t64_"$version"_*.deb ../mate-desktop_"$version"_*.deb \
    ../mate-desktop-common_"$version"_all.deb /out/debs/
# Ship corresponding source including Debian's patches and this fix.
cp ../mate-desktop_"$version".dsc ../mate-desktop_"$version".debian.tar.xz \
    ../mate-desktop_*.orig.tar.* /out/source/
