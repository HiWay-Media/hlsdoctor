package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestGetLimitAndCount(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "hlsdoctor" || r.Header.Get("X-Probe") != "1" {
			t.Errorf("headers not sent: %v", r.Header)
		}
		w.Header().Set("Content-Type", "video/mp2t")
		w.Write(make([]byte, 10000))
	}))
	defer s.Close()
	c := New(5*time.Second, false)
	c.Headers = map[string]string{"X-Probe": "1"}
	r, err := c.Get(context.Background(), s.URL+"/seg.ts?token=secret", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Body) != 1000 || r.Bytes != 10000 || !r.Truncated || r.Status != 200 || r.ContentType != "video/mp2t" {
		t.Errorf("kept=%d bytes=%d truncated=%v status=%d ct=%s", len(r.Body), r.Bytes, r.Truncated, r.Status, r.ContentType)
	}
	if strings.Contains(r.URL, "secret") || !strings.HasSuffix(r.URL, "/seg.ts?…") {
		t.Errorf("URL not redacted: %s", r.URL)
	}
	if r.Latency <= 0 || r.TTFB <= 0 {
		t.Errorf("timings missing: %v %v", r.TTFB, r.Latency)
	}
}

func TestNon2xxIsNotAnError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "gone", 404) }))
	defer s.Close()
	r, err := New(time.Second, false).Get(context.Background(), s.URL, 100)
	if err != nil || r.Status != 404 {
		t.Errorf("err=%v status=%d", err, r.Status)
	}
}

func TestErrorRedactsURL(t *testing.T) {
	c := New(time.Second, false)
	_, err := c.Get(context.Background(), "http://127.0.0.1:1/x.m3u8?token=secret", 100)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("error leaks the query: %v", err)
	}
}

func TestForIPPinsTheConnection(t *testing.T) {
	var gotHost string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gotHost = r.Host; w.Write([]byte("#EXTM3U\n")) }))
	defer s.Close()
	u, _ := url.Parse(s.URL)
	pinned := New(2*time.Second, false).ForIP("127.0.0.1", false)
	r, err := pinned.Get(context.Background(), "http://stream.example.invalid:"+u.Port()+"/master.m3u8", 100)
	if err != nil || r.Status != 200 {
		t.Fatalf("pinned get: %v %+v", err, r)
	}
	if !strings.HasPrefix(gotHost, "stream.example.invalid") {
		t.Errorf("Host header was %q, want the URL's host", gotHost)
	}
	if ips, err := ResolveIPs(context.Background(), "http://127.0.0.1:1/"); err != nil || len(ips) != 1 || ips[0] != "127.0.0.1" {
		t.Errorf("literal ip: %v %v", ips, err)
	}
}

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"https://cdn.example/live/master.m3u8":                      "https://cdn.example/live/master.m3u8",
		"https://cdn.example/live/master.m3u8?token=abc&exp=1":      "https://cdn.example/live/master.m3u8?…",
		"https://user:pass@cdn.example/live/master.m3u8":            "https://cdn.example/live/master.m3u8",
		"rtmp://origin.example:1935/live/streamkey?auth=x":          "rtmp://origin.example:1935/live/…?…",
		"rtmp://origin.example/live":                                "rtmp://origin.example/live",
		"rtmp://origin.example/live/":                               "rtmp://origin.example/live/",
		"rtmps://origin.example/app/sub/sk_live_123":                "rtmps://origin.example/app/sub/…",
		"https://cdn.example/live/720p/index.m3u8?wmsAuthSign=Zm9v": "https://cdn.example/live/720p/index.m3u8?…",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%s) = %s, want %s", in, got, want)
		}
	}
}
