package findings

import (
	"strings"
	"testing"
	"time"

	"github.com/hiway-media/hlsdoctor/internal/hls"
	"github.com/hiway-media/hlsdoctor/internal/m3u8"
	"github.com/hiway-media/hlsdoctor/internal/rtmp"
)

func codes(fs []Finding) string {
	out := []string{}
	for _, f := range fs {
		out = append(out, string(f.Level)+":"+f.Code)
	}
	return strings.Join(out, " ")
}

func healthy() hls.PlaylistReport {
	return hls.PlaylistReport{Status: 200, Live: true, TargetDuration: 6, SegmentCount: 5, Window: 30, MaxSegmentDuration: 6, LastSequence: 1044, Behind: 8, Wait: 6 * time.Second, Refetched: true, Advanced: true,
		Segments: []hls.SegmentReport{{Sequence: 1044, Duration: 6, Status: 200, Bytes: 900000, Latency: 400 * time.Millisecond, Container: "ts"}}}
}

// variant gives every label its own rung, so only an explicit copy is a duplicate.
func variant(label string, pl hls.PlaylistReport) hls.VariantReport {
	bw, h := int64(2500000), 720
	if label != "720p" {
		bw, h = 1200000, 480
	}
	return hls.VariantReport{Variant: m3u8.Variant{Bandwidth: bw, Resolution: m3u8.Resolution{Width: h * 16 / 9, Height: h}, Codecs: "avc1"}, Label: label, Playlist: pl}
}

func TestHealthyMaster(t *testing.T) {
	r := hls.Report{URL: "u", Master: true, Variants: []hls.VariantReport{variant("720p", healthy()), variant("480p", healthy())}}
	fs := Evaluate(r, Default)
	if codes(fs) != "OK:playable OK:playable" || Worst(fs) != OK {
		t.Errorf("%s", codes(fs))
	}
	p := Default
	p.OKIsFinding = false
	if fs := Evaluate(r, p); len(fs) != 0 {
		t.Errorf("--no-ok still reports: %s", codes(fs))
	}
}

