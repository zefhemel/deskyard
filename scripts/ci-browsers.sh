#!/bin/bash
# The test suites' browsers (Chromium, Firefox and WebKit) and the system
# packages they need, on a GitHub Actions Ubuntu runner (the Tests workflow).
#
# Playwright's own `install --with-deps` runs apt-get update and fetches
# every package from the runner's apt mirror, which has taken 35-50 minutes.
# Instead, the packages the runner image lacks are fetched once into
# $PLAYWRIGHT_DEBS, which the workflow caches by the image's version and
# Playwright's, and installed from there. Should installing them from the
# cache fail, apt-get installs them the slow way.
set -euo pipefail

debs=${PLAYWRIGHT_DEBS:?the folder for the packages}
browsers=(chromium firefox webkit)
cd "$(dirname "$0")/.."

# What's missing ("  name" lines; the dry run exits 1 when anything is).
missing() {
    (cd supervisor/tests && uv run playwright install-deps --dry-run "${browsers[@]}" || true) | sed -n 's/^  \([^ ]*\)$/\1/p'
}

mapfile -t need < <(missing)
if [ "${#need[@]}" -gt 0 ]; then
    echo "Missing system packages: ${need[*]}"
    if ! compgen -G "$debs/*.deb" >/dev/null; then
        echo "Fetching them (not cached yet)"
        mkdir -p "$debs/partial"
        sudo apt-get update
        sudo apt-get install -y --no-install-recommends --download-only -o Dir::Cache::archives="$debs" "${need[@]}"
        sudo chown -R "$(id -u):$(id -g)" "$debs"
        rm -rf "$debs/partial" "$debs/lock"
    fi
    if ! sudo dpkg -i "$debs"/*.deb; then
        echo "Installing from the cache failed; installing with apt-get"
        sudo apt-get update
        sudo apt-get install -y -f --no-install-recommends "${need[@]}"
    fi
    mapfile -t still < <(missing)
    if [ "${#still[@]}" -gt 0 ]; then
        echo "Still missing: ${still[*]}; installing with apt-get"
        sudo apt-get update
        sudo apt-get install -y -f --no-install-recommends "${still[@]}"
    fi
fi

for d in images/tests supervisor/tests; do
    (cd "$d" && uv run playwright install "${browsers[@]}")
done
