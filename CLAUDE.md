# CLAUDE.md

Guidance for Claude Code when working in this repository.

## What this repo is

`hlsdoctor` is a **single static Go binary** that probes a stream the way a player
would: HLS master → every variant → a second fetch after one target duration → the
newest segment's first bytes; RTMP up to the end of the handshake. It prints a table,
findings with a verdict, or JSON. Read-only, no dependencies beyond the standard
library, MIT, published by the HiWay Media org and dogfooded on its video farm's
channels.

`thoughts/HLD-1-stream-doctor/00-brief.md` is the task definition; the QRSPI run's
artifacts sit beside it.

## Layout

```
cmd/hlsdoctor/main.go        the CLI: check · ls · version; flags; run() is what the tests call
internal/m3u8/               the parser: Playlist, Variant, Rendition, Segment; ParseAttributes; Resolve
internal/fetch/              one GET with a byte budget and timings; ForIP pins a connection; Redact
internal/hls/                the probe: Probe → Report{Variants[].Playlist{Segments}}; Container()
internal/rtmp/               the handshake: Probe → Result{Stage, Echo, ServerVersion, timings}
internal/findings/           Policy, Evaluate, EvaluateRTMP, Nodes, Worst, ExitCode
internal/render/             the table and the findings text
internal/version/            Version, set by -ldflags at release
testdata/                    master, live, VOD and low-latency playlists
deploy/nomad/                the periodic batch job spec; deploy/streams.example.txt
Dockerfile                   the image: static binary on distroless static-debian13:nonroot; VERSION via build-arg
scripts/check-repo.sh        the repo's invariants (VERSION ↔ CHANGELOG, README statements, the findings table); CI runs it
scripts/backlog.mjs          lint · roadmap · check · issues — Node, tooling only (package.json is private)
site/build.mjs               generates site/dist/index.html FROM README.md
.github/workflows/           ci.yml (gofmt, vet, test, static builds, check-repo, backlog), release.yml (tag v*:
                             binaries + checksums + GitHub release + milestone), release-drift.yml (VERSION with
                             no tag for 2 h), docker.yml (image: smoke on PR, :main, :<version> on tag),
                             pages.yml, backlog-issues.yml
VERSION                      the one version; CHANGELOG.md must have its section; the tag is v<VERSION>
BACKLOG.md / ROADMAP.md      single source of truth (HLD-n ids) / generated view
```

## The rules the code encodes

1. **Read-only.** `GET` on playlists and segments; on RTMP the handshake and nothing
   after it — no `connect`, `publish` or `play`, so no stream key is ever needed.
2. **Never print what could carry a token.** Every URL goes through `fetch.Redact`
   (query string and userinfo stripped, path kept; on `rtmp(s)://` the last path
   element — the stream key — replaced by `…`) before it is stored in a report or a
   finding; transport errors are redacted too; request headers are never printed;
   the JSON output has no field for them. A new field that holds a URL takes the
   redacted form.
3. **A failing fetch is a finding, never a crash.** `hls.Probe` and `rtmp.Probe` return
   a report whatever happens; the CLI's exit code is 0 unless `--exit-on` says otherwise.
4. **Facts in the probe, verdicts in findings.** `internal/hls` and `internal/rtmp`
   record what happened; `internal/findings` decides what it means and every threshold
   is in `Policy` with a flag. Tests for a verdict build a report by hand.
5. **The probe is a player, not a validator.** One target duration between the two
   fetches, the newest segment, the first 64 KiB: enough to say playable or not within
   a few seconds per stream. Conformance auditing is `mediastreamvalidator`'s job.
6. **Worst first, exit 0 by default.** The checkfleet contract: a check that ran is a
   success; `--exit-on` is the gate.
7. **Tolerant parsing.** An unknown tag is counted in `Playlist.Tags`, never fatal;
   the one fatal parse error is a missing `#EXTM3U`, which is the `not-a-playlist`
   finding. Add a tag by extending the switch and a fixture.

## Facts the code depends on (dated — re-verify before every tag)

- **RFC 8216** (HLS, 2017-08; read 2026-09-24): a media playlist must contain at least
  three target durations of segments once it has been running (§6.2.2); EXTINF must not
  exceed EXT-X-TARGETDURATION after rounding to the nearest integer (§4.3.3.1); a live
  playlist reloads no more often than the target duration and a client that sees no
  change waits half a target duration before trying again (§6.3.4) — hence `--wait`
  defaults to one target duration. EXT-X-PROGRAM-DATE-TIME applies to the segment
  that follows it and to the ones after by adding durations (§4.3.2.6).
- **Low-latency HLS** (HLS 2nd edition draft, EXT-X-PART, EXT-X-PART-INF,
  EXT-X-SERVER-CONTROL, EXT-X-PRELOAD-HINT): parts are counted, not fetched; a growing
  part count counts as advancement.
- **MPEG-TS**: 188-byte packets each starting with sync byte `0x47`. **fMP4**: a box
  header is 4 bytes of size then 4 bytes of type; segments start with `styp` or `moof`,
  init sections with `ftyp`; `sidx`, `prft`, `emsg` may precede.
- **RTMP handshake** (Adobe RTMP specification 1.0, §5.2): C0/S0 one byte, version 3;
  C1/S1/S2/C2 1536 bytes — 4 bytes time, 4 bytes zero (or version in some servers),
  1528 bytes random; S2 echoes C1's random bytes, C2 echoes S1's. Default port 1935;
  rtmps on 443.
- **Two public streams used for smoke tests** (2026-09-24): Unified Streaming's
  `demo.unified-streaming.com/k8s/live/stable/scte35.isml/.m3u8` (live, 3 s target,
  three rungs including audio-only, ~600 s window) and Mux's
  `test-streams.mux.dev/x36xhzz/x36xhzz.m3u8` (VOD, seven rungs). Neither is under our
  control; a failing smoke run there is news, not a test failure — CI does not call them.
- **HiWay's farm** (devops_hiway `docs/infrastructure`, 2026-07): restreamers,
  liveclip and encoders publish HLS behind the CDN and take RTMP ingest; the weekly
  checks in TeamCity produce findings (exit 0/1/2) into InfraDigest — `--exit-on` and
  `--json` are shaped for that.

## Verifying a change

```bash
gofmt -l cmd internal && go vet ./... && go test ./... -count=1
./scripts/check-repo.sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/hlsdoctor ./cmd/hlsdoctor
go run ./cmd/hlsdoctor check https://demo.unified-streaming.com/k8s/live/stable/scte35.isml/.m3u8
npm run backlog && npm run build:site
docker build --build-arg VERSION=v0.0.0-test -t hlsdoctor:test . && docker run --rm hlsdoctor:test
```

Against the farm: `hlsdoctor check --from streams.txt` on the real channels for a week,
the findings that were true and the ones that were noise recorded in the README with a
date. That record is the 0.1.0 gate.

## Conventions

- BACKLOG.md first: every idea is a `HLD-n` item; shipped items say `ver=`. Regenerate
  ROADMAP.md; `check` fails when it is stale.
- CHANGELOG under `[Unreleased]` in the same pull request as the change.
- A new finding code goes in three places: `findings.Evaluate`'s doc comment, the
  README table, a case in `findings_test.go`. `check-repo.sh` compares the first two.
- Prose in English, British-leaning spelling, em-dashes, no marketing filler, no
  decorative emoji (the finding glyphs in the terminal output are functional).
- Standard library only. A dependency needs a reason written in this file.