func TestEachCode(t *testing.T) {
	mk := func(mut func(*hls.PlaylistReport)) hls.Report {
		pl := healthy()
		mut(&pl)
		return hls.Report{URL: "u", Master: true, Variants: []hls.VariantReport{variant("720p", pl)}}
	}
	cases := []struct {
		name string
		rep  hls.Report
		want string
	}{
		{"stale", mk(func(p *hls.PlaylistReport) { p.Advanced = false }), "BAD:stale"},
		{"stale second fetch", mk(func(p *hls.PlaylistReport) { p.Error = "second fetch: HTTP 503" }), "BAD:stale"},
		{"behind", mk(func(p *hls.PlaylistReport) { p.Behind = 40 }), "WARN:behind-live"},
		{"short window", mk(func(p *hls.PlaylistReport) { p.Window = 12; p.SegmentCount = 2 }), "WARN:short-window"},
		{"long segment", mk(func(p *hls.PlaylistReport) { p.MaxSegmentDuration = 7.2 }), "WARN:long-segment"},
		{"gaps", mk(func(p *hls.PlaylistReport) { p.Gaps = 1 }), "WARN:gaps"},
		{"segment 404", mk(func(p *hls.PlaylistReport) { p.Segments[0].Error = "HTTP 404"; p.Segments[0].Status = 404 }), "BAD:segment-unreachable"},
		{"segment text", mk(func(p *hls.PlaylistReport) { p.Segments[0].Container = "text" }), "BAD:segment-corrupt"},
		{"segment empty", mk(func(p *hls.PlaylistReport) { p.Segments[0].Container = "empty" }), "BAD:segment-corrupt"},
		{"segment unknown", mk(func(p *hls.PlaylistReport) { p.Segments[0].Container = "unknown" }), "BAD:segment-corrupt"},
		{"segment slow warn", mk(func(p *hls.PlaylistReport) { p.Segments[0].Latency = 4 * time.Second }), "WARN:segment-slow"},
		{"segment slow bad", mk(func(p *hls.PlaylistReport) { p.Segments[0].Latency = 7 * time.Second }), "BAD:segment-slow"},
		{"discontinuity is ok", mk(func(p *hls.PlaylistReport) { p.Discontinuities = 1 }), "OK:discontinuity OK:playable"},
		{"empty", mk(func(p *hls.PlaylistReport) { p.SegmentCount = 0 }), "BAD:empty-playlist"},
		{"variant 404", mk(func(p *hls.PlaylistReport) { p.Status = 404; p.Error = "HTTP 404" }), "BAD:http-status"},
		{"variant html", mk(func(p *hls.PlaylistReport) {
			p.Error = "not an M3U8 playlist: #EXTM3U missing (HTTP 200, text/html, 300 bytes)"
		}), "BAD:not-a-playlist"},
		{"variant refused", mk(func(p *hls.PlaylistReport) { p.Error = "dial tcp: connection refused" }), "BAD:unreachable"},
		{"vod", mk(func(p *hls.PlaylistReport) {
			p.Live = false
			p.Advanced = false
			p.Refetched = false
			p.Behind = -1
			p.Window = 12
		}), "OK:playable"},
		{"master 503", hls.Report{URL: "u", Status: 503, Error: "HTTP 503"}, "BAD:http-status"},
		{"master html", hls.Report{URL: "u", Status: 200, Error: "not an M3U8 playlist: #EXTM3U missing"}, "BAD:not-a-playlist"},
		{"master refused", hls.Report{URL: "u", Error: "dial: refused"}, "BAD:unreachable"},
		{"master no variants", hls.Report{URL: "u", Master: true}, "BAD:empty-playlist"},
		{"media direct", func() hls.Report { p := healthy(); return hls.Report{URL: "u", Playlist: &p} }(), "OK:playable"},
	}
	for _, c := range cases {
		if got := codes(Evaluate(c.rep, Default)); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestLadder(t *testing.T) {
	a := variant("720p", healthy())
	b := variant("480p", healthy())
	b.Playlist.LastSequence = 1040
	c := hls.VariantReport{Label: "variant", Playlist: healthy()} // no bandwidth, resolution, codecs
	d := variant("720p", healthy())
	d.Label = "720p-dup"
	r := hls.Report{URL: "u", Master: true, Variants: []hls.VariantReport{a, b, c, d}}
	got := codes(Evaluate(r, Default))
	for _, want := range []string{"WARN:ladder-skew", "WARN:ladder-incomplete"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if strings.Count(got, "ladder-incomplete") != 2 {
		t.Errorf("want one incomplete and one duplicate: %s", got)
	}
	fs := Evaluate(r, Default)
	if fs[0].Level != WARN || fs[len(fs)-1].Level != OK {
		t.Errorf("not sorted worst first: %s", got)
	}
}

func TestRTMP(t *testing.T) {
	cases := []struct {
		r    rtmp.Result
		want string
	}{
		{rtmp.Result{Stage: "done", Echo: true, Handshake: 30 * time.Millisecond}, "OK:rtmp-ok"},
		{rtmp.Result{Stage: "done", Echo: false}, "WARN:rtmp-echo"},
		{rtmp.Result{Stage: "s0", Error: "server answered RTMP version 6, not 3"}, "BAD:rtmp-handshake"},
		{rtmp.Result{Stage: "dial", Error: "dial: connection refused"}, "BAD:rtmp-unreachable"},
	}
	for _, c := range cases {
		if got := codes(EvaluateRTMP(c.r, Default)); got != c.want {
			t.Errorf("%+v: %s", c.r, got)
		}
	}
	p := Default
	p.OKIsFinding = false
	if fs := EvaluateRTMP(rtmp.Result{Stage: "done", Echo: true}, p); len(fs) != 0 {
		t.Errorf("--no-ok: %s", codes(fs))
	}
}

func TestNodes(t *testing.T) {
	ok := hls.Report{URL: "u", Node: "10.0.0.1", Master: true, Variants: []hls.VariantReport{variant("720p", healthy())}}
	lag := ok
	lag.Node = "10.0.0.2"
	v := variant("720p", healthy())
	v.Playlist.LastSequence = 1030
	lag.Variants = []hls.VariantReport{v}
	down := hls.Report{URL: "u", Node: "10.0.0.3", Error: "dial: timeout"}
	got := codes(Nodes("u", []hls.Report{ok, lag, down}, Default))
	if got != "BAD:nodes-disagree WARN:nodes-skew" {
		t.Errorf("%s", got)
	}
	if fs := Nodes("u", []hls.Report{ok}, Default); fs != nil {
		t.Errorf("one node: %s", codes(fs))
	}
}

func TestExitCode(t *testing.T) {
	fs := []Finding{{Level: OK}, {Level: WARN}, {Level: BAD}}
	if ExitCode(fs, "") != 0 || ExitCode(fs, "warn") != 2 || ExitCode(fs, "bad") != 2 || ExitCode(fs, "error") != 0 {
		t.Errorf("exit codes wrong")
	}
	if ExitCode([]Finding{{Level: WARN}}, "bad") != 0 || ExitCode([]Finding{{Level: WARN}}, "warn") != 1 || ExitCode([]Finding{Invalid("u", "x")}, "error") != 3 {
		t.Errorf("exit codes wrong at the edges")
	}
}
