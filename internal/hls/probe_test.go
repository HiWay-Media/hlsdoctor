package hls

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hiway-media/hlsdoctor/internal/fetch"
)

var tsBytes = func() []byte {
	b := make([]byte, 188*3)
	for i := 0; i < len(b); i += 188 {
		b[i] = 0x47
	}
	return b
}()

// Fixture: a master with three variants. 720p is a healthy live stream that advances
// on the second fetch; 480p is stale (same playlist every time) and its segment is an
// HTML error page served as 200; 360p returns 404.
func fixture(t *testing.T) *httptest.Server {
	t.Helper()
	master, _ := os.ReadFile("../../testdata/master.m3u8")
	live, _ := os.ReadFile("../../testdata/media-live.m3u8")
	var n720 int32
	mux := http.NewServeMux()
	mux.HandleFunc("/live/master.m3u8", func(w http.ResponseWriter, _ *http.Request) { w.Write(master) })
	mux.HandleFunc("/live/720p/index.m3u8", func(w http.ResponseWriter, _ *http.Request) {
		body := string(live)
		if atomic.AddInt32(&n720, 1) > 1 {
			// The window slides: seg1040 leaves, seg1045 arrives.
			body = strings.Replace(body, "#EXT-X-MEDIA-SEQUENCE:1040", "#EXT-X-MEDIA-SEQUENCE:1041", 1)
			body = strings.Replace(body, "#EXTINF:6.000,live\nseg1040.ts\n", "", 1) + "#EXTINF:6.000,\nseg1045.ts\n"
		}
		w.Write([]byte(body))
	})
	mux.HandleFunc("/live/720p/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp2t")
		w.Write(tsBytes)
	})
	mux.HandleFunc("/live/480p/index.m3u8", func(w http.ResponseWriter, _ *http.Request) { w.Write(live) })
	mux.HandleFunc("/live/480p/", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("<html><body>origin error</body></html>"))
	})
	mux.HandleFunc("/live/360p/", func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) })
	mux.HandleFunc("/vod/index.m3u8", func(w http.ResponseWriter, _ *http.Request) {
		b, _ := os.ReadFile("../../testdata/media-vod.m3u8")
		w.Write(b)
	})
	mux.HandleFunc("/vod/", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("\x00\x00\x00\x18ftypiso5")) })
	mux.HandleFunc("/broken.m3u8", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("<html>not here</html>")) })
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func opts() Options {
	return Options{Segments: 1, Now: func() time.Time { return time.Date(2026, 9, 24, 9, 0, 45, 0, time.UTC) }, Sleep: func(context.Context, time.Duration) {}}
}

func TestMasterProbe(t *testing.T) {
	s := fixture(t)
	rep := Probe(context.Background(), fetch.New(5*time.Second, false), s.URL+"/live/master.m3u8?token=secret", opts())
	if rep.Error != "" || !rep.Master || len(rep.Variants) != 3 || rep.Renditions != 1 {
		t.Fatalf("%+v", rep)
	}
	if strings.Contains(rep.URL, "secret") {
		t.Errorf("URL leaked: %s", rep.URL)
	}
	v720 := rep.Variants[0].Playlist
	if !v720.Live || v720.TargetDuration != 6 || !v720.Refetched || !v720.Advanced || v720.SecondLastSequence != 1045 || v720.Wait != 6*time.Second {
		t.Errorf("720p: %+v", v720)
	}
	if v720.LastSequence != 1045 || v720.SegmentCount != 5 {
		t.Errorf("720p should be refilled from the second fetch: last=%d n=%d", v720.LastSequence, v720.SegmentCount)
	}
	if len(v720.Segments) != 1 || v720.Segments[0].Container != "ts" || v720.Segments[0].Bytes != 564 || v720.Segments[0].Sequence != 1045 {
		t.Errorf("720p segment: %+v", v720.Segments)
	}
	// Edge: last PDT 09:00:24 + 6 s (seg1044) + 6 s (seg1045) = 09:00:36; now 09:00:45 → 9 s behind.
	if v720.Behind < 8.9 || v720.Behind > 9.1 {
		t.Errorf("behind %v", v720.Behind)
	}
	v480 := rep.Variants[1].Playlist
	if !v480.Refetched || v480.Advanced || v480.LastSequence != 1044 {
		t.Errorf("480p should be stale: %+v", v480)
	}
	if len(v480.Segments) != 1 || v480.Segments[0].Container != "text" || v480.Segments[0].Status != 200 {
		t.Errorf("480p segment should be text: %+v", v480.Segments)
	}
	v360 := rep.Variants[2].Playlist
	if v360.Status != 404 || v360.Error != "HTTP 404" {
		t.Errorf("360p: %+v", v360)
	}
	if rep.Variants[0].Label != "1280x720@2500k" {
		t.Errorf("label %s", rep.Variants[0].Label)
	}
}

