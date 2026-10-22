---
tags: component
status: built
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
One Go binary, run directly or in a small Alpine container, that creates, runs and shares many desktops. People sign in with their own account; admins decide which accounts may use which desktop. Agents reach a desktop through the [[Architecture/Agent API]], including [[Architecture/Port forwarding]]. See the Supervisor design for the full picture.
