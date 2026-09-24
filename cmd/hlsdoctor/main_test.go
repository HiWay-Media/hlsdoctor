package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

var tsBytes = func() []byte {
	b := make([]byte, 188*4)
	for i := 0; i < len(b); i += 188 {
		b[i] = 0x47
	}
	return b
}()

// The same shape as the hls package fixture: 720p healthy and advancing, 480p stale
// with an HTML page where its segment should be, 360p gone.
func origin(t *testing.T) *httptest.Server {
	t.Helper()
	master, _ := os.ReadFile("../../testdata/master.m3u8")
	live, _ := os.ReadFile("../../testdata/media-live.m3u8")
	var n int32
	mux := http.NewServeMux()
	mux.HandleFunc("/live/master.m3u8", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Token") == "" {
			http.Error(w, "forbidden", 403)
			return
		}
		w.Write(master)
	})
	// 720p behaves like a live packager: every fetch shows the window one segment on.
	mux.HandleFunc("/live/720p/index.m3u8", func(w http.ResponseWriter, _ *http.Request) {
		first := 1040 + int(atomic.AddInt32(&n, 1)) - 1
		var b strings.Builder
		fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:%d\n", first)
		for i := 0; i < 5; i++ {
			fmt.Fprintf(&b, "#EXTINF:6.000,\nseg%d.ts\n", first+i)
		}
		w.Write([]byte(b.String()))
	})
	mux.HandleFunc("/live/720p/", func(w http.ResponseWriter, _ *http.Request) { w.Write(tsBytes) })
	mux.HandleFunc("/live/480p/index.m3u8", func(w http.ResponseWriter, _ *http.Request) { w.Write(live) })
	mux.HandleFunc("/live/480p/", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("<html>error</html>")) })
	mux.HandleFunc("/live/360p/", func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) })
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func rtmpServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				c := make([]byte, 1537)
				if _, err := io.ReadFull(conn, c); err != nil {
					return
				}
				s1 := make([]byte, 1536)
				binary.BigEndian.PutUint32(s1, 1)
				conn.Write([]byte{3})
				conn.Write(s1)
				conn.Write(c[1:])
				io.ReadFull(conn, make([]byte, 1536))
			}()
		}
	}()
	return ln.Addr().String()
}

func TestCheckText(t *testing.T) {
	s := origin(t)
	var out, errb bytes.Buffer
	code := run([]string{"check", s.URL + "/live/master.m3u8?token=secret", "--wait", "1ms", "--header", "X-Token=secret", "--exit-on", "bad"}, &out, &errb)
	text := out.String()
	if code != 2 {
		t.Errorf("exit %d, want 2 (BAD present)\n%s", code, text)
	}
	for _, want := range []string{"🔴 BAD   stale", "854x480@1200k", "🔴 BAD   segment-corrupt", "🔴 BAD   http-status", "640x360@600k", "🟢 OK    playable", "1280x720@2500k", "advanced to 1045"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in output:\n%s", want, text)
		}
	}
	if strings.Contains(text, "secret") || strings.Contains(errb.String(), "secret") {
		t.Errorf("the token leaked:\n%s%s", text, errb.String())
	}
	if !strings.Contains(text, "/live/master.m3u8?…") {
		t.Errorf("the URL should be printed with its query redacted:\n%s", text)
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if !strings.HasPrefix(lines[0], "🔴") {
		t.Errorf("worst first: %s", lines[0])
	}
}

