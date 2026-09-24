<p align="center"><img src="https://raw.githubusercontent.com/hiway-media/hlsdoctor/main/assets/logo.svg" width="96" height="96" alt="hlsdoctor"></p>

# hlsdoctor — is the stream actually playable? HLS and RTMP, probed like a player, with a verdict

A stream can answer `200 OK` on its master playlist and be dead: the packager stopped
writing and the playlist is frozen, the CDN serves an HTML error page where a segment
should be, one rung of the ladder is ten segments behind the others, one of the three
origins behind the DNS name is stale, the RTMP ingest accepts TCP and never finishes
the handshake. A URL monitor does not see any of that. hlsdoctor is one static binary
that does what a player does — master, every variant, wait one target duration, fetch
again, download the newest segment, look at its first bytes — and reports the result as
a table, as findings with a verdict, and as JSON. It **reads only**: GET on playlists and
segments, a TCP handshake on RTMP; it never sends `connect`, `publish` or `play`. Query
strings and userinfo are stripped from every URL it prints and request headers are
never printed, because that is where tokens live.

```
$ hlsdoctor ls https://demo.unified-streaming.com/k8s/live/stable/scte35.isml/.m3u8
hlsdoctor · https://demo.unified-streaming.com/k8s/live/stable/scte35.isml/.m3u8 · 2026-09-24 08:18:15Z · master, 3 variant(s), 2 rendition(s)
│ variant        │ bw    │ resolution │ codecs                │ kind │ target │ segs/window │ seq       │ advanced │ behind │ fetch │ newest segment │
├────────────────┼───────┼────────────┼───────────────────────┼──────┼────────┼─────────────┼───────────┼──────────┼────────┼───────┼────────────────┤
│ 1280x720@658k  │ 658k  │ 1280x720   │ mp4a.40.2,avc1.42C01F │ live │ 3      │ 313/601s    │ 932415570 │ yes      │ 6s     │ 158ms │ ts 150k 236ms  │
│ 1280x720@1316k │ 1316k │ 1280x720   │ mp4a.40.2,avc1.42C01F │ live │ 3      │ 312/599s    │ 932415569 │ yes      │ 8s     │ 163ms │ ts 156k 221ms  │
│ audio@75k      │ 75k   │            │ mp4a.40.2             │ live │ 3      │ 313/601s    │ 932415570 │ yes      │ 6s     │ 143ms │ ts 21k 168ms   │

$ hlsdoctor check https://cdn.example.com/live/channel-1/master.m3u8?token=…
🔴 BAD   stale                854x480@1200k    newest segment still 1044 after 6s (target duration 6s) — the packager stopped writing
      https://cdn.example.com/live/channel-1/master.m3u8?…
🔴 BAD   segment-corrupt      854x480@1200k    segment 1044 is text, not media — an error page served as HTTP 200 (38 bytes)
      https://cdn.example.com/live/channel-1/master.m3u8?…
🔴 BAD   http-status          640x360@600k     HTTP 404 for the media playlist (2ms)
      https://cdn.example.com/live/channel-1/master.m3u8?…
🟢 OK    playable             1280x720@2500k   live, 5 segments / 30s window, target 6s, advanced to 1045 after 6s, 9s behind live, segment 1045 ts 752 bytes in 1ms
      https://cdn.example.com/live/channel-1/master.m3u8?…

4 findings: 1 OK, 0 WARN, 3 BAD, 0 ERROR
```

> **Status: implemented and tested against local fakes and two public streams; not yet
> run for a week against the farm's channels.** The parser follows RFC 8216 and the
> low-latency extensions, the probe does what a player does, the findings are
> unit-tested case by case. A week of `check` runs against real channels — which
> findings were true, which were noise, what broke without one — is the gate on 0.1.0
> (`BACKLOG.md`). Built for the HiWay Media video farm, where the restreamers, the
> live-clipping service and the encoders publish HLS over a CDN and take RTMP in.

## What it checks

| Question | How |
|---|---|
| Does the master parse, and what is the ladder | `GET` the URL; EXT-X-STREAM-INF with BANDWIDTH, RESOLUTION, CODECS, FRAME-RATE; EXT-X-MEDIA renditions counted |
| Does every variant answer, and is it live or VOD | `GET` each media playlist; EXT-X-ENDLIST, EXT-X-PLAYLIST-TYPE, EXT-X-TARGETDURATION, the window as the sum of EXTINF |
| Is the live edge moving | a second `GET` after one target duration (`--wait`, capped by `--max-wait`): the newest sequence number must grow, or a new part appear |
| How far behind wall-clock is it | EXT-X-PROGRAM-DATE-TIME of the newest segment plus the durations after it, against now |
| Can the newest segment be fetched, and is it media | `GET` it, count the bytes, look at the first 64 KiB: a `0x47` sync byte every 188 bytes is MPEG-TS, an `ftyp`/`styp`/`moof` box is fMP4, `#EXTM3U` or `<html` is an error page |
| Can playback keep up | the segment's download time against its duration (`--slow`) |
| Are the rungs in step | the newest sequence numbers across variants (`--skew`) |
| Do all origins behind the name agree | `--each-ip`: resolve A/AAAA, probe each address with the URL's Host header and TLS name, compare |
| Is the RTMP ingest alive | `rtmp://` / `rtmps://`: TCP, the C0/C1 → S0/S1/S2 → C2 handshake, the version byte and the S2 echo, timed |

From those facts, the findings:

