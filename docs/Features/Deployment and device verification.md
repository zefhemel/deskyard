---
displayName: "Deployment and device verification"
tags: "feature"
status: "discovery"
kind: "improvement"
availability: "unavailable"
summary: "Several local fixes and features have evidence, while native Linux, provider changes and physical-device coverage remain incomplete."
outcome: "Establish deployment and device confidence for the intended environment."
acceptance:
  - "Native Linux and the chosen host/engine reboot scenarios are verified for VPN desktops."
  - "Country/credential changes and provider limits are checked separately from successful OrbStack authentication."
  - "The selected browsers/devices and affected CI media, settings, first-frame and X-input regressions pass in their target environments."
next: "Check the current CI and deployment results, then run the still-missing environment-specific acceptance checks."
components:
  - "[[Architecture/VPN runtime]]"
  - "[[Architecture/Selkies]]"
  - "[[Architecture/Shared terminal]]"
  - "[[Architecture/Web UI]]"
---
Live NordVPN authentication and ordinary egress already passed on OrbStack; do not list them as absent. Country geolocation, changed credentials/country, provider limits, actual host/engine reboot and native Linux remain distinct checks. The uncommitted Selkies input recovery change has local real-X/MATE evidence; native amd64 Actions verification remains pending in the retained record. Original intermittent MATE stall cause remains unconfirmed. Public-domain automatic HTTPS was not verified in the retained initial supervisor evidence. The [[Testing/CI transport regressions 2026-10-24|CI transport record]], [[Testing/MATE stream startup 2026-10-24|Input recovery record]] and [[Testing/Shared terminal 2026-10-23|Terminal evidence]] define the exact scopes. Historical job-state/preflight/event-test findings were fixed in commit 02898bb; they are not current defects.

# Implementation boundary
- `supervisor/tests/VPN.md`
- `images/tests/test_selkies.py`
- `supervisor/tests/test_ui_audio.py`
