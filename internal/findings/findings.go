// Package findings turns probe reports into verdicts. Worst first; exit code 0 unless
// asked otherwise, because a check that ran is a success — the same contract as
// checkfleet and gpuledger.
package findings

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hiway-media/hlsdoctor/internal/hls"
	"github.com/hiway-media/hlsdoctor/internal/rtmp"
)

type Level string

const (
	OK    Level = "OK"
	WARN  Level = "WARN"
	BAD   Level = "BAD"
	ERROR Level = "ERROR"
)

var rank = map[Level]int{OK: 0, WARN: 1, BAD: 2, ERROR: 3}

// Finding is one verdict about one stream, variant or segment.
type Finding struct {
	Level   Level  `json:"level"`
	Code    string `json:"code"`
	URL     string `json:"url"`               // redacted, the target as given
	Node    string `json:"node,omitempty"`    // the IP when probed per node
	Variant string `json:"variant,omitempty"` // "1280x720@2500k", "media", "rtmp"
	Message string `json:"message"`
}

// Policy holds the thresholds; every one has a defensible default and a flag.
type Policy struct {
	// MaxBehind is how far (seconds) the live edge may lag wall-clock before it is a
	// WARN; 0 means three target durations — the delay a compliant player sits at.
	MaxBehind float64
	// MinWindow is the shortest live window (seconds) before a WARN; 0 means three
	// target durations, the minimum RFC 8216 asks a server to keep.
	MinWindow float64
	// SlowFactor: a segment whose download took longer than SlowFactor × its duration
	// is WARN; longer than its duration is BAD (the player cannot keep up).
	SlowFactor float64
	// SkewSegments: two variants (or two nodes) whose newest sequence numbers differ by
	// more than this are out of step — WARN.
	SkewSegments int64
	// OKIsFinding reports the streams and variants that passed (informational).
	OKIsFinding bool
}

// Default is what ships.
var Default = Policy{MaxBehind: 0, MinWindow: 0, SlowFactor: 0.5, SkewSegments: 2, OKIsFinding: true}

// Evaluate returns the findings for one HLS report, worst first. Codes:
//
//	unreachable          the playlist could not be fetched at all (BAD)
//	http-status          a playlist answered with a non-2xx status (BAD)
//	not-a-playlist       the body is not M3U8 — an HTML error page behind a 200 (BAD)
//	empty-playlist       a media playlist with no segments (BAD)
//	stale                a live playlist did not advance within the wait (BAD)
//	segment-unreachable  the newest segment could not be fetched, or non-2xx (BAD)
//	segment-corrupt      the segment is empty, text, or has no TS sync byte / MP4 box (BAD)
//	segment-slow         the segment took longer than its duration to download (BAD), or
//	                     longer than SlowFactor × its duration (WARN)
//	behind-live          the live edge is more than MaxBehind seconds behind wall-clock (WARN)
//	short-window         a live playlist shorter than MinWindow (WARN)
//	long-segment         an EXTINF exceeds EXT-X-TARGETDURATION (WARN, breaks players)
//	ladder-skew          variants' newest sequence numbers differ by more than SkewSegments (WARN)
//	ladder-incomplete    a variant without BANDWIDTH, RESOLUTION or CODECS; duplicate rungs (WARN)
//	gaps                 EXT-X-GAP segments in the window (WARN)
//	discontinuity        EXT-X-DISCONTINUITY in the window — an encoder restart (OK, informational)
//	playable             the variant passed every check (OK)
func Evaluate(r hls.Report, p Policy) []Finding {
	var out []Finding
	add := func(l Level, code, variant, msg string) {
		out = append(out, Finding{Level: l, Code: code, URL: r.URL, Node: r.Node, Variant: variant, Message: msg})
	}
	if r.Error != "" {
		switch {
		case r.Status/100 != 2 && r.Status != 0:
			add(BAD, "http-status", "", fmt.Sprintf("HTTP %d for the playlist (%s)", r.Status, ms(r.Latency)))
		case strings.Contains(r.Error, "#EXTM3U missing"):
			add(BAD, "not-a-playlist", "", r.Error)
		default:
			add(BAD, "unreachable", "", r.Error)
		}
		return sorted(out)
	}
	if r.Master {
		if len(r.Variants) == 0 {
			add(BAD, "empty-playlist", "", "the master playlist lists no variant")
			return sorted(out)
		}
		out = append(out, ladder(r, p)...)
		for _, v := range r.Variants {
			out = append(out, playlist(r, v.Label, v.Playlist, p)...)
		}
	} else if r.Playlist != nil {
		out = append(out, playlist(r, "media", *r.Playlist, p)...)
	}
	return sorted(out)
}

