---
displayName: Idle desktop memory reclamation spike
date: "2026-10-23"
---
**An XFCE desktop with sandboxed Chromium can release memory on the tested macOS/OrbStack setup without stopping its applications.** A disposable host helper successfully requested cgroup-v2 `memory.reclaim`; unsaved browser text and a JavaScript-only nonce survived. This is a feasibility experiment, not an implemented supervisor feature or an automatic idle policy.

**Follow-up, 2026-10-23:** the manual **Reclaim memory** button is now implemented, as described in [[Architecture/Engine adapter]] and [[Architecture/Web UI]]. It uses two capability-free helpers with narrower mounts instead of this probe's privileged helper. The measurements and limitations below record the earlier experiment; the subsequent one-shot idle policy is also implemented in [[Architecture/Lifecycle]].

# Environment and method

- macOS ARM64 with the `orbstack` Docker context, Docker Engine 29.8.2, Linux `7.2.9-orbstack-00391-g2b298d05fbd4`, cgroup v2, memory and swap limit support enabled.
- Local `deskyard-debian-xfce:test`, image `sha256:4fe98951594089ef71fab04e348d77b225a3a92fa37ab7051cd752a50a91456b`.
- Linux swap was already configured: approximately 15.7 GiB logical zram capacity and 1 GiB disk swap. Zram capacity is a limit on uncompressed data, not a preallocated RAM reservation. No host swap configuration was changed.
- Each desktop used a fresh named workspace volume, the ordinary desktop test security settings, 1 GiB shared memory and only port 8080 published on random macOS localhost. The existing `deskyard-obs` desktop was not targeted or modified.
- The disposable `python:3.13-alpine` helper had no network, but was privileged with host PID/cgroup namespaces and the Linux host's cgroup filesystem mounted writable. It resolved the test desktop's cgroup from its host PID, checked the full container ID in that path, and wrote only that cgroup's `memory.reclaim`. This broad access is a diagnostic technique, not a proposed production security boundary.
- Chromium was started through the desktop's Caddy/CDP route with its normal sandbox. A page held unsaved textarea text, a random JavaScript-only nonce and a one-second timer. CDP disconnected between checks; Chromium continued running. No Selkies viewer was connected.
- After a 20-second settling period and a baseline interaction check, the probe waited 15 seconds, requested `128M swappiness=100`, measured immediately and after five seconds, then reconnected and checked browser state, editing and PNG screenshot capture. It repeated with `256M swappiness=100` after another 15 seconds. These are requests to reclaim additional bytes, not target footprints or RAM limits. The second pass follows the first on the same desktop.
- Measurements include `memory.current`, `memory.swap.current`, `memory.stat`, `memory.events` and the shared zram device's `mm_stat`. The host was not otherwise isolated from other work, so shared memory counters and timings can include concurrent workload effects. Temporary containers and workspace volumes were removed after each experiment.

# Results

First run; memory in MiB, rounded:

| Requested reclaim | Resident before | Resident after 5 s | Resident after interaction | Logical swap after 5 s | Reclaim duration |
| --- | ---: | ---: | ---: | ---: | ---: |
| 128 MiB | 472 | 350 | 363 | 90 | 470 ms |
| 256 MiB | 456 | 292 | 333 | 227 | 890 ms |

Both writes completed without errors. Browser nonce and unsaved text matched after both passes, editing and screenshot capture passed, the page timer advanced from 21 to 44 to 68, and `memory.events` recorded no OOM or OOM kills. This tests application continuity, not restoration after termination.

Reconnection plus reading page state took 357 and 397 ms after reclamation; editing and restoring the textarea took 272 and 486 ms, and screenshots took 288 and 238 ms. The unreclaimed baseline was slower (1,241 ms to reconnect/read, 908 ms to edit/restore, 347 ms for a screenshot), demonstrating substantial timing noise; these samples do not establish a latency improvement or bound the added wake delay.

`memory.current` is resident cgroup accounting including file cache, not the whole Mac's physical footprint. The shared zram allocation grew by about 55 MiB from the first pre-reclaim snapshot to the final five-second snapshot. Subtracting this from the approximately 180 MiB resident reduction gives an **estimated net Linux-memory saving of 125 MiB** across both passes. This uses a shared device counter that can include unrelated workloads; it is not a measurement of memory returned to macOS. Per-pass estimated savings were 97 and 134 MiB and must not be added: background allocations and file cache changed between passes. The resident increase from 363 to 456 MiB between passes illustrates that an idle application can allocate or refill caches again.

Full first-run counters and interaction timings are saved in [results.json](2026-10-23-idle-memory/results.json).

A second fresh desktop passed the same checks with the finalized diagnostic, including the cleanup assertions. Its starting footprint was larger, so these are repeatability observations rather than directly comparable workload benchmarks:

| Requested reclaim | Resident before | Resident after 5 s | Resident after interaction | Logical swap after 5 s | Reclaim duration |
| --- | ---: | ---: | ---: | ---: | ---: |
| 128 MiB | 676 | 549 | 596 | 92 | 354 ms |
| 256 MiB | 595 | 360 | 373 | 323 | 932 ms |

