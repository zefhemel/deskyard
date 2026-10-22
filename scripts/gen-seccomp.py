#!/usr/bin/env python3
"""Generate supervisor/internal/engine/docker/flatpak-seccomp.json: Docker's default profile plus the syscalls
bubblewrap (Flatpak) needs to build its sandbox.

Docker's default only allows namespace and mount syscalls to containers that
have CAP_SYS_ADMIN. This profile allows them unconditionally, without granting
the capability.
"""

import json
import sys
import urllib.request
from pathlib import Path

PROFILES_TAG = "seccomp/v0.2.4"  # github.com/moby/profiles, the profile Docker ships
URL = f"https://raw.githubusercontent.com/moby/profiles/{PROFILES_TAG}/seccomp/default.json"
EXTRA = ["clone", "clone3", "unshare", "setns", "mount", "umount2", "pivot_root"]
OUT = Path(__file__).resolve().parent.parent / "supervisor" / "internal" / "engine" / "docker" / "flatpak-seccomp.json"


def main() -> None:
    profile = json.load(urllib.request.urlopen(URL))
    rules = []
    for rule in profile["syscalls"]:
        if rule.get("action") == "SCMP_ACT_ALLOW" and rule.get("args"):
            # Argument-filtered allow rules (e.g. clone without namespace flags)
            # are superseded by the unconditional rule below.
            names = [n for n in rule["names"] if n not in EXTRA]
            if not names:
                continue
            rule = {**rule, "names": names}
        elif rule.get("action") == "SCMP_ACT_ERRNO":
            names = [n for n in rule["names"] if n not in EXTRA]
            if not names:
                continue
            rule = {**rule, "names": names}
        rules.append(rule)
    rules.append({
        "names": EXTRA,
        "action": "SCMP_ACT_ALLOW",
        "comment": f"workspace sandbox: bubblewrap/Flatpak namespaces (generated from moby/profiles {PROFILES_TAG})",
    })
    profile["syscalls"] = rules
    OUT.parent.mkdir(exist_ok=True)
    OUT.write_text(json.dumps(profile, indent=2) + "\n")
    print(f"wrote {OUT}", file=sys.stderr)


if __name__ == "__main__":
    main()
