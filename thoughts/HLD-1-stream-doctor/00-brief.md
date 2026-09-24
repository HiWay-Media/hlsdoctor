# HLD-1 — Brief: a probe that says whether an HLS or RTMP stream is playable

**Repository:** https://github.com/hiway-media/hlsdoctor · **Ticket:** HLD-1 in `BACKLOG.md`
**Date:** 2026-09-24 · **Author:** Allan Nava (HiWay Media), with Claude

## Goal

HiWay's farm publishes live channels as HLS behind a CDN and takes RTMP in from
encoders and partners. The checks that exist ask whether a host answers, not whether
the stream plays: a frozen playlist, an error page served as a segment, a rung ten
segments behind the others and a dead origin behind a round-robin name all pass a URL
monitor. hlsdoctor is one static binary that does what a player does — master, every
variant, a second fetch after one target duration, the newest segment's first bytes —
and, for RTMP, the handshake, and reports a table, findings with a verdict, or JSON.
Read-only; no token is ever printed.

## Done when

- `hlsdoctor check <url>` on a live channel prints, per variant, whether the playlist
  advanced, how far behind live it is, and whether the newest segment is media; on an
  RTMP endpoint whether the handshake completed; worst first; exit 0 unless `--exit-on`.
- `hlsdoctor check --from streams.txt` run every five minutes for a week against the
  farm's channels; each finding classified true or noise; each known outage in that
  week checked for a finding; the tally in the README, dated.
- A periodic Nomad job (or a TeamCity step — the design decides) runs it fleet-wide from
  a release binary.
- `go test ./...` runs without a stream; CI builds static linux/amd64, linux/arm64,
  darwin/arm64.

## In scope

- The M3U8 parser (RFC 8216 plus low-latency tags), the HLS probe, the RTMP handshake,
  the findings, the CLI with `--from`, `--each-ip`, `--json`, the job spec, the docs.

## Out of scope

- Publishing, playing or transcoding anything: no `connect`/`publish`/`play` on RTMP
  in this milestone (v0.2.0 adds an opt-in `play`).
- Decoding media, verifying DRM keys, measuring picture quality.
- DASH and a Prometheus exporter (v0.2.0).

## Constraints

- Standard library only; static binary; Node only for the repository's tooling.
- Every printed URL has its query string and userinfo stripped; request headers are
  sent and never printed; the JSON has no field for them.
- A failing fetch is a finding, never a crash; the thresholds all live in `Policy` and
  have a flag.
- Fast: a channel with five rungs is judged in about one target duration plus a few
  round trips, so a list of forty channels fits in a five-minute period.
