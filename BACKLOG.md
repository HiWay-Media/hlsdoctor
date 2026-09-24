# Backlog — hlsdoctor

Single source of truth for what is planned. Items keep a stable `HLD-n` id so commits,
the CHANGELOG, the `thoughts/` artifacts and the issues can reference them.

[ROADMAP.md](ROADMAP.md) is a **generated** view of this file — run
`node scripts/backlog.mjs roadmap` after touching it, or CI fails. The GitHub issues are
synced from it one way on every push to `main` that changes this file.

## How to write an item

```
## v0.2.0 — Title of the milestone <!-- ms: phase=next -->

- [ ] **HLD-99 — Short name**: what it is, why it earns its place, what it needs to
  touch. <!-- hld: prio=high size=M labels=probe -->
```

- The **id never changes**; a new item takes the next free number.
- `- [ ]` open, `- [x]` shipped with `ver=x.y.z` (or `ver=main` when merged, unreleased);
  decided against → ticked with `ver=dropped` and the reason in the body.
- Labels: `probe`, `verdict`, `benchmark`, `release`, `docs`, `project`, `tests`, `enhancement`.

## v0.1.0 — A week on the farm's channels <!-- ms: phase=now -->

The first release: `hlsdoctor check` run on a schedule against the real channels for a
week, every finding classified as true or noise, every outage checked for a finding,
the tally written in the README with a date. Nothing about the thresholds is trusted
until that tally exists.

**The tally is the gate on this milestone.** No `v0.1.0` before HLD-9 is in the README.

- [ ] **HLD-1 — Run QRSPI on the brief: Questions → Research → Spec → Plan**: input
  `thoughts/HLD-1-stream-doctor/00-brief.md`, one fresh session per phase. The design
  questions it must settle: the default thresholds (how far behind live is a finding
  for a 6 s target versus a 2 s low-latency one); whether an audio-only rung, a
  subtitles rendition and an I-frame playlist are probed or only counted; what
  `--each-ip` should do when the CDN answers from anycast; and whether the farm wants
  the probe as a Nomad job, a TeamCity step or both. <!-- hld: prio=high size=L labels=verdict -->
- [x] **HLD-2 — The M3U8 parser**: master and media playlists, RFC 8216 tags and the
  low-latency extensions, quoted attribute lists, tolerant of unknown tags, one fatal
  error (`#EXTM3U` missing). <!-- hld: prio=high size=M labels=probe ver=main -->
- [x] **HLD-3 — The HLS probe**: master → variants in parallel, a second fetch after
  one target duration, the newest segment downloaded and its container recognised,
  latency from EXT-X-PROGRAM-DATE-TIME, every URL redacted.
  <!-- hld: prio=high size=M labels=probe ver=main -->
- [x] **HLD-4 — The RTMP handshake probe**: rtmp and rtmps, C0/C1 → S0/S1/S2 → C2,
  version and echo, stage and timings; nothing sent after the handshake.
  <!-- hld: prio=high size=S labels=probe ver=main -->
- [x] **HLD-5 — Findings**: twenty-three codes, worst first, `--exit-on`, every threshold
  in `Policy` with a flag, `--no-ok`. <!-- hld: prio=high size=M labels=verdict ver=main -->
- [x] **HLD-6 — The CLI**: `check`, `ls`, `version`; interleaved flags and targets;
  `--from FILE`; `--each-ip` with per-node findings; `--header` sent and never printed;
  JSON output. <!-- hld: prio=med size=M labels=enhancement ver=main -->
- [x] **HLD-7 — Tests without a stream**: fixtures, an httptest origin with a healthy,
  a stale and a missing variant, a fake RTMP server, unit and end-to-end tests.
  <!-- hld: prio=high size=M labels=tests ver=main -->
- [x] **HLD-8 — Repo operating model**: CI (gofmt, vet, tests, static builds, check-repo),
  release by tag with checksums, drift check, Pages from README, backlog sync, the
  periodic Nomad job spec. <!-- hld: prio=med size=M labels=project,release ver=main -->
- [ ] **HLD-9 — A week on the channels**: `hlsdoctor check --from streams.txt` every
  five minutes against the farm's real HLS channels and RTMP ingests, findings kept;
  each classified true or noise, each known outage checked for a finding; the tally,
  dated, in the README. Thresholds adjusted from it. **Gates the release.**
  <!-- hld: prio=high size=M labels=benchmark -->
- [ ] **HLD-10 — Renditions and I-frame playlists**: probe EXT-X-MEDIA renditions that
  carry a URI (alternate audio, subtitles) the way variants are probed, so a dead
  audio track is a finding; count I-frame playlists. <!-- hld: prio=med size=S labels=probe -->
- [ ] **HLD-11 — Release 0.1.0**: VERSION, CHANGELOG, tag — after HLD-9.
  <!-- hld: prio=med size=S labels=release -->

## v0.2.0 — Deeper, and on a dashboard <!-- ms: phase=next -->

- [ ] **HLD-12 — RTMP connect and play**: after the handshake, `connect` to the
  application and `play` a named stream long enough to receive one video tag, so the
  ingest is proven to carry media, not only to answer; opt-in, because it appears in
  the server's log. <!-- hld: prio=med size=M labels=probe -->
- [ ] **HLD-13 — Low-latency as a player**: blocking playlist reloads
  (`_HLS_msn`, `_HLS_part`), preload hints, part-level latency — for the 2 s channels.
  <!-- hld: prio=med size=M labels=probe -->
- [ ] **HLD-14 — Prometheus exposition**: a `serve` mode that probes the list on an
  interval and exposes `hlsdoctor_playable`, `_behind_seconds`, `_segment_latency_seconds`
  per target and variant, labels never carrying a query string.
  <!-- hld: prio=med size=M labels=enhancement -->
- [ ] **HLD-15 — Segment continuity**: for MPEG-TS, the continuity counters and the PCR
  of the first packets; for fMP4, the `tfdt` base media decode time against the
  previous segment — a stuck encoder that still writes files.
  <!-- hld: prio=low size=M labels=probe -->
- [ ] **HLD-16 — DASH**: the same probe for an MPD with SegmentTemplate — the farm's
  players use HLS today, but the packagers emit both. <!-- hld: prio=low size=L labels=probe -->
