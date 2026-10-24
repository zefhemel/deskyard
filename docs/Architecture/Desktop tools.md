---
tags: component
status: built
partOf: "[[Architecture/Desktop]]"
uses:
  - "[[Architecture/Deskyard CLI]]"
  - "[[Architecture/Chromium]]"
  - "[[Architecture/Workspace folder]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
Tools available in the desktop: mise, uv and npm install into the home folder, and Flatpak apps install with `--user`. People use these tools through desktop terminals or the [[Architecture/Remote CLI]]. Optional agent integrations are supported by the shipped skills in `images/common/rootfs/opt/workspace/skills/`, which explain the desktop, shared browser and screen controls.

uv's default `python-preference = "only-managed"` comes from `images/common/rootfs/etc/uv/uv.toml`, installed as `/etc/uv/uv.toml` in every desktop variant. Normal `uv tool install` commands use a Python stored in `/workspace`, keeping tools independent of the image's Python on upgrade. User configuration, explicit command options and installers that disable uv config can override this default. It is not enforced through an inherited environment variable: the former `UV_PYTHON_PREFERENCE` setting conflicted with explicit `--managed-python` flags. `images/tests/test_persistence.py` covers installer flags, default tool interpreter ownership and tool survival across container recreation.

Hermes Agent can be installed separately into `/workspace` as the ordinary user. The [[superpowers/spikes/2026-10-24-hermes|Hermes Agent desktop trial]] verifies CLI/dashboard startup, restart persistence and discovery of these skills through Hermes' `skills.external_dirs` setting. The image does not bundle Hermes or configure its skills automatically; provider authentication and a real model conversation remain unverified. The trial preserves the installer workaround needed on older images with the `UV_PYTHON_PREFERENCE` environment variable.
