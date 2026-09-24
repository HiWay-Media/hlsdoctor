# 00 · Questions — HLD-1 A probe that says whether an HLS or RTMP stream is playable

The default assumption is what makes this phase non-blocking: work can proceed
without waiting for answers, and the assumptions are on the record. Every question
below is grounded in what the code does today (repo-root-relative path, line numbers,
read 2026-09-24 on `main` at 0.0.1), so the answer changes a named line, not a mood.

---

## Ticket

**ID:** HLD-1
**Link:** `BACKLOG.md` lines 33-39 (https://github.com/hiway-media/hlsdoctor)
**Title:** Run QRSPI on the brief: Questions → Research → Spec → Plan

Brief (`thoughts/HLD-1-stream-doctor/00-brief.md`): HiWay's farm publishes live
channels as HLS behind a CDN and takes RTMP in from encoders and partners. The checks
that exist ask whether a host answers, not whether the stream plays. hlsdoctor is one
static binary that does what a player does — master, every variant, a second fetch
after one target duration, the newest segment's first bytes — and, for RTMP, the
handshake, and reports a table, findings with a verdict, or JSON. Read-only; no token
is ever printed.

Done when: `hlsdoctor check <url>` reports per variant whether the playlist advanced,
how far behind live it is, whether the newest segment is media, and for RTMP whether
the handshake completed — worst first, exit 0 unless `--exit-on`; `check --from
streams.txt` runs every five minutes for a week against the farm's channels with every
finding classified true or noise and every known outage checked for a finding, tally
in the README, dated; a periodic Nomad job or a TeamCity step runs it fleet-wide from a
release binary; `go test ./...` runs without a stream; CI builds static linux/amd64,
linux/arm64, darwin/arm64.

Constraints: standard library only; every printed URL has its query string and
userinfo stripped, request headers are sent and never printed; a failing fetch is a
finding, never a crash; every threshold lives in `Policy` with a flag; a channel with
five rungs is judged in about one target duration plus a few round trips, so forty
channels fit a five-minute period.

The backlog item names four design questions this phase must carry: the default
thresholds (how far behind live is a finding for a 6 s target versus a 2 s low-latency
one); whether an audio-only rung, a subtitles rendition and an I-frame playlist are
probed or only counted; what `--each-ip` should do when the CDN answers from anycast;
and whether the farm wants the probe as a Nomad job, a TeamCity step or both. They are
Q1, Q4, Q5 and Q2 below.

Already settled by the brief, not asked again: read-only (no `connect`/`publish`/`play`
until v0.2.0), no decoding, no DRM, no DASH, no Prometheus, standard library only, the
three build targets, the week-long tally as the release gate.

---

## Questions

### Q1 · What is the default `behind-live` limit for a 6 s channel and for a 2 s low-latency one?

- **Today:** `Policy.MaxBehind` is 0, which `findings.playlist` turns into three target
  durations (`internal/findings/findings.go:39-41`, `185-192`); `--max-behind` overrides
  it in seconds for every target at once (`cmd/hlsdoctor/main.go:90`). `Behind` is
  wall-clock minus the end of the last full segment, by EXT-X-PROGRAM-DATE-TIME
  (`internal/hls/probe.go:303-310`) — parts are counted for advancement
  (`probe.go:234`) but never enter the latency, so on a low-latency playlist
  (`testdata/media-llhls.m3u8:3-5`: target 2, PART-TARGET 0.5, PART-HOLD-BACK 1.5) the
  reading is up to one target duration higher than the true edge. The second fetch
  waits one target duration, floored at 2 s and capped at `--max-wait 15s`
  (`probe.go:40`, `216-227`). The probe host's clock is trusted as is.
- **Risk if unresolved:** the limit is the single biggest source of noise or silence in
  the week's tally. Three target durations is 18 s on a 6 s channel — a player sits
  there, so it only fires on real drift — but 6 s on a 2 s low-latency channel is four
  times what a player tolerates (PART-HOLD-BACK 1.5 s), so a low-latency channel that
  has quietly become a 6 s channel stays `playable`. An absolute number instead would
  fire on every ordinary 6 s channel.
- **Options:** (a) keep three target durations for every playlist; (b) when
  EXT-X-PART-INF is present, use three PART-HOLD-BACK (or three part targets) instead;
  (c) an absolute default in seconds; (d) a second flag, `--max-behind-ll`.
- **Proposed default:** (a) for v0.1.0, with the limit and the measured `Behind` both
  in the JSON so the week's tally can say what the right multiple is. Reason: with
  parts outside the latency measurement a tighter low-latency limit would fire on the
  probe's own granularity, not the stream's; part-level latency is HLD-13
  (`BACKLOG.md:78-80`), and HLD-9 exists to recalibrate this number from data rather
  than from a guess. The design should state the multiple in one place and the README
  table should say "3× target duration" against `behind-live` (`README.md:73`) exactly
  as the code does.
- **Answer:** _(to be filled — human)_

### Q2 · Does the farm run the probe as a Nomad periodic job, a TeamCity step, or both — and at which alarm level?

- **Today:** `deploy/nomad/hlsdoctor.nomad.hcl` is a `batch` job with
  `periodic { crons = ["*/5 * * * *"], prohibit_overlap = true }` (lines 21-24), driver
  `raw_exec` (29), the release binary as an `artifact` with the checksum commented out
  (30-35), the stream list inline in a `template` (37-44), and
  `check --from local/streams.txt --exit-on bad --no-ok --timeout 10s` (48), so the
  allocation's exit status is the alarm and WARN never alarms. `README.md:103-106` says
  the same command fits a TeamCity build step; `CLAUDE.md:87-90` records that the farm's
  weekly checks already run in TeamCity and feed findings (exit 0/1/2) into InfraDigest.
  Nothing consumes `--json` yet.
- **Risk if unresolved:** the deliverable differs: a Nomad job needs a namespace, a
  client with `raw_exec` enabled, a host volume or a sink for the output, and a way to
  see a failed allocation; a TeamCity step needs an agent slot every five minutes or a
  different cadence, and produces build artifacts by itself. Building both blind
  doubles the work; building the wrong one means the week never happens.
- **Options:** (a) Nomad only, five-minute cadence, allocation status as the alarm;
  (b) TeamCity only, on the cadence the existing checks use; (c) both — Nomad every
  five minutes as the alarm, a TeamCity step in the existing weekly checks emitting
  `--json` into InfraDigest; (d) cron on one farm host for the week, decide after.
- **Proposed default:** (c), with `--exit-on bad` kept for the alarm and WARN collected
  in the JSON for the tally rather than paged. Reason: the two cadences already exist
  for two consumers — a five-minute probe is an alarm, a weekly digest is a report —
  and the job spec and the JSON output were shaped for exactly that split. The design
  must name the Nomad namespace, whether `raw_exec` is allowed on the farm's clients,
  and where InfraDigest reads from.
- **Answer:** _(to be filled — human)_

### Q3 · Where do a week of findings live, and how is each one classified true or noise?

- **Today:** `check` writes to stdout only — text or, with `--json`, one document per
  run with `at`, `version`, `targets`, `findings`, `worst`
  (`cmd/hlsdoctor/main.go:273-277`). There is no output file flag, no append mode and
  no run identifier beyond `at`. A Nomad batch allocation's stdout is garbage-collected
  with the allocation; the job spec keeps nothing (`deploy/nomad/hlsdoctor.nomad.hcl:46-49`).
  HLD-9 (`BACKLOG.md:60-64`) wants roughly 2,000 runs' worth of findings kept,
  classified, and checked against the week's known outages.
- **Risk if unresolved:** the release gate is the tally; without a sink from day one
  the week starts over. If classification is left to the end, 2,000 runs × forty
  targets of `playable` lines bury the hundred findings that matter.
- **Options:** (a) the runner keeps the output — the job redirects `--json` stdout to
  a dated file on a host volume, or TeamCity keeps it as a build artifact — and the
  classification is a table beside the README tally, built with `jq` grouped by code
  and target; (b) hlsdoctor gains `--out FILE` (append, one JSON document per line);
  (c) the job ships stdout to the farm's log store and the tally is a query there;
  (d) a `serve` mode with retention — that is HLD-14 territory.
- **Proposed default:** (a), and the job runs with `--no-ok` off for the week so OK
  lines are in the file (the "checked for a finding" half of the tally needs them).
  Reason: the binary stays a probe, the sink is a deployment concern, and a week's data
  at forty targets is a few hundred megabytes of JSON — a file on a volume and `jq`
  are enough. The design must name the path, the retention and who owns the
  classification pass.
- **Answer:** _(to be filled — human)_

### Q4 · Are an audio-only rung, a subtitles rendition and an I-frame playlist probed or only counted?

- **Today:** three different fates. An audio-only EXT-X-STREAM-INF is a variant and is
  probed like the others — `Variant.AudioOnly()` (`internal/m3u8/m3u8.go:61-63`) only
  exempts it from the RESOLUTION check (`internal/findings/findings.go:119`) and labels
  it `audio@75k`; it takes part in `ladder-skew` (`findings.go:137-151`). EXT-X-MEDIA
  renditions (alternate audio, subtitles, closed captions) are parsed with their URI
  (`m3u8.go:252-255`) but only counted: `rep.Renditions = len(pl.Renditions)`
  (`internal/hls/probe.go:157`). EXT-X-I-FRAME-STREAM-INF marks the playlist as a
  master and is otherwise dropped — not even counted (`m3u8.go:250-251`). If a
  subtitles rendition were probed today its WebVTT segments would come back as
  container `unknown` — `Container()` knows TS, fMP4, ADTS/ID3 and text, not `WEBVTT`
  (`probe.go:340-361`) — and be reported BAD `segment-corrupt` (`findings.go:221-223`).
  HLD-10 (`BACKLOG.md:66-68`) is the backlog item for probing renditions, in the v0.1.0
  milestone.
- **Risk if unresolved:** a dead alternate-audio track is an outage a viewer hears and
  the probe cannot see; probing subtitles without a WebVTT rule makes every channel
  with captions BAD from day one of the week; an I-frame playlist that is not even
  counted cannot be reported missing.
- **Options:** (a) count only, in v0.1.0 — HLD-10 later; (b) probe renditions that
  carry a URI the way variants are probed, subtitles included with a WebVTT container
  rule; (c) probe audio renditions with a URI, fetch a subtitles playlist for
  advancement only (no segment verdict), count I-frame playlists; (d) as (c) plus
  probe I-frame playlists.
- **Proposed default:** (c). Reason: alternate audio is media a player switches to and
  is worth the same verdict as a rung; a subtitles playlist that stops advancing is
  visible without inventing a container rule the farm may never exercise; an I-frame
  playlist serves trick play, not playback, so a count in the report (`Report` gains an
  `IFramePlaylists` field beside `Renditions`, `probe.go:124`) is enough for the
  verdict "is it playable". The design should decide whether renditions carry their
  own `ladder-skew` comparison or stay out of it.
- **Answer:** _(to be filled — human)_

### Q5 · What should `--each-ip` do when the name is a CDN answering from anycast, or returns addresses this host cannot reach?

- **Today:** `fetch.ResolveIPs` returns every A and AAAA record
  (`internal/fetch/fetch.go:121-139`); `main.probe` runs one full probe per address
  with the connection pinned in `DialContext` and the Host header and TLS name kept
  (`cmd/hlsdoctor/main.go:196-217`, `fetch.go:42-44`, `65-73`), then `findings.Nodes`
  reports BAD `nodes-disagree` when one address fails while another answers and WARN
  `nodes-skew` when their newest sequences differ by more than `--skew`
  (`internal/findings/findings.go:289-346`). A resolution failure is a single BAD
  `unreachable` (`main.go:197-201`). Nothing dedupes, caps or classifies the addresses;
  the fleet job does not pass `--each-ip` (`deploy/nomad/hlsdoctor.nomad.hcl:48`).
- **Risk if unresolved:** behind an anycast CDN every address is the same nearest edge,
  so N addresses cost N × (variants × 2 fetches + segments) for no information; a CDN
  that returns 4-8 addresses per lookup changes the set every TTL, so two runs are not
  comparable; an AAAA record on a probe host without an IPv6 route fails at dial and
  becomes a BAD `nodes-disagree` against a healthy channel — a false alarm on every
  run of the week.
- **Options:** (a) leave it, document `--each-ip` as a tool for origin names with a
  handful of A records; (b) drop an address family from the comparison when its dial
  fails with "no route" / "network is unreachable", so it is neither a node nor a
  disagreement; (c) cap the addresses probed (`--max-ips`, default 8) and say so in a
  finding; (d) collapse addresses that share a prefix or an edge (not knowable from
  the address); (e) probe the name once and one address per family in addition.
- **Proposed default:** (a) plus (b). Reason: `--each-ip` answers "do the origins behind
  this round-robin name agree", which is the farm's origin question, not the CDN's;
  anycast makes the question meaningless rather than the tool wrong, and the README
  can say so. The unreachable-family rule removes the one false BAD that is certain to
  occur. The design must decide whether the comparison keys on the address string
  (today) or on the family, and whether a name that yields more than eight addresses
  is refused or trimmed.
- **Answer:** _(to be filled — human)_

### Q6 · Should an RTMP URL be redacted below the path — the stream key is the last path element?

- **Today:** `fetch.Redact` strips the query string and userinfo and keeps the path,
  "so a finding still names the stream" (`internal/fetch/fetch.go`), and every URL in
  a report or finding goes through it, RTMP included (`internal/rtmp/rtmp.go:46`).
  Since 2026-09-24 (the commit that closed this phase) it also applies option (b)
  below on `rtmp(s)://` as the safe default — `rtmp://host/app/<key>` prints as
  `rtmp://host/app/…` — pending this answer; the question is whether that is the
  final shape and whether signed HLS paths need the same. The RTMP probe uses only the host and port
  (`rtmp.go:53-60`); the application and stream name in the path are never sent. The
  example list has `rtmp://origin.example.com/live` (`deploy/streams.example.txt:5`),
  but the ingest URL an encoder or partner is given is conventionally
  `rtmp://host/app/<stream-key>`, and that is the line an operator will paste.
- **Risk if unresolved:** the brief's constraint "no token is ever printed" holds for
  HLS and fails for RTMP the first time a real ingest URL is listed: the stream key
  appears in every finding, in the JSON, in the InfraDigest, in the allocation log. A
  signed-path HLS URL (`/<token>/master.m3u8`, as some CDNs do) has the same shape.
- **Options:** (a) keep the path for both; (b) for `rtmp(s)://`, print
  `scheme://host:port/app` — drop the last path element when there are two or more —
  since nothing after the application is used; (c) print `scheme://host:port` only;
  (d) a `--redact-path` flag that keeps only the first N elements, for both schemes.
- **Proposed default:** (b), HLS paths unchanged. Reason: hlsdoctor never needs the key
  (rule 1, `CLAUDE.md:42-43`), so printing it is pure liability, while the application
  name is what distinguishes one ingest from another; an HLS path names the channel
  and stripping it would make the findings anonymous. The design should decide whether
  redaction of a signed HLS path is a v0.1.0 flag or a documented limitation.
- **Answer:** _(to be filled — human)_

### Q7 · How does a forty-channel list fit a five-minute period when several channels are down?

- **Today:** `--concurrency 4` bounds both the targets probed at once
  (`cmd/hlsdoctor/main.go:226-241`) and the variants within a target
  (`internal/hls/probe.go:164-179`); `--timeout 10s` is a whole-request timeout
  (`main.go:79`, `internal/fetch/fetch.go:52`) over a 5 s dial and a 10 s
  response-header wait (`fetch.go:57`, `61`); the RTMP handshake gets the same 10 s for
  dial, TLS and handshake together (`internal/rtmp/rtmp.go:61-64`). A healthy 6 s
  channel costs one target duration plus a few round trips; a channel whose origin
  drops packets costs up to 10 s per rung in waves of four before the second fetch is
  skipped; there is no overall deadline. The Nomad job has `prohibit_overlap = true`
  (`deploy/nomad/hlsdoctor.nomad.hcl:23`), so an overrun skips the next period
  silently and no finding says so.
- **Risk if unresolved:** the brief's "forty channels in five minutes" is true for
  forty healthy channels — ten waves of roughly seven seconds — and false on the bad
  day the probe exists for: ten dead origins with five rungs each add about 200 s, the
  run overruns, the period is skipped, and the outage is under-sampled exactly when it
  matters.
- **Options:** (a) keep the defaults, pin `--concurrency 8 --timeout 5s` in the job
  spec and document the arithmetic; (b) a `--deadline` after which the targets not yet
  probed are reported as ERROR findings and the run exits; (c) decouple the two
  concurrency knobs (`--concurrency` for targets, `--variant-concurrency` for rungs);
  (d) a 10-minute period.
- **Proposed default:** (a) for the week, (b) considered in the design if the
  arithmetic does not close. Reason: nothing in the binary needs to change to make
  forty channels fit — the job's flags do — and a skipped period is visible in
  `nomad job status`; a deadline is a new failure mode to test. The design should
  write the worst-case number down against the farm's actual list length and target
  durations.
- **Answer:** _(to be filled — human)_

### Q8 · Do per-target options belong in the `--from` list, given that `--header` and `--each-ip` apply to every line?

- **Today:** a list is one target per line with `#` comments
  (`cmd/hlsdoctor/main.go:114-128`, `deploy/streams.example.txt`); every flag is global.
  `--header Name=value` is set once on the client and sent to every HTTP target in the
  run (`main.go:188-189`) — including the third-party hosts on a mixed list. The
  thresholds, `--each-ip`, `--wait` and `--segments` are likewise per run.
- **Risk if unresolved:** the farm's list mixes 6 s CDN channels, 2 s low-latency
  channels, origins worth `--each-ip` and RTMP ingests; one set of flags either
  over-probes the CDN or under-probes the origins, and a CDN token header meant for
  one host is sent to all of them, which is the one place the "never leaks a token"
  promise has a hole on the sending side rather than the printing side.
- **Options:** (a) keep the format; one list per policy, several `check --from`
  invocations in the job; (b) per-line flags after the URL
  (`https://… --each-ip --max-behind 12`); (c) a per-line `key=value` suffix, a
  narrower grammar; (d) scope `--header` to the host of the first target unless
  `--header host:Name=value`.
- **Proposed default:** (a), with the README stating that `--header` goes to every
  target on the list. Reason: per-line flags are a small language with parsing,
  precedence and error cases to test, and the farm has three classes of target at most
  — three lists in one job spec express that without a grammar. The design should
  decide whether the header scope in (d) is cheap enough to add regardless.
- **Answer:** _(to be filled — human)_

### Q9 · Is `rtmp-echo` (S2 does not echo C1) a WARN for the farm's ingest servers, or an OK?

- **Today:** `EvaluateRTMP` makes a completed handshake whose S2 does not echo C1's
  random bytes a WARN `rtmp-echo` — "the server is not a compliant RTMP endpoint, most
  players tolerate it" (`internal/findings/findings.go:264-283`); the echo is a byte
  comparison of the 1528 random bytes (`internal/rtmp/rtmp.go:117`). Servers that
  implement the digest ("complex") handshake used by Flash-era clients — Wowza, FMS,
  some nginx-rtmp builds — legitimately answer with a digest in S2, not a plain echo;
  which server the farm's restreamers and encoders speak to is not in this repository.
- **Risk if unresolved:** if the farm's ingests use a digest handshake, every RTMP
  target is WARN on every run of the week — noise that trains the reader to ignore
  the RTMP column; if they echo and one day stop, a WARN is the only signal that the
  endpoint has been swapped for something that is not an RTMP server. The 10 s
  `--timeout` shared with HTTP (`cmd/hlsdoctor/main.go:79`) also decides how long a
  half-open ingest holds a slot.
- **Options:** (a) keep WARN through the week, decide from the tally; (b) OK with an
  informational message; (c) recognise the digest scheme (the version field of S1 is
  non-zero) and treat that as compliant, WARN only when neither echo nor digest fits;
  (d) a flag.
- **Proposed default:** (a). Reason: the level should follow what the farm's servers
  actually do, which is one week of data away, and (c) is protocol work that is only
  worth doing if the data says the WARN is noise. The design should name the ingest
  server type so the fake in `internal/rtmp/rtmp_test.go` matches it.
- **Answer:** _(to be filled — human)_

### Q10 · Are the JSON shape and the exit codes a frozen contract for InfraDigest in v0.1.0?

- **Today:** the JSON document is `{at, version, targets[], findings[], worst}` with
  `target{url, kind, hls[], rtmp, error, findings[]}` (`cmd/hlsdoctor/main.go:163-170`,
  `273-277`) and `Finding{level, code, url, node, variant, message}`
  (`internal/findings/findings.go:28-35`); durations are Go `time.Duration`
  nanoseconds. `ExitCode` maps the worst level to 0/1/2/3 and, under `--exit-on bad`,
  returns 3 for ERROR (`findings.go:373-393`) — while `CLAUDE.md:87-90` describes the
  TeamCity checks as producing 0/1/2. There is no schema version distinct from the
  binary version and no statement of what may change.
- **Risk if unresolved:** the first consumer written against this JSON pins it; a
  renamed field or a duration that becomes seconds breaks the digest silently. A
  `3` where the runner expects 0/1/2 is either "unknown" or "success", depending on
  the runner.
- **Options:** (a) freeze the shape as is, document it in the README as the contract,
  use `version` as the marker, and add a `schema` field only when it changes; (b) fold
  ERROR into 2 for the exit code; (c) emit durations as seconds or milliseconds before
  anyone depends on nanoseconds; (d) leave it unstated until v0.2.0.
- **Proposed default:** (a) plus (c) — decide the units now, because that is the one
  change that cannot be made later without breaking a reader — and keep 3 for ERROR,
  documented, since an invalid target is neither a stream failure nor a success.
  Reason: the week's tooling (Q3) will be the first consumer, so the contract is being
  fixed this month whether or not it is written down.
- **Answer:** _(to be filled — human)_

---

## Out of scope

Things the ticket might suggest but that we are **not** doing in this task:

- Sending anything after the RTMP handshake — `connect`, `play`, `publish` — even
  opt-in: HLD-12, v0.2.0.
- Blocking playlist reloads, preload hints, part-level latency for the 2 s channels:
  HLD-13. Q1 fixes a threshold, not a low-latency player.
- A `serve` mode, Prometheus metrics, any retention inside the binary: HLD-14.
- Decoding, continuity counters, `tfdt` checks, DRM keys, picture quality: HLD-15 and
  the brief's exclusions.
- DASH: HLD-16.
- Changing the finding codes' names or adding codes beyond what Q4, Q5 and Q6 imply
  (`nodes-*` behaviour, an I-frame count, an RTMP redaction rule).
- A dependency of any kind; Node stays tooling-only (`scripts/`, `site/`).

---

## Status

- [x] Questions generated
- [ ] Reviewed by a human
- [ ] Answers collected (or assumptions explicitly accepted)

> Next phase: **Research**. The ticket is **not** passed to Research — only the
> questions and their answers.
