---
tags: component
status: built
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
One Go binary, run directly or in a small Alpine container, that creates, runs and shares many desktops. People sign in with their own account; admins decide which accounts may use which desktop. Remote CLI clients reach a desktop through the [[Architecture/Remote access API]], including [[Architecture/Port forwarding]]. It shows who is acting on each desktop, browser viewers and CLI clients, with [[Architecture/CLI activity]]. See the Supervisor design for the full picture.

Before listening, it probes the [[Architecture/Engine adapter]]'s optional memory-reclamation capability on a disposable target, logs support or a precise failure reason, and makes that cached decision available through the [[Architecture/API]]. A failed probe disables reclamation without failing startup. Restarting the supervisor repeats the probe; no existing desktop is reclaimed during it.

After startup, supported hosts run the one-shot idle memory policy in [[Architecture/Lifecycle]]. It monitors eligible desktops' inside activity even with the UI closed, reserves desktop operations before reclaiming, and persists opt-outs by stable desktop ID. Timer/attempt state stays in memory and starts fresh on supervisor restart.
