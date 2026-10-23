#!/bin/bash
# The test suites' browsers (Chromium, Firefox and WebKit) and the system
# packages they need, on a GitHub Actions Ubuntu runner (the Tests workflow).
#
# Playwright's own `install --with-deps` runs apt-get update and fetches
# every package from the runner's apt mirror, which has taken 35-50 minutes.
# Instead, the packages the runner image lacks are kept in $PLAYWRIGHT_DEBS,
# which the workflow caches (by Playwright's version and the runner image's,
# falling back to an older image's), and installed from there. Only what the
# cache doesn't have comes from the mirror, and goes into the cache.
set -euo pipefail

debs=${PLAYWRIGHT_DEBS:?the folder for the packages}
browsers=(chromium firefox webkit)
cd "$(dirname "$0")/.."
mkdir -p "$debs"

installed() { [ "$(dpkg-query -W -f='${Status}' "$1" 2>/dev/null)" = "install ok installed" ]; }

# What Playwright says is missing ("  name" lines; the dry run exits 1 then).
mapfile -t need < <(
    (cd supervisor/tests && uv run playwright install-deps --dry-run "${browsers[@]}" || true) |
        sed -n 's/^  \([^ ]*\)$/\1/p'
)
echo "Missing system packages: ${need[*]:-none}"

if [ "${#need[@]}" -gt 0 ]; then
    # From the cache: the packages not installed (never a downgrade of one
    # this image has newer).
    from_cache=()
    for f in "$debs"/*.deb; do
        [ -e "$f" ] || continue
        installed "$(dpkg-deb -f "$f" Package)" || from_cache+=("$f")
    done
    if [ "${#from_cache[@]}" -gt 0 ] && ! sudo dpkg -i "${from_cache[@]}"; then
        echo "Some packages from the cache didn't install; apt-get finishes"
    fi

    still=()
    for p in "${need[@]}"; do installed "$p" || still+=("$p"); done
    if [ "${#still[@]}" -gt 0 ] || [ -n "$(sudo dpkg --audit)" ]; then
        echo "From the mirror: ${still[*]:-(dependencies)}"
        sudo apt-get update
        mkdir -p "$debs/partial"
        sudo apt-get install -y -f --no-install-recommends -o Dir::Cache::archives="$debs" "${still[@]}"
        sudo rm -rf "$debs/partial" "$debs/lock"
    fi
    sudo chown -R "$(id -u):$(id -g)" "$debs"
fi

for d in images/tests supervisor/tests; do
    (cd "$d" && uv run playwright install "${browsers[@]}")
done
