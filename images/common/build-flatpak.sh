#!/bin/sh
# Rebuild Debian's current security source, retaining its complete patch stack.
# The upstream retry is https://github.com/flatpak/flatpak/issues/6783.
set -eu
cd /src/source

git apply --check /tmp/flatpak-openat2.diff
cp /tmp/flatpak-openat2.diff debian/patches/deskyard-openat2-retry.patch
printf '\ndeskyard-openat2-retry.patch\n' >> debian/patches/series
version="$(dpkg-parsechangelog -S Version)+deskyard1"
{
    printf 'flatpak (%s) trixie; urgency=medium\n\n' "$version"
    printf '  * Backport the upstream libglnx openat2 EAGAIN retry.\n\n'
    printf ' -- Deskyard image build <nobody@invalid>  %s\n\n' "$(date -R)"
    cat debian/changelog
} > /tmp/flatpak-changelog
mv /tmp/flatpak-changelog debian/changelog
DEB_BUILD_OPTIONS=nocheck dpkg-buildpackage -us -uc -j2
# Package cleanup unapplies quilt patches; restore the matching test headers.
dpkg-source --before-build .

# Fault-inject into the exact libglnx objects linked into the CLI and library.
build="obj-$(dpkg-architecture -qDEB_HOST_GNU_TYPE)"
cc -Wall -Wextra -Werror -include "$build/config.h" \
    -I"$build" -I"$build/subprojects/libglnx" -Isubprojects/libglnx \
    $(pkg-config --cflags gio-unix-2.0) \
    /tmp/flatpak-openat2-test.c "$build/subprojects/libglnx/libglnx.a" \
    $(pkg-config --libs gio-unix-2.0) -Wl,--wrap=syscall -o /tmp/flatpak-openat2-test
timeout 10 /tmp/flatpak-openat2-test

mkdir -p /out/debs /out/source
cp ../flatpak_"$version"_*.deb ../libflatpak0_"$version"_*.deb \
    ../gir1.2-flatpak-1.0_"$version"_*.deb /out/debs/
# Ship corresponding source including Debian's patches and this backport.
cp ../flatpak_"$version".dsc ../flatpak_"$version".debian.tar.xz \
    ../flatpak_*.orig.tar.* /out/source/