func TestVariantCapAndNoWait(t *testing.T) {
	s := fixture(t)
	o := opts()
	o.Variants = 1
	o.Wait = -1
	o.Segments = 0
	rep := Probe(context.Background(), fetch.New(5*time.Second, false), s.URL+"/live/master.m3u8", o)
	if len(rep.Variants) != 1 || rep.Variants[0].Playlist.Refetched || len(rep.Variants[0].Playlist.Segments) != 0 {
		t.Errorf("%+v", rep.Variants)
	}
}

func TestMediaDirectVOD(t *testing.T) {
	s := fixture(t)
	rep := Probe(context.Background(), fetch.New(5*time.Second, false), s.URL+"/vod/index.m3u8", opts())
	if rep.Master || rep.Playlist == nil {
		t.Fatalf("%+v", rep)
	}
	p := rep.Playlist
	if p.Live || p.PlaylistType != "VOD" || p.Refetched || p.Gaps != 1 || !p.HasMap || p.Behind != -1 {
		t.Errorf("%+v", p)
	}
	// The last segment is a GAP and is skipped; with Segments=1 nothing is downloaded.
	if len(p.Segments) != 0 {
		t.Errorf("gap segment downloaded: %+v", p.Segments)
	}
	o := opts()
	o.Segments = 2
	p = Probe(context.Background(), fetch.New(5*time.Second, false), s.URL+"/vod/index.m3u8", o).Playlist
	if len(p.Segments) != 1 || p.Segments[0].Container != "fmp4" {
		t.Errorf("fmp4: %+v", p.Segments)
	}
}

func TestErrors(t *testing.T) {
	s := fixture(t)
	f := fetch.New(2*time.Second, false)
	rep := Probe(context.Background(), f, s.URL+"/broken.m3u8", opts())
	if !strings.Contains(rep.Error, "#EXTM3U missing") || !strings.Contains(rep.Error, "HTTP 200") {
		t.Errorf("broken: %q", rep.Error)
	}
	rep = Probe(context.Background(), f, s.URL+"/live/360p/index.m3u8", opts())
	if rep.Status != 404 || rep.Error != "HTTP 404" {
		t.Errorf("404: %+v", rep)
	}
	rep = Probe(context.Background(), f, "http://127.0.0.1:1/x.m3u8", opts())
	if rep.Error == "" {
		t.Errorf("refused should be an error")
	}
}

func TestContainer(t *testing.T) {
	cases := map[string]string{
		string(tsBytes):                    "ts",
		"\x00\x00\x00\x18ftypiso5":         "fmp4",
		"\x00\x00\x00\x08styp":             "fmp4",
		"\x00\x00\x01\x00moof":             "fmp4",
		"ID3\x04\x00":                      "aac",
		"#EXTM3U\n":                        "text",
		"  <html>":                         "text",
		"{\"error\":1}":                    "text",
		"":                                 "empty",
		"\x00\x01\x02\x03\x04\x05\x06\x07": "unknown",
	}
	for in, want := range cases {
		if got := Container([]byte(in)); got != want {
			t.Errorf("Container(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestLatencyFromPDTWithoutTrailingTag(t *testing.T) {
	// A playlist where only the first segment carries PDT: the edge is derived by
	// adding the durations that follow.
	body := "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:1\n#EXT-X-PROGRAM-DATE-TIME:2026-09-24T09:00:00Z\n#EXTINF:4,\na.ts\n#EXTINF:4,\nb.ts\n#EXTINF:4,\nc.ts\n"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, body) }))
	defer s.Close()
	o := opts()
	o.Wait = -1
	o.Segments = 0
	o.Now = func() time.Time { return time.Date(2026, 9, 24, 9, 0, 20, 0, time.UTC) }
	p := Probe(context.Background(), fetch.New(time.Second, false), s.URL+"/i.m3u8", o).Playlist
	if p.Behind < 7.9 || p.Behind > 8.1 { // edge 09:00:12, now 09:00:20
		t.Errorf("behind %v (edge %v)", p.Behind, p.LastPDT)
	}
}
