// Package rtmp performs the RTMP handshake (C0/C1 → S0/S1/S2 → C2) and nothing more:
// enough to say that a server is listening, speaks RTMP version 3 and answers within
// the timeout. It never sends connect, publish or play, so it needs no stream key and
// leaves no trace in the server's application log beyond a TCP connection.
package rtmp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/hiway-media/hlsdoctor/internal/fetch"
)

const (
	version       = 0x03
	handshakeSize = 1536
)

// Result is what the handshake told us.
type Result struct {
	URL           string        `json:"url"`
	Addr          string        `json:"addr"`
	TLS           bool          `json:"tls"`
	Connect       time.Duration `json:"connect"`
	Handshake     time.Duration `json:"handshake"`
	ServerVersion int           `json:"server_version"`
	ServerTime    uint32        `json:"server_time"`
	Echo          bool          `json:"echo"`
	Error         string        `json:"error,omitempty"`
	// Stage is how far it got: dial, s0, s1, s2, done.
	Stage string `json:"stage"`
}

// Probe dials the URL (rtmp:// on 1935, rtmps:// on 443 unless a port is given) and
// completes the handshake. The URL is redacted before it is stored.
func Probe(ctx context.Context, rawURL string, timeout time.Duration, insecure bool) Result {
	r := Result{URL: fetch.Redact(rawURL), Stage: "dial"}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "rtmp" && u.Scheme != "rtmps") || u.Hostname() == "" {
		r.Error = "not an rtmp:// or rtmps:// URL"
		return r
	}
	r.TLS = u.Scheme == "rtmps"
	port := u.Port()
	if port == "" {
		port = "1935"
		if r.TLS {
			port = "443"
		}
	}
	r.Addr = net.JoinHostPort(u.Hostname(), port)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	d := &net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", r.Addr)
	if err != nil {
		r.Error = "dial: " + err.Error()
		return r
	}
	defer conn.Close()
	if r.TLS {
		tc := tls.Client(conn, &tls.Config{ServerName: u.Hostname(), InsecureSkipVerify: insecure}) //nolint:gosec // opt-in by flag
		if err := tc.HandshakeContext(ctx); err != nil {
			r.Error = "tls: " + err.Error()
			return r
		}
		conn = tc
	}
	r.Connect = time.Since(start)
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)

	c1 := make([]byte, handshakeSize)
	binary.BigEndian.PutUint32(c1[0:4], uint32(time.Since(start).Milliseconds()))
	if _, err := rand.Read(c1[8:]); err != nil {
		r.Error = "random: " + err.Error()
		return r
	}
	if _, err := conn.Write(append([]byte{version}, c1...)); err != nil {
		r.Error = "write C0C1: " + err.Error()
		return r
	}
	r.Stage = "s0"
	s0 := make([]byte, 1)
	if _, err := io.ReadFull(conn, s0); err != nil {
		r.Error = "read S0: " + describe(err)
		return r
	}
	r.ServerVersion = int(s0[0])
	if s0[0] != version {
		r.Error = fmt.Sprintf("server answered RTMP version %d, not 3", s0[0])
		return r
	}
	r.Stage = "s1"
	s1 := make([]byte, handshakeSize)
	if _, err := io.ReadFull(conn, s1); err != nil {
		r.Error = "read S1: " + describe(err)
		return r
	}
	r.ServerTime = binary.BigEndian.Uint32(s1[0:4])
	r.Stage = "s2"
	s2 := make([]byte, handshakeSize)
	if _, err := io.ReadFull(conn, s2); err != nil {
		r.Error = "read S2: " + describe(err)
		return r
	}
	r.Echo = bytes.Equal(s2[8:], c1[8:])
	c2 := make([]byte, handshakeSize)
	copy(c2[0:4], s1[0:4])
	binary.BigEndian.PutUint32(c2[4:8], uint32(time.Since(start).Milliseconds()))
	copy(c2[8:], s1[8:])
	if _, err := conn.Write(c2); err != nil {
		r.Error = "write C2: " + err.Error()
		return r
	}
	r.Stage = "done"
	r.Handshake = time.Since(start)
	return r
}

func describe(err error) string {
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return "connection closed before the handshake completed"
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return "timeout"
	}
	return err.Error()
}

// DefaultPort is exported for the CLI's help text.
var DefaultPort = strconv.Itoa(1935)