func TestCheckJSONAndExitPolicies(t *testing.T) {
	s := origin(t)
	var out bytes.Buffer
	code := run([]string{"check", s.URL + "/live/master.m3u8", "--wait", "1ms", "--header", "X-Token=1", "--json"}, &out, io.Discard)
	if code != 0 {
		t.Errorf("default exit must be 0, got %d", code)
	}
	var doc struct {
		Worst    string `json:"worst"`
		Findings []struct {
			Level, Code, Variant string
		} `json:"findings"`
		Targets []struct {
			Kind string `json:"kind"`
			HLS  []struct {
				Master   bool `json:"master"`
				Variants []struct {
					Label    string `json:"label"`
					Playlist struct {
						Advanced bool `json:"advanced"`
					} `json:"playlist"`
				} `json:"variants"`
			} `json:"hls"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if doc.Worst != "BAD" || len(doc.Targets) != 1 || doc.Targets[0].Kind != "hls" || !doc.Targets[0].HLS[0].Master || len(doc.Targets[0].HLS[0].Variants) != 3 {
		t.Errorf("%+v", doc)
	}
	if !doc.Targets[0].HLS[0].Variants[0].Playlist.Advanced || doc.Targets[0].HLS[0].Variants[1].Playlist.Advanced {
		t.Errorf("advanced flags wrong: %+v", doc.Targets[0].HLS[0].Variants)
	}
	if strings.Contains(out.String(), "X-Token") {
		t.Errorf("headers must never be in the output")
	}
	if code := run([]string{"check", s.URL + "/live/master.m3u8", "--wait", "1ms", "--header", "X-Token=1", "--exit-on", "warn", "--variants", "1", "--no-ok"}, io.Discard, io.Discard); code != 0 {
		t.Errorf("only the healthy variant, --no-ok: want exit 0, got %d", code)
	}
	if code := run([]string{"check", s.URL + "/live/master.m3u8", "--exit-on", "bad"}, io.Discard, io.Discard); code != 2 {
		t.Errorf("403 on the master should be BAD → 2, got %d", code)
	}
}

func TestLsAndRTMPAndFrom(t *testing.T) {
	s := origin(t)
	addr := rtmpServer(t)
	dir := t.TempDir()
	list := filepath.Join(dir, "streams.txt")
	os.WriteFile(list, []byte("# the farm\n"+s.URL+"/live/720p/index.m3u8\n\nrtmp://"+addr+"/live/key?auth=secret\nftp://nope/x\n"), 0o600)
	var out bytes.Buffer
	code := run([]string{"ls", "--from", list, "--wait", "-1ms", "--exit-on", "error"}, &out, io.Discard)
	text := out.String()
	if code != 3 {
		t.Errorf("an invalid target under --exit-on error is 3, got %d\n%s", code, text)
	}
	for _, want := range []string{"│ media", "1044", "handshake ok", "unsupported scheme ftp", "rtmp://" + addr + "/live/key?…"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "secret") {
		t.Errorf("leak:\n%s", text)
	}
	out.Reset()
	if code := run([]string{"check", "rtmp://" + addr + "/live"}, &out, io.Discard); code != 0 || !strings.Contains(out.String(), "rtmp-ok") {
		t.Errorf("rtmp check: %d\n%s", code, out.String())
	}
	out.Reset()
	if code := run([]string{"check", "rtmp://127.0.0.1:1/live", "--exit-on", "bad"}, &out, io.Discard); code != 2 || !strings.Contains(out.String(), "rtmp-unreachable") {
		t.Errorf("rtmp refused: %d\n%s", code, out.String())
	}
}

func TestEachIP(t *testing.T) {
	s := origin(t)
	var out bytes.Buffer
	// 127.0.0.1 resolves to itself: one node, no comparison, but the pinned path runs.
	code := run([]string{"check", s.URL + "/live/720p/index.m3u8", "--each-ip", "--wait", "-1ms"}, &out, io.Discard)
	if code != 0 || !strings.Contains(out.String(), "@127.0.0.1") || !strings.Contains(out.String(), "playable") {
		t.Errorf("each-ip: %d\n%s", code, out.String())
	}
}

func TestUsageAndErrors(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "hlsdoctor ") {
		t.Errorf("version: %d %q", code, out.String())
	}
	if code := run([]string{"bogus"}, io.Discard, &errb); code != 2 {
		t.Errorf("unknown command: %d", code)
	}
	if code := run([]string{"check"}, io.Discard, &errb); code != 2 || !strings.Contains(errb.String(), "no target") {
		t.Errorf("no target: %d %s", code, errb.String())
	}
	if code := run([]string{"check", "http://x", "--exit-on", "maybe"}, io.Discard, &errb); code != 2 {
		t.Errorf("bad --exit-on: %d", code)
	}
	if code := run([]string{"check", "not a url", "--exit-on", "error"}, &out, &errb); code != 3 || !strings.Contains(out.String(), "invalid-target") {
		t.Errorf("invalid target: %d\n%s", code, out.String())
	}
}