| Code | Level | Meaning |
|---|---|---|
| `unreachable` | BAD | the playlist could not be fetched: DNS, TCP, TLS, timeout |
| `http-status` | BAD | a playlist answered with a non-2xx status |
| `not-a-playlist` | BAD | the body is not M3U8 — an HTML error page behind a 200 |
| `empty-playlist` | BAD | a media playlist with no segments, or a master with no variant |
| `stale` | BAD | a live playlist did not advance within the wait |
| `segment-unreachable` | BAD | the newest segment could not be fetched, or answered non-2xx |
| `segment-corrupt` | BAD | the segment is empty, text, or has no TS sync byte and no MP4 box |
| `segment-slow` | BAD / WARN | the segment took longer than its duration to download (BAD), or longer than `--slow` × its duration (WARN, default 0.5) |
| `behind-live` | WARN | the live edge is more than `--max-behind` seconds behind wall-clock (default 3× target duration) |
| `short-window` | WARN | a live window shorter than `--min-window` (default 3× target duration, the minimum RFC 8216 asks for) |
| `long-segment` | WARN | an EXTINF longer than EXT-X-TARGETDURATION — players may refuse it |
| `ladder-skew` | WARN | variants' newest sequence numbers more than `--skew` apart (default 2) |
| `ladder-incomplete` | WARN | a variant without BANDWIDTH, RESOLUTION (video) or CODECS; two rungs with the same resolution and bandwidth |
| `gaps` | WARN | EXT-X-GAP segments in the window |
| `nodes-disagree` | BAD | with `--each-ip`: one address failed while another answered |
| `nodes-skew` | WARN | with `--each-ip`: addresses more than `--skew` segments apart |
| `rtmp-unreachable` | BAD | the RTMP endpoint did not accept the connection |
| `rtmp-handshake` | BAD | wrong version byte, connection closed mid-handshake, or timeout |
| `rtmp-echo` | WARN | the handshake completed but S2 did not echo C1 |
| `discontinuity` | OK | EXT-X-DISCONTINUITY in the window — an encoder restart or a splice (informational) |
| `playable` | OK | the variant passed every check (`--no-ok` hides it) |
| `rtmp-ok` | OK | the handshake completed (`--no-ok` hides it) |
| `invalid-target` | ERROR | not a URL, or a scheme other than http(s) and rtmp(s) |

Worst first. The exit code is 0 whatever the findings — a check that ran is a success —
unless `--exit-on warn|bad|error` asks for one, for CI, cron and check runners.

## Run it

**Once:**

```
hlsdoctor check https://cdn.example.com/live/channel-1/master.m3u8
hlsdoctor check --from streams.txt --exit-on bad --no-ok
hlsdoctor check https://cdn.example.com/live/channel-1/master.m3u8 --each-ip --json
hlsdoctor ls rtmp://origin.example.com/live
```

**On a schedule**, as a periodic Nomad batch job whose exit code is the alarm:
[`deploy/nomad/hlsdoctor.nomad.hcl`](deploy/nomad/hlsdoctor.nomad.hcl) probes the
streams in its template every five minutes with `--exit-on bad`. The same command fits
a TeamCity build step or a cron line; `--json` feeds anything that reads JSON.

**Flags:** `--timeout 10s` per request; `--wait` between the two fetches of a live
playlist (default one target duration, `--max-wait 15s` caps it, negative skips it);
`--segments 1` newest segments downloaded per variant, `--no-segments`; `--variants 0`
caps the rungs probed; `--concurrency 4`; `--each-ip`; the thresholds `--max-behind`,
`--min-window`, `--slow`, `--skew`; `--header Name=value` (repeatable, for a CDN token
header — sent, never printed), `--user-agent`, `--insecure-tls`; `--no-ok`, `--json`,
`--exit-on`.

## What it does not do

- Publish, play or transcode. It sends `GET` and an RTMP handshake and nothing else, so
  it needs no stream key and leaves nothing in the server's application log beyond a
  connection. A full `connect`/`play` on RTMP is a backlog item.
- Decode media. It recognises the container by its first bytes and trusts EXTINF for
  duration; a corrupt frame inside a well-formed segment is invisible to it.
- Verify DRM keys or fetch EXT-X-KEY: it does not read keys.
- Follow low-latency parts as a player would. It parses EXT-X-PART and EXT-X-SERVER-CONTROL
  and counts parts as advancement, but it does not use blocking reloads or preload hints.
- Replace a viewer. It says whether the stream can be played, not how it looks.

## Install

Static binaries for linux/amd64, linux/arm64 and darwin/arm64 on the
[releases page](https://github.com/hiway-media/hlsdoctor/releases), with checksums.
From source: `go install github.com/hiway-media/hlsdoctor/cmd/hlsdoctor@latest` (Go 1.27).
No dependencies beyond the standard library.

## Prior art

- [hlsprobe](https://github.com/grafov/hlsprobe) and [m3u8](https://github.com/grafov/m3u8):
  the Go parser most tools build on, and a monitor from 2014; hlsdoctor parses on its own
  to stay dependency-free and adds the second fetch, the segment look and the verdict.
- Apple's `mediastreamvalidator`: the authority on conformance, macOS only, no exit-code
  contract; hlsdoctor is the thing you run every five minutes on a Linux box.
- `ffprobe`: decodes everything and says nothing about staleness, ladders or origins.
- [checkfleet](https://github.com/Allan-Nava/checkfleet) and
  [gpuledger](https://github.com/hiway-media/gpuledger): the same findings contract —
  worst first, exit 0 by default — for domain checks and for GPU nodes.

## License

MIT.
