#!/bin/sh
# Installs the deskyard command from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/zefhemel/deskyard/main/install.sh | sh
#
# Environment:
#   DESKYARD_VERSION      release tag to install (default: edge, built from main)
#   DESKYARD_INSTALL_DIR  where to put deskyard (default: ~/.local/bin if
#                         writable, else /usr/local/bin with sudo)
#   DESKYARD_REPO         GitHub repository to download from (default:
#                         zefhemel/deskyard), for forks
#   DESKYARD_BASE_URL     download from here instead of the GitHub release
#                         (a folder with deskyard-<os>-<arch> and SHA256SUMS)
set -eu

repo=${DESKYARD_REPO:-zefhemel/deskyard}
version=${DESKYARD_VERSION:-edge}
base_url=${DESKYARD_BASE_URL:-https://github.com/$repo/releases/download/$version}

say() { printf '%s\n' "$*"; }
fail() { printf 'deskyard install: %s\n' "$*" >&2; exit 1; }

case $(uname -s) in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "unsupported system $(uname -s): Linux and macOS only (Windows: download deskyard-windows-amd64.exe from https://github.com/$repo/releases)" ;;
esac
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail "unsupported architecture $(uname -m): amd64 and arm64 only" ;;
esac
file=deskyard-$os-$arch

if command -v curl > /dev/null 2>&1; then
  download() { curl -fsSL -o "$2" "$1"; }
elif command -v wget > /dev/null 2>&1; then
  download() { wget -q -O "$2" "$1"; }
else
  fail "needs curl or wget"
fi

if command -v sha256sum > /dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum > /dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
  fail "needs sha256sum or shasum to check the download"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading $file ($version) from $base_url"
download "$base_url/$file" "$tmp/deskyard" || fail "could not download $base_url/$file"
download "$base_url/SHA256SUMS" "$tmp/SHA256SUMS" || fail "could not download $base_url/SHA256SUMS"

want=$(awk -v f="$file" '$2 == f || $2 == "*" f { print $1 }' "$tmp/SHA256SUMS")
[ -n "$want" ] || fail "$file is not listed in SHA256SUMS"
got=$(sha256 "$tmp/deskyard")
[ "$got" = "$want" ] || fail "checksum mismatch for $file: expected $want, got $got"
chmod 755 "$tmp/deskyard"

sudo=
if [ -n "${DESKYARD_INSTALL_DIR:-}" ]; then
  dir=$DESKYARD_INSTALL_DIR
  mkdir -p "$dir" 2> /dev/null || true
  [ -w "$dir" ] || fail "DESKYARD_INSTALL_DIR $dir is not writable"
else
  dir=$HOME/.local/bin
  mkdir -p "$dir" 2> /dev/null || true
  if [ ! -w "$dir" ]; then
    dir=/usr/local/bin
    if [ ! -w "$dir" ]; then
      command -v sudo > /dev/null 2>&1 || fail "$HOME/.local/bin and $dir are not writable; set DESKYARD_INSTALL_DIR"
      say "$HOME/.local/bin is not writable; installing to $dir with sudo"
      sudo=sudo
    fi
  fi
fi

# Copy next to it, then rename: replaces a running deskyard safely, and
# with sudo the file belongs to root.
$sudo cp "$tmp/deskyard" "$dir/.deskyard.$$"
$sudo mv -f "$dir/.deskyard.$$" "$dir/deskyard"
say "Installed $dir/deskyard"

case ":$PATH:" in
  *":$dir:"*) ;;
  *) say "Note: $dir is not on your PATH. Add it, for example:"
     say "  export PATH=\"$dir:\$PATH\"" ;;
esac

"$dir/deskyard" --version