func ladder(r hls.Report, p Policy) []Finding {
	var out []Finding
	add := func(l Level, code, variant, msg string) {
		out = append(out, Finding{Level: l, Code: code, URL: r.URL, Node: r.Node, Variant: variant, Message: msg})
	}
	seen := map[string]string{}
	for _, v := range r.Variants {
		missing := []string{}
		if v.Bandwidth == 0 {
			missing = append(missing, "BANDWIDTH")
		}
		if v.Resolution.Width == 0 && !v.AudioOnly() {
			missing = append(missing, "RESOLUTION")
		}
		if v.Codecs == "" {
			missing = append(missing, "CODECS")
		}
		if len(missing) > 0 {
			add(WARN, "ladder-incomplete", v.Label, "EXT-X-STREAM-INF without "+strings.Join(missing, ", "))
		}
		key := v.Resolution.String() + "/" + fmt.Sprint(v.Bandwidth)
		if prev, ok := seen[key]; ok && v.Bandwidth > 0 {
			add(WARN, "ladder-incomplete", v.Label, "same resolution and bandwidth as "+prev+" — a duplicate rung")
		}
		seen[key] = v.Label
	}
	// Skew: compare the newest sequence numbers of the live variants that answered.
	var lo, hi int64 = -1, -1
	var loL, hiL string
	for _, v := range r.Variants {
		pl := v.Playlist
		if pl.Error != "" || !pl.Live || pl.LastSequence < 0 {
			continue
		}
		if lo < 0 || pl.LastSequence < lo {
			lo, loL = pl.LastSequence, v.Label
		}
		if hi < 0 || pl.LastSequence > hi {
			hi, hiL = pl.LastSequence, v.Label
		}
	}
	if hi >= 0 && hi-lo > p.SkewSegments {
		add(WARN, "ladder-skew", "", fmt.Sprintf("%s is at sequence %d while %s is at %d — %d segments apart, players switching rungs will stall", hiL, hi, loL, lo, hi-lo))
	}
	return out
}

