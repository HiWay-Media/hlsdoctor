package rtmp

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeServer answers the handshake like nginx-rtmp does: S0=3, S1 with its own time and
// random bytes, S2 echoing C1. With badVersion it answers S0=6 and closes; with silent
// it accepts and never writes.
func fakeServer(t *testing.T, mode string) string {
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
				c0c1 := make([]byte, 1+handshakeSize)
				if _, err := io.ReadFull(conn, c0c1); err != nil {
					return
				}
				switch mode {
				case "badVersion":
					conn.Write([]byte{6})
					return
				case "silent":
					time.Sleep(2 * time.Second)
					return
				case "short":
					conn.Write([]byte{3, 0, 0})
					return
				}
				s1 := make([]byte, handshakeSize)
				binary.BigEndian.PutUint32(s1[0:4], 4242)
				for i := 8; i < len(s1); i++ {
					s1[i] = byte(i)
				}
				s2 := make([]byte, handshakeSize)
				copy(s2, c0c1[1:])
				conn.Write([]byte{3})
				conn.Write(s1)
				conn.Write(s2)
				io.ReadFull(conn, make([]byte, handshakeSize))
			}()
		}
	}()
	return ln.Addr().String()
}

func TestHandshakeOK(t *testing.T) {
	addr := fakeServer(t, "ok")
	r := Probe(context.Background(), "rtmp://"+addr+"/live/streamkey?token=secret", 2*time.Second, false)
	if r.Error != "" || r.Stage != "done" || !r.Echo || r.ServerVersion != 3 || r.ServerTime != 4242 {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(r.URL, "secret") || strings.Contains(r.URL, "streamkey") || !strings.HasSuffix(r.URL, "/live/…?…") {
		t.Errorf("URL not redacted: %s", r.URL)
	}
	if r.Connect <= 0 || r.Handshake < r.Connect {
		t.Errorf("timings %v %v", r.Connect, r.Handshake)
	}
}

func TestHandshakeFailures(t *testing.T) {
	cases := map[string]struct{ stage, want string }{
		"badVersion": {"s0", "version 6"},
		"short":      {"s1", "closed before"},
		"silent":     {"s0", "timeout"},
	}
	for mode, c := range cases {
		addr := fakeServer(t, mode)
		r := Probe(context.Background(), "rtmp://"+addr+"/live", 300*time.Millisecond, false)
		if r.Stage != c.stage || !strings.Contains(r.Error, c.want) {
			t.Errorf("%s: stage=%s error=%q, want stage %s containing %q", mode, r.Stage, r.Error, c.stage, c.want)
		}
	}
}

func TestRefusedAndBadURL(t *testing.T) {
	r := Probe(context.Background(), "rtmp://127.0.0.1:1/live", time.Second, false)
	if r.Stage != "dial" || !strings.HasPrefix(r.Error, "dial:") {
		t.Errorf("refused: %+v", r)
	}
	r = Probe(context.Background(), "https://example/live", time.Second, false)
	if r.Error == "" {
		t.Errorf("https accepted as rtmp")
	}
	r = Probe(context.Background(), "rtmps://127.0.0.1:1/live", time.Second, false)
	if r.Addr != "127.0.0.1:1" || !r.TLS {
		t.Errorf("rtmps: %+v", r)
	}
	if r := Probe(context.Background(), "rtmps://origin.example/live", 0, false); r.Addr != "origin.example:443" {
		t.Errorf("rtmps default port: %s", r.Addr)
	}
}
