#!/bin/bash
# Link the host-side skills (skills/host/*) into coding agents' user skill dirs.
# usage: install-host-skills.sh [--claude] [--codex] [--agents] [--gemini]
# Without flags, installs for every agent whose config dir exists in $HOME.
set -euo pipefail

src="$(cd "$(dirname "$0")/.." && pwd)/skills/host"
all="claude codex agents gemini"

selected=""
for arg in "$@"; do
    case $arg in
        --claude|--codex|--agents|--gemini) selected="$selected ${arg#--}" ;;
        *) echo "unknown option: $arg" >&2; exit 2 ;;
    esac
done
if [ -z "$selected" ]; then
    for name in $all; do
        [ -d "$HOME/.$name" ] && selected="$selected $name"
    done
fi

for name in $selected; do
    dest="$HOME/.$name/skills"
    mkdir -p "$dest"
    for skill in "$src"/*/; do
        skill=${skill%/}
        ln -sfn "$skill" "$dest/$(basename "$skill")"
        echo "linked $(basename "$skill") -> $dest"
    done
done
