// Package fetch is the HTTP side of the probe: one GET with a byte budget, timings,
// optional pinning of the connection to one IP behind a DNS name, and the redaction
// every printed URL goes through.
package fetch

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Response is what one GET produced. Body holds at most the limit asked for; Bytes is
// the whole transfer, kept bytes plus what was drained and counted.
type Response struct {
	URL           string        `json:"url"`
	Status        int           `json:"status"`
	ContentType   string        `json:"content_type,omitempty"`
	ContentLength int64         `json:"content_length"`
	Bytes         int64         `json:"bytes"`
	Truncated     bool          `json:"truncated,omitempty"`
	TTFB          time.Duration `json:"ttfb"`
	Latency       time.Duration `json:"latency"`
	Body          []byte        `json:"-"`
}

// Fetcher is the one method the probe needs; tests can hand in their own.
type Fetcher interface {
	Get(ctx context.Context, rawURL string, limit int64) (*Response, error)
}

// Client is the real Fetcher.
type Client struct {
	HTTP      *http.Client
	UserAgent string
	Headers   map[string]string
	// IP pins every connection to this address whatever the URL's host says; the Host
	// header and the TLS server name stay those of the URL.
	IP string
}

// New builds a client with a whole-request timeout. Redirects are followed (up to
// ten, Go's default); with insecure the certificate is not verified — useful to reach
// an origin by IP, never the default.
func New(timeout time.Duration, insecure bool) *Client {
	c := &Client{UserAgent: "hlsdoctor"}
	c.HTTP = &http.Client{Timeout: timeout, Transport: c.transport(insecure)}
	return c
}

func (c *Client) transport(insecure bool) *http.Transport {
	d := &net.Dialer{Timeout: 5 * time.Second}
	t := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec // opt-in by flag
		ResponseHeaderTimeout: 10 * time.Second,
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     true,
	}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if c.IP != "" {
			_, port, err := net.SplitHostPort(addr)
			if err == nil {
				addr = net.JoinHostPort(c.IP, port)
			}
		}
		return d.DialContext(ctx, network, addr)
	}
	return t
}

// ForIP returns a copy of the client whose connections go to ip.
func (c *Client) ForIP(ip string, insecure bool) *Client {
	n := &Client{UserAgent: c.UserAgent, Headers: c.Headers, IP: ip}
	n.HTTP = &http.Client{Timeout: c.HTTP.Timeout, Transport: n.transport(insecure)}
	return n
}

// Get performs the request. A non-2xx status is not an error: the status is the finding.
func (c *Client) Get(ctx context.Context, rawURL string, limit int64) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "*/*")
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %s", Redact(rawURL), redactErr(err))
	}
	defer resp.Body.Close()
	r := &Response{URL: Redact(rawURL), Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), ContentLength: resp.ContentLength, TTFB: time.Since(start)}
	if limit < 0 {
		limit = 0
	}
	r.Body, err = io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return r, fmt.Errorf("%s: read: %s", r.URL, redactErr(err))
	}
	r.Bytes = int64(len(r.Body))
	n, err := io.Copy(io.Discard, resp.Body)
	r.Bytes += n
	r.Truncated = n > 0
	r.Latency = time.Since(start)
	if err != nil {
		return r, fmt.Errorf("%s: read: %s", r.URL, redactErr(err))
	}
	return r, nil
}

// ResolveIPs returns the A and AAAA records of a URL's host, or the literal IP.
func ResolveIPs(ctx context.Context, rawURL string) ([]string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		return []string{host}, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.IP.String())
	}
	return out, nil
}

// Redact strips the query string and any userinfo from a URL: tokens live there.
// The path is kept, so a finding still names the stream — except on rtmp(s)://, where
// the last path element is the stream key (rtmp://host/app/<key>): it is replaced by
// an ellipsis when the path has two or more elements, and the application is kept.
func Redact(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		if i := strings.IndexByte(rawURL, '?'); i >= 0 {
			return rawURL[:i] + "?…"
		}
		return rawURL
	}
	u.User = nil
	if u.Scheme == "rtmp" || u.Scheme == "rtmps" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 && parts[len(parts)-1] != "" {
			u.Path = "/" + strings.Join(parts[:len(parts)-1], "/") + "/…"
			u.RawPath = ""
		}
	}
	q := ""
	if u.RawQuery != "" || u.ForceQuery {
		u.RawQuery = ""
		u.ForceQuery = false
		q = "?…"
	}
	// url.String percent-encodes the ellipsis in a path; print it as the glyph.
	return strings.Replace(u.String(), "%E2%80%A6", "…", 1) + q
}

// redactErr keeps a transport error from leaking the full URL with its query.
func redactErr(err error) string {
	s := err.Error()
	if ue, ok := err.(*url.Error); ok {
		s = ue.Err.Error()
		if ue.URL != "" {
			s = strings.ReplaceAll(s, ue.URL, Redact(ue.URL))
		}
	}
	if i := strings.Index(s, "?"); i >= 0 && strings.Contains(s[:i], "://") {
		if j := strings.IndexAny(s[i:], " \"'"); j >= 0 {
			s = s[:i] + "?…" + s[i+j:]
		} else {
			s = s[:i] + "?…"
		}
	}
	return s
}
