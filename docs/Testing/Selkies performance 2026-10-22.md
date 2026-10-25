---
displayName: "Selkies performance observations — 22 October 2026"
tags: "verification"
result: "partial"
scope: "Historical ARM64 MacBook Air whole-container CPU and A/V measurements with the three experimental idle-CPU patches, including an audio gate disabled by default today."
date: "2026-10-22"
---
Measurements retained from the completed Selkies implementation plan. These are historical experiments on OrbStack and Playwright Chromium with fake audio output, not a current fleet footprint guarantee. Whole-container cgroup CPU was measured over interleaved 60-second runs on an ARM64 MacBook Air at host load averages 3.3–5.1. Each value is percent of one CPU core.

| Case | Stock observations | Three patches enabled |
| --- | --- | --- |
| No viewer, fresh | 1.12, 1.08, 1.09, 1.07 | 0.47, 0.44, 0.48, 0.40 |
| One idle viewer at 1280×800 after 20 seconds | 7.7, 8.4, 7.5, 7.4 | 1.16, 1.11, 1.11, 1.09 |
| No viewer, 20 seconds after departure | 1.13, 1.10, 1.12, 1.07 | 0.58, 0.44, 0.44, 0.41 |

The patches covered sleeping key timers, idle screen-grab suppression and speaker capture gating. The last gate dropped about the first 90 ms of sound after three seconds of silence, which continuous-beep sync tests did not reveal. It is disabled by default in the current runtime; do not present the all-three-patches CPU column as today's default-image measurement.

With the patches, clean-link median A/V offset was +18 ms (individual beeps −21 to +57 ms, sound lag 64 ms). With 60±20 ms delay in each direction, median was +26 ms (individual beeps +6 to +67 ms, sound lag 129 ms). The declared median target was no more than 45 ms audio lead or 125 ms audio lag, using 30 beeps after five warm-up beeps. These results retain their tested setup and do not establish all sound-start or device behavior.

Later bounded first-picture recovery was verified locally at 0.01 seconds for normal joining and 0.16 seconds with deterministic withheld initial video. Current first-frame, display readiness and input recovery are described in [[Architecture/Selkies]], [[Testing/Desktop startup readiness 2026-10-24]] and [[Testing/MATE stream startup 2026-10-24]]. Native amd64 Actions coverage of the latest input recovery remains a release check in [[Features/Deployment and device verification]].
