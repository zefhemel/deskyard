#!/bin/sh
set -eu

mkdir -p /out/cores /out/sources
cd /build
while read -r name repository revision sha256 directory makefile; do
    case "$name" in ''|'#'*) continue ;; esac
    archive="/out/sources/$name-$revision.tar.gz"
    curl --fail --location --retry 3 --output "$archive" \
        "https://codeload.github.com/libretro/$repository/tar.gz/$revision"
    printf '%s  %s\n' "$sha256" "$archive" | sha256sum --check --strict
    mkdir "$name"
    tar -xzf "$archive" --strip-components=1 -C "$name"
    # Two compiler processes bound memory use on small Docker hosts. Build
    # natively for the target architecture; no host-specific CPU tuning.
    make -C "$name/$directory" -f "$makefile" -j2 platform=unix
    install -m 644 "$name/$directory/$name.so" "/out/cores/$name.so"
    rm -rf "$name"
done < cores.tsv
