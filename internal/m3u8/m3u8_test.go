package m3u8

import (
	"os"
	"testing"
	"time"
)

func load(t *testing.T, name string) *Playlist {
	t.Helper()
	b, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(b)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return p
}

func TestMaster(t *testing.T) {
	p := load(t, "master.m3u8")
	if !p.Master || len(p.Variants) != 3 {
		t.Fatalf("master=%v variants=%d", p.Master, len(p.Variants))
	}
	v := p.Variants[0]
	if v.Bandwidth != 2500000 || v.Resolution.String() != "1280x720" || v.Codecs != "avc1.4d401f,mp4a.40.2" || v.FrameRate != 25 || v.URI != "720p/index.m3u8" {
		t.Errorf("variant 0 parsed wrong: %+v", v)
	}
	if v.Label() != "1280x720@2500k" || v.AudioOnly() {
		t.Errorf("label %q audio-only %v", v.Label(), v.AudioOnly())
	}
	a := Variant{Bandwidth: 75000, Codecs: "mp4a.40.2"}
	if a.Label() != "audio@75k" || !a.AudioOnly() {
		t.Errorf("audio rung: label %q audio-only %v", a.Label(), a.AudioOnly())
	}
	if len(p.Renditions) != 1 || p.Renditions[0].Type != "AUDIO" || p.Renditions[0].URI != "audio/it/index.m3u8" || !p.Renditions[0].Default {
		t.Errorf("renditions %+v", p.Renditions)
	}
	if !p.IndependentSegments || p.Version != 6 {
		t.Errorf("header: independent=%v version=%d", p.IndependentSegments, p.Version)
	}
}

func TestMediaLive(t *testing.T) {
	p := load(t, "media-live.m3u8")
	if p.Master || !p.Live() || p.TargetDuration != 6 || p.MediaSequence != 1040 || len(p.Segments) != 5 {
		t.Fatalf("live: master=%v live=%v target=%v seq=%d n=%d", p.Master, p.Live(), p.TargetDuration, p.MediaSequence, len(p.Segments))
	}
	if p.Segments[0].Sequence != 1040 || p.LastSequence() != 1044 {
		t.Errorf("sequences %d..%d", p.Segments[0].Sequence, p.LastSequence())
	}
	if p.Duration() < 29.9 || p.Duration() > 30.1 {
		t.Errorf("window %v", p.Duration())
	}
	if !p.Segments[2].Discontinuity || p.Discontinuities() != 1 || p.DiscontinuitySequence != 3 {
		t.Errorf("discontinuity: %+v", p.Segments[2])
	}
	want := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	if !p.Segments[0].ProgramDateTime.Equal(want) {
		t.Errorf("pdt %v", p.Segments[0].ProgramDateTime)
	}
	if p.Segments[4].ProgramDateTime.IsZero() {
		t.Errorf("last segment should carry its own PDT")
	}
	if p.Segments[0].Title != "live" {
		t.Errorf("title %q", p.Segments[0].Title)
	}
}

func TestMediaVOD(t *testing.T) {
	p := load(t, "media-vod.m3u8")
	if p.Live() || p.PlaylistType != "VOD" || !p.EndList || len(p.Segments) != 3 {
		t.Fatalf("vod: %+v", p)
	}
	if p.Segments[0].Map != "init.mp4" || p.Segments[1].ByteRange != "500000@700000" {
		t.Errorf("map/byterange: %+v %+v", p.Segments[0], p.Segments[1])
	}
	if !p.Segments[2].Gap {
		t.Errorf("gap flag lost")
	}
	if p.MaxSegmentDuration() != 6.006 {
		t.Errorf("max %v", p.MaxSegmentDuration())
	}
}

func TestLowLatency(t *testing.T) {
	p := load(t, "media-llhls.m3u8")
	if p.PartTargetDuration != 0.5 || p.PartCount != 4 || p.ServerControl["CAN-BLOCK-RELOAD"] != "YES" || p.ServerControl["PART-HOLD-BACK"] != "1.5" {
		t.Errorf("ll-hls: part-target=%v parts=%d server-control=%v", p.PartTargetDuration, p.PartCount, p.ServerControl)
	}
	if len(p.Segments) != 2 {
		t.Errorf("segments %d (parts must not count as segments)", len(p.Segments))
	}
}

func TestNotAPlaylist(t *testing.T) {
	for _, body := range []string{"", "<html><body>404</body></html>", "#EXTINF:6,\nseg.ts\n"} {
		if _, err := Parse([]byte(body)); err != ErrNotPlaylist {
			t.Errorf("%q: err=%v", body, err)
		}
	}
	if p, err := Parse([]byte("\xef\xbb\xbf#EXTM3U\r\n#EXT-X-TARGETDURATION:4\r\n")); err != nil || p.TargetDuration != 4 {
		t.Errorf("BOM and CRLF: %v %+v", err, p)
	}
}

func TestParseAttributes(t *testing.T) {
	a := ParseAttributes(`BANDWIDTH=2500000,CODECS="avc1.4d401f,mp4a.40.2",RESOLUTION=1280x720,NAME="720p, main",FRAME-RATE=25.000`)
	if a["CODECS"] != "avc1.4d401f,mp4a.40.2" || a["NAME"] != "720p, main" || a["RESOLUTION"] != "1280x720" || a["FRAME-RATE"] != "25.000" {
		t.Errorf("%v", a)
	}
	if a := ParseAttributes(`URI="init.mp4`); a["URI"] != "init.mp4" {
		t.Errorf("unterminated quote: %v", a)
	}
}

func TestResolve(t *testing.T) {
	cases := [][3]string{
		{"https://cdn.example/live/master.m3u8", "720p/index.m3u8", "https://cdn.example/live/720p/index.m3u8"},
		{"https://cdn.example/live/720p/index.m3u8?token=x", "seg1044.ts", "https://cdn.example/live/720p/seg1044.ts"},
		{"https://cdn.example/live/master.m3u8", "/abs/index.m3u8", "https://cdn.example/abs/index.m3u8"},
		{"https://cdn.example/live/master.m3u8", "https://other.example/i.m3u8", "https://other.example/i.m3u8"},
	}
	for _, c := range cases {
		got, err := Resolve(c[0], c[1])
		if err != nil || got != c[2] {
			t.Errorf("Resolve(%s, %s) = %s, %v; want %s", c[0], c[1], got, err, c[2])
		}
	}
}
