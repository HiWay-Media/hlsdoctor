# Changelog

All notable changes to hlsdoctor. The format is [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [SemVer](https://semver.org/). Items reference their `HLD-n` backlog id.

## [Unreleased]

### Changed
- `fetch.Redact` hides the last path element of an `rtmp(s)://` URL — the stream key —
  as well as the query string; found by the HLD-1 Questions phase (Q6).

### Added
- `thoughts/HLD-1-stream-doctor/00-questions.md`: the QRSPI Questions phase, ten
  questions with proposed defaults, awaiting answers (HLD-1).

## [0.0.1] — 2026-09-24

Not released: the first working binary, tested against local fakes and two public
streams, before the QRSPI design run and before a week on the farm's channels.

### Added
- The M3U8 parser: master and media playlists, RFC 8216 tags plus the low-latency
  extensions, tolerant of unknown tags, quoted attribute lists (HLD-2).
- The HLS probe: master → every variant, a second fetch after one target duration to
  see the live edge move, the newest segment downloaded and its container recognised
  (MPEG-TS sync byte, fMP4 box, or text where media should be), latency from
  EXT-X-PROGRAM-DATE-TIME (HLD-3).
- The RTMP handshake probe: C0/C1 → S0/S1/S2 → C2, version and echo checked, nothing
  sent after it (HLD-4).
- Findings with the checkfleet contract — stale, segment-corrupt, behind-live,
  ladder-skew, nodes-disagree and the rest — worst first, exit 0 unless `--exit-on` (HLD-5).
- `check`, `ls`, `version`; `--from FILE`; `--each-ip` to probe every address behind
  a DNS name and compare them; query strings and headers never printed (HLD-6).
- Fixtures, an httptest origin and a fake RTMP server, unit and end-to-end tests; CI
  with static builds; release by tag; the periodic Nomad job; the site from the
  README; the backlog with issue sync (HLD-7, HLD-8).