func playlist(r hls.Report, label string, pl hls.PlaylistReport, p Policy) []Finding {
	var out []Finding
	add := func(l Level, code, msg string) {
		out = append(out, Finding{Level: l, Code: code, URL: r.URL, Node: r.Node, Variant: label, Message: msg})
	}
	if pl.Error != "" && !strings.HasPrefix(pl.Error, "second fetch") {
		switch {
		case pl.Status/100 != 2 && pl.Status != 0:
			add(BAD, "http-status", fmt.Sprintf("HTTP %d for the media playlist (%s)", pl.Status, ms(pl.Latency)))
		case strings.Contains(pl.Error, "#EXTM3U missing"):
			add(BAD, "not-a-playlist", pl.Error)
		default:
			add(BAD, "unreachable", pl.Error)
		}
		return out
	}
	if pl.SegmentCount == 0 {
		add(BAD, "empty-playlist", "the media playlist has no segments")
		return out
	}
	healthy := true
	target := pl.TargetDuration
	if pl.Live {
		if strings.HasPrefix(pl.Error, "second fetch") {
			healthy = false
			add(BAD, "stale", pl.Error+" — the playlist could not be re-read after "+pl.Wait.String())
		} else if pl.Refetched && !pl.Advanced {
			healthy = false
			add(BAD, "stale", fmt.Sprintf("newest segment still %d after %s (target duration %gs) — the packager stopped writing", pl.LastSequence, pl.Wait, target))
		}
		maxBehind := p.MaxBehind
		if maxBehind <= 0 {
			maxBehind = 3 * target
		}
		if pl.Behind >= 0 && pl.Behind > maxBehind {
			healthy = false
			add(WARN, "behind-live", fmt.Sprintf("live edge %.0fs behind wall-clock (limit %.0fs) by EXT-X-PROGRAM-DATE-TIME", pl.Behind, maxBehind))
		}
		minWindow := p.MinWindow
		if minWindow <= 0 {
			minWindow = 3 * target
		}
		if pl.Window < minWindow {
			healthy = false
			add(WARN, "short-window", fmt.Sprintf("%.0fs of segments in the window (%d), RFC 8216 wants at least %.0fs", pl.Window, pl.SegmentCount, minWindow))
		}
	}
	if target > 0 && pl.MaxSegmentDuration > target+0.5 {
		healthy = false
		add(WARN, "long-segment", fmt.Sprintf("a segment of %.3fs in a playlist whose EXT-X-TARGETDURATION is %g — players may refuse it", pl.MaxSegmentDuration, target))
	}
	if pl.Gaps > 0 {
		healthy = false
		add(WARN, "gaps", fmt.Sprintf("%d EXT-X-GAP segment(s) in the window — missing media", pl.Gaps))
	}
	for _, s := range pl.Segments {
		switch {
		case s.Error != "":
			healthy = false
			add(BAD, "segment-unreachable", fmt.Sprintf("segment %d: %s", s.Sequence, s.Error))
		case s.Container == "empty":
			healthy = false
			add(BAD, "segment-corrupt", fmt.Sprintf("segment %d is 0 bytes", s.Sequence))
		case s.Container == "text":
			healthy = false
			add(BAD, "segment-corrupt", fmt.Sprintf("segment %d is text, not media — an error page served as HTTP %d (%d bytes)", s.Sequence, s.Status, s.Bytes))
		case s.Container == "unknown":
			healthy = false
			add(BAD, "segment-corrupt", fmt.Sprintf("segment %d: no MPEG-TS sync byte and no MP4 box in the first bytes (%d bytes)", s.Sequence, s.Bytes))
		default:
			if s.Duration > 0 {
				ratio := s.Latency.Seconds() / s.Duration
				switch {
				case ratio > 1:
					healthy = false
					add(BAD, "segment-slow", fmt.Sprintf("segment %d (%s, %d bytes) took %s to download for %.1fs of media — playback cannot keep up", s.Sequence, s.Container, s.Bytes, ms(s.Latency), s.Duration))
				case p.SlowFactor > 0 && ratio > p.SlowFactor:
					healthy = false
					add(WARN, "segment-slow", fmt.Sprintf("segment %d (%s, %d bytes) took %s for %.1fs of media (%.0f%% of real time)", s.Sequence, s.Container, s.Bytes, ms(s.Latency), s.Duration, ratio*100))
				}
			}
		}
	}
	if p.OKIsFinding && pl.Discontinuities > 0 {
		add(OK, "discontinuity", fmt.Sprintf("%d EXT-X-DISCONTINUITY in the window — an encoder restart or an ad splice", pl.Discontinuities))
	}
	if healthy && p.OKIsFinding {
		kind := "VOD"
		if pl.Live {
			kind = "live"
		}
		seg := ""
		if len(pl.Segments) > 0 {
			s := pl.Segments[len(pl.Segments)-1]
			seg = fmt.Sprintf(", segment %d %s %d bytes in %s", s.Sequence, s.Container, s.Bytes, ms(s.Latency))
		}
		adv := ""
		if pl.Live && pl.Advanced {
			adv = fmt.Sprintf(", advanced to %d after %s", pl.LastSequence, pl.Wait)
		}
		behind := ""
		if pl.Behind >= 0 {
			behind = fmt.Sprintf(", %.0fs behind live", pl.Behind)
		}
		add(OK, "playable", fmt.Sprintf("%s, %d segments / %.0fs window, target %gs%s%s%s", kind, pl.SegmentCount, pl.Window, target, adv, behind, seg))
	}
	return out
}

// EvaluateRTMP maps a handshake result. Codes: rtmp-unreachable (BAD), rtmp-handshake
// (BAD: wrong version, closed early, timeout), rtmp-echo (WARN: S2 did not echo C1 —
// the server is not a compliant RTMP endpoint, most players tolerate it), rtmp-ok (OK).
func EvaluateRTMP(r rtmp.Result, p Policy) []Finding {
	f := Finding{URL: r.URL, Variant: "rtmp"}
	switch {
	case r.Stage == "dial" && r.Error != "":
		f.Level, f.Code, f.Message = BAD, "rtmp-unreachable", r.Error
	case r.Error != "":
		f.Level, f.Code, f.Message = BAD, "rtmp-handshake", fmt.Sprintf("%s (stage %s, %s)", r.Error, r.Stage, ms(r.Connect))
	case !r.Echo:
		f.Level, f.Code, f.Message = WARN, "rtmp-echo", fmt.Sprintf("handshake completed in %s but S2 did not echo C1", ms(r.Handshake))
	default:
		if !p.OKIsFinding {
			return nil
		}
		f.Level, f.Code, f.Message = OK, "rtmp-ok", fmt.Sprintf("RTMP 3 handshake in %s (connect %s) on %s", ms(r.Handshake), ms(r.Connect), r.Addr)
	}
	return []Finding{f}
}