The shared zram allocation grew by approximately 85 MiB across both passes, giving an estimated net Linux-memory saving of **231 MiB** from the first pre-reclaim snapshot to the final five-second snapshot. Reconnect/read took 466 and 458 ms versus a 359 ms baseline; edit/restore took 360 and 386 ms versus 206 ms, and screenshots took 191 and 183 ms versus 108 ms. The state nonce and textarea survived, the timer advanced from 20 to 42 to 65, and there were no OOM kills. The second run illustrates a measurable interaction cost as pages are accessed again; these few samples do not predict real-user latency. See [repeat-results.json](2026-10-23-idle-memory/repeat-results.json) for the counters.

# Reproducing

From the repository root, outside the sandbox, with the two images already available locally:

```sh
uv run --project images/tests python docs/superpowers/spikes/2026-10-23-idle-memory/reclaim-probe.py
```

The [diagnostic](2026-10-23-idle-memory/reclaim-probe.py) writes `/tmp/deskyard-reclaim-result.json`; an optional first argument selects another result path. It checks browser state continuity, a running timer, successful reclaim writes, lower resident usage, increased logical swap, no OOM kills and removal of its containers and volume. This is an environment-specific diagnostic, not a general CI test or a supervisor implementation.

# Implications and limits

The supervisor can potentially operate this through its existing Docker connection, using a helper inside OrbStack's Linux VM; native Linux cgroup access from the macOS supervisor process is unnecessary. A production implementation still needs a constrained helper design, compatibility checks, bounded asynchronous reclaim operations, failure handling, and idle detection that accounts for agents and background work. A writable `memory.reclaim` interface and available swap should be detected, not assumed for every Docker runtime. The experiment's helper command was synchronous and the kernel can reclaim more or less than requested.

No container freezing, application shutdown, hard-memory-limit reduction, global cache dropping, host reconfiguration or CRIU checkpointing was used. Unlike the separate desktop suspend/checkpoint experiment, this approach leaves processes and their namespaces running and pages out eligible memory.

Two reclaim sizes on each of two fresh desktops with a small synthetic browser page do not establish behavior for real browser workloads, many desktops, Flatpak apps, prolonged idle periods, memory pressure, sustained memory savings or active agents. Human-perceived Selkies reconnection/input latency and actual macOS memory return were not measured. At the time of this probe no supervisor feature, API or UI was added; the subsequent manual implementation is identified above. The later automatic policy and its activity guards are described above.

# Live Obsidian follow-up, 2026-10-23

Tested the upgraded persistent `obs` desktop on macOS/OrbStack with Obsidian 1.14.5 (ARM64 Flatpak), its normal sandbox and the production supervisor's **Reclaim memory** button. Added an isolated folder with 201 synthetic notes, edited the test note through the live Selkies viewer, exercised graph view and searched the 200 linked notes (8,400 matches). Existing notes were not edited. The desktop viewer stayed connected; this tests the manual action, not the automatic timer. See [recorded counters and timings](2026-10-23-idle-memory/obsidian-results.json), [before](2026-10-23-idle-memory/obsidian-before-reclaim.jpg) and [after](2026-10-23-idle-memory/obsidian-after-reclaim.jpg).

The production helper requested 128 MiB and completed without partial success. Resident cgroup accounting was 950.01 MiB immediately before its request and 865.04 MiB afterwards: an observed 84.97 MiB decrease. Samples five seconds after completion showed 866.74 MiB resident and 82.46 MiB logical swap; thirty seconds after completion, 908.69 MiB resident and 78.23 MiB swap. After three further GUI edits, median resident use was 890.41 MiB and swap 75.15 MiB (98 half-second samples): about 59.60 MiB less resident memory than the helper's pre-request reading. Memory changes during ordinary activity, so these are observations rather than a sustained saving guarantee.

All Obsidian processes present at the reclaim boundary survived with the same PIDs/start times; OOM-kill counters did not change. The pre-reclaim text, open note and search state survived, and three subsequent GUI edits were verified on disk. Obsidian incurred 260 major and 39,518 minor faults across the resume/edit interval, consistent with paging or remapping memory back in. The three baseline input-to-file timings were 2.006, 2.008 and 2.005 seconds; the first post-reclaim edit took 2.270 seconds, then 2.004 and 2.005 seconds. These timings include remote input and Obsidian's approximately two-second autosave delay; the first edit's roughly 264 ms increase is a single observation, not an isolated app-render or wake-latency benchmark.

The shared zram device's physical allocation grew by approximately 18.66 MiB during the idle post-reclaim period and remained about 18.56 MiB above the earlier baseline after editing. This is shared compressed RAM rather than per-desktop usage and does not measure RAM returned to macOS. Resident memory also fell about 168 MiB when switching to the control tab, before the reclaim helper sampled it; that change is excluded from the 84.97 MiB reclamation figure. Other host workloads were not isolated.

The Flatpak's folder chooser failed because its document portal could not mount `/run/workspace/doc`. The app already had home-folder access, so no sandbox permission was changed. The synthetic folder was placed under the registered vault and opened through the supported [Obsidian URI interface](https://obsidian.md/help/uri); this setup issue is separate from memory reclamation.