// Nodes compares the per-IP reports of one URL. A node that failed while another
// answered is BAD nodes-disagree; nodes whose newest sequence numbers for the same
// variant differ by more than SkewSegments are WARN nodes-skew — normal for
// independent origins, worth knowing behind one DNS name.
func Nodes(url string, reports []hls.Report, p Policy) []Finding {
	if len(reports) < 2 {
		return nil
	}
	var out []Finding
	ok, failed := []string{}, []string{}
	for _, r := range reports {
		if r.Error != "" {
			failed = append(failed, r.Node)
		} else {
			ok = append(ok, r.Node)
		}
	}
	if len(failed) > 0 && len(ok) > 0 {
		out = append(out, Finding{Level: BAD, Code: "nodes-disagree", URL: url, Message: fmt.Sprintf("%d of %d addresses failed: %s (answering: %s)", len(failed), len(reports), strings.Join(failed, ", "), strings.Join(ok, ", "))})
	}
	seqs := map[string]map[string]int64{} // variant → node → last sequence
	for _, r := range reports {
		if r.Error != "" {
			continue
		}
		if r.Playlist != nil {
			if seqs["media"] == nil {
				seqs["media"] = map[string]int64{}
			}
			seqs["media"][r.Node] = r.Playlist.LastSequence
		}
		for _, v := range r.Variants {
			if v.Playlist.Error != "" || !v.Playlist.Live {
				continue
			}
			if seqs[v.Label] == nil {
				seqs[v.Label] = map[string]int64{}
			}
			seqs[v.Label][r.Node] = v.Playlist.LastSequence
		}
	}
	labels := make([]string, 0, len(seqs))
	for l := range seqs {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	for _, l := range labels {
		var lo, hi int64 = -1, -1
		var loN, hiN string
		for n, s := range seqs[l] {
			if lo < 0 || s < lo {
				lo, loN = s, n
			}
			if hi < 0 || s > hi {
				hi, hiN = s, n
			}
		}
		if hi >= 0 && hi-lo > p.SkewSegments {
			out = append(out, Finding{Level: WARN, Code: "nodes-skew", URL: url, Variant: l, Message: fmt.Sprintf("%s is at sequence %d, %s at %d — %d segments apart behind one name", hiN, hi, loN, lo, hi-lo)})
		}
	}
	return out
}

// Invalid is the ERROR finding for a target the probe could not even attempt.
func Invalid(url, msg string) Finding {
	return Finding{Level: ERROR, Code: "invalid-target", URL: url, Message: msg}
}

// Sort orders findings worst first, keeping the input order within a level.
func Sort(fs []Finding) []Finding { return sorted(fs) }

func sorted(fs []Finding) []Finding {
	sort.SliceStable(fs, func(i, j int) bool { return rank[fs[i].Level] > rank[fs[j].Level] })
	return fs
}

// Worst returns the highest level present, OK for none.
func Worst(fs []Finding) Level {
	w := OK
	for _, f := range fs {
		if rank[f.Level] > rank[w] {
			w = f.Level
		}
	}
	return w
}

// ExitCode maps the worst level to a process exit code under an --exit-on policy:
// "" → always 0; "warn" → 1 at WARN, 2 at BAD, 3 at ERROR; "bad" → 0 below BAD; "error".
func ExitCode(fs []Finding, exitOn string) int {
	w := Worst(fs)
	code := map[Level]int{OK: 0, WARN: 1, BAD: 2, ERROR: 3}[w]
	switch exitOn {
	case "warn":
		return code
	case "bad":
		if w == WARN {
			return 0
		}
		return code
	case "error":
		if w == ERROR {
			return 3
		}
		return 0
	}
	return 0
}

func ms(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
