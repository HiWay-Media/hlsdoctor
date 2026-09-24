// hlsdoctor — is the stream actually playable? HLS and RTMP, probed the way a player
// would, with a verdict.
//
//	hlsdoctor check <url>...   findings, worst first: stale playlists, dead segments,
//	                           error pages served as media, a ladder out of step, an
//	                           RTMP ingest that does not answer
//	hlsdoctor ls <url>         one row per variant: bandwidth, codecs, live window,
//	                           sequence, whether it advanced, how far behind live
//	hlsdoctor version
//
// A target is an http(s):// playlist (master or media) or an rtmp(s):// endpoint; with
// --from, one per line from a file. Flags:
//
//	--timeout 10s      per request      --wait 0 (one target duration)   --max-wait 15s
//	--segments 1       newest segments to download per variant   --no-segments
//	--variants 0       cap on variants probed (0 = all)           --concurrency 4
//	--each-ip          resolve the host and probe every address, then compare them
//	--max-behind 0     seconds behind live before WARN (0 = 3× target duration)
//	--min-window 0     live window in seconds before WARN (0 = 3× target duration)
//	--slow 0.5         segment download time / segment duration before WARN
//	--skew 2           segments of sequence difference tolerated between rungs or nodes
//	--header K=V       extra request header (repeatable; never printed)
//	--user-agent       --insecure-tls      --no-ok      --json      --exit-on warn|bad|error
//
// Reads only: GET on playlists and segments, a TCP handshake on RTMP. Never publishes,
// never sends connect or play. Query strings and userinfo are stripped from every URL
// it prints, because that is where tokens live; headers are never printed.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hiway-media/hlsdoctor/internal/fetch"
	"github.com/hiway-media/hlsdoctor/internal/findings"
	"github.com/hiway-media/hlsdoctor/internal/hls"
	"github.com/hiway-media/hlsdoctor/internal/render"
	"github.com/hiway-media/hlsdoctor/internal/rtmp"
	"github.com/hiway-media/hlsdoctor/internal/version"
)

type headers map[string]string

func (h headers) String() string { return fmt.Sprintf("%d header(s)", len(h)) }
func (h headers) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok || strings.TrimSpace(k) == "" {
		return fmt.Errorf("--header wants Name=value")
	}
	h[strings.TrimSpace(k)] = strings.TrimSpace(val)
	return nil
}

type options struct {
	timeout, wait, maxWait          time.Duration
	segments, variants, concurrency int
	noSegments, eachIP, jsonOut     bool
	noOK, insecure                  bool
	maxBehind, minWindow, slow      float64
	skew                            int64
	exitOn, userAgent, from         string
	headers                         headers
	targets                         []string
}

func parse(args []string) (string, options, error) {
	fs := flag.NewFlagSet("hlsdoctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	o := options{headers: headers{}}
	fs.DurationVar(&o.timeout, "timeout", 10*time.Second, "per-request timeout")
	fs.DurationVar(&o.wait, "wait", 0, "wait between the two fetches of a live playlist (0 = one target duration, negative = do not refetch)")
	fs.DurationVar(&o.maxWait, "max-wait", 15*time.Second, "cap on --wait when it comes from the target duration")
	fs.IntVar(&o.segments, "segments", 1, "newest segments to download per variant")
	fs.IntVar(&o.variants, "variants", 0, "cap on the variants probed (0 = all)")
	fs.IntVar(&o.concurrency, "concurrency", 4, "variants and targets probed at once")
	fs.BoolVar(&o.noSegments, "no-segments", false, "do not download segments")
	fs.BoolVar(&o.eachIP, "each-ip", false, "resolve the host and probe every address, then compare them")
	fs.BoolVar(&o.jsonOut, "json", false, "JSON output")
	fs.BoolVar(&o.noOK, "no-ok", false, "do not report what passed")
	fs.BoolVar(&o.insecure, "insecure-tls", false, "do not verify TLS certificates")
	fs.Float64Var(&o.maxBehind, "max-behind", 0, "seconds behind live before WARN (0 = 3× target duration)")
	fs.Float64Var(&o.minWindow, "min-window", 0, "live window in seconds before WARN (0 = 3× target duration)")
	fs.Float64Var(&o.slow, "slow", findings.Default.SlowFactor, "download time / segment duration before WARN (above 1 is BAD)")
	fs.Int64Var(&o.skew, "skew", findings.Default.SkewSegments, "segments of sequence difference tolerated between rungs or nodes")
	fs.StringVar(&o.exitOn, "exit-on", "", "exit code policy: warn|bad|error (default: always 0)")
	fs.StringVar(&o.userAgent, "user-agent", "hlsdoctor/"+version.Version, "User-Agent header")
	fs.StringVar(&o.from, "from", "", "file with one target per line (# comments)")
	fs.Var(o.headers, "header", "extra request header Name=value (repeatable)")
	cmd := "check"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	// Flags and targets may be interleaved: parse, take the first positional, parse on.
	for {
		if err := fs.Parse(args); err != nil {
			return "", o, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		o.targets = append(o.targets, rest[0])
		args = rest[1:]
	}
	if o.from != "" {
		f, err := os.Open(o.from)
		if err != nil {
			return "", o, err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			o.targets = append(o.targets, line)
		}
	}
	switch o.exitOn {
	case "", "warn", "bad", "error":
	default:
		return "", o, fmt.Errorf("--exit-on wants warn, bad or error")
	}
	if o.noSegments {
		o.segments = 0
	}
	return cmd, o, nil
}

func usage() string {
	return `hlsdoctor — is the stream actually playable? HLS and RTMP, with a verdict.

  hlsdoctor check <url>...   findings, worst first (--json, --exit-on warn|bad|error)
  hlsdoctor ls <url>...      one row per variant
  hlsdoctor version

Targets: http(s):// playlists (master or media), rtmp(s):// endpoints, or --from FILE.
Flags: --timeout --wait --max-wait --segments --no-segments --variants --concurrency
       --each-ip --max-behind --min-window --slow --skew --header --user-agent
       --insecure-tls --no-ok --json --exit-on
`
}

func (o options) policy() findings.Policy {
	return findings.Policy{MaxBehind: o.maxBehind, MinWindow: o.minWindow, SlowFactor: o.slow, SkewSegments: o.skew, OKIsFinding: !o.noOK}
}

func (o options) probeOptions() hls.Options {
	return hls.Options{Wait: o.wait, MaxWait: o.maxWait, Segments: o.segments, Variants: o.variants, Concurrency: o.concurrency}
}

// target is one probed URL: an HLS report (one per node with --each-ip) or an RTMP result.
type target struct {
	URL      string             `json:"url"`
	Kind     string             `json:"kind"` // hls, rtmp, invalid
	HLS      []hls.Report       `json:"hls,omitempty"`
	RTMP     *rtmp.Result       `json:"rtmp,omitempty"`
	Error    string             `json:"error,omitempty"`
	Findings []findings.Finding `json:"findings"`
}

func probe(ctx context.Context, raw string, o options) target {
	t := target{URL: fetch.Redact(raw)}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		t.Kind, t.Error = "invalid", "not a URL"
		t.Findings = []findings.Finding{findings.Invalid(t.URL, "not a URL with a scheme and a host")}
		return t
	}
	switch u.Scheme {
	case "rtmp", "rtmps":
		t.Kind = "rtmp"
		r := rtmp.Probe(ctx, raw, o.timeout, o.insecure)
		t.RTMP = &r
		t.Findings = findings.EvaluateRTMP(r, o.policy())
	case "http", "https":
		t.Kind = "hls"
		client := fetch.New(o.timeout, o.insecure)
		client.UserAgent, client.Headers = o.userAgent, o.headers
		if !o.eachIP {
			r := hls.Probe(ctx, client, raw, o.probeOptions())
			t.HLS = []hls.Report{r}
			t.Findings = findings.Evaluate(r, o.policy())
			return t
		}
		ips, err := fetch.ResolveIPs(ctx, raw)
		if err != nil || len(ips) == 0 {
			t.Error = "resolve: " + fmt.Sprint(err)
			t.Findings = []findings.Finding{{Level: findings.BAD, Code: "unreachable", URL: t.URL, Message: t.Error}}
			return t
		}
		t.HLS = make([]hls.Report, len(ips))
		var wg sync.WaitGroup
		for i, ip := range ips {
			wg.Add(1)
			go func(i int, ip string) {
				defer wg.Done()
				r := hls.Probe(ctx, client.ForIP(ip, o.insecure), raw, o.probeOptions())
				r.Node = ip
				t.HLS[i] = r
			}(i, ip)
		}
		wg.Wait()
		for _, r := range t.HLS {
			t.Findings = append(t.Findings, findings.Evaluate(r, o.policy())...)
		}
		t.Findings = append(t.Findings, findings.Nodes(t.URL, t.HLS, o.policy())...)
		t.Findings = findings.Sort(t.Findings)
	default:
		t.Kind, t.Error = "invalid", "unsupported scheme "+u.Scheme
		t.Findings = []findings.Finding{findings.Invalid(t.URL, "unsupported scheme "+u.Scheme+" — http(s) or rtmp(s)")}
	}
	return t
}

func probeAll(ctx context.Context, o options) []target {
	out := make([]target, len(o.targets))
	sem := make(chan struct{}, max(1, o.concurrency))
	var wg sync.WaitGroup
	for i, raw := range o.targets {
		wg.Add(1)
		go func(i int, raw string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = probe(ctx, raw, o)
		}(i, raw)
	}
	wg.Wait()
	return out
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd, o, err := parse(args)
	if err != nil {
		fmt.Fprintln(stderr, "hlsdoctor:", err)
		fmt.Fprint(stderr, usage())
		return 2
	}
	ctx := context.Background()
	switch cmd {
	case "version":
		fmt.Fprintln(stdout, "hlsdoctor", version.Version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage())
		return 0
	case "check", "ls":
	default:
		fmt.Fprintf(stderr, "hlsdoctor: unknown command %q\n%s", cmd, usage())
		return 2
	}
	if len(o.targets) == 0 {
		fmt.Fprintln(stderr, "hlsdoctor: no target — give a URL or --from FILE")
		return 2
	}
	targets := probeAll(ctx, o)
	var all []findings.Finding
	for _, t := range targets {
		all = append(all, t.Findings...)
	}
	all = findings.Sort(all)
	if o.jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(map[string]any{"at": time.Now().UTC(), "version": version.Version, "targets": targets, "findings": all, "worst": findings.Worst(all)})
		return findings.ExitCode(all, o.exitOn)
	}
	if cmd == "ls" {
		for _, t := range targets {
			switch t.Kind {
			case "rtmp":
				fmt.Fprint(stdout, render.RTMP(*t.RTMP))
			case "hls":
				for _, r := range t.HLS {
					fmt.Fprint(stdout, render.Report(r))
				}
				if t.Error != "" {
					fmt.Fprintf(stdout, "hlsdoctor · %s\n  %s\n", t.URL, t.Error)
				}
			default:
				fmt.Fprintf(stdout, "hlsdoctor · %s\n  %s\n", t.URL, t.Error)
			}
		}
		return findings.ExitCode(all, o.exitOn)
	}
	fmt.Fprint(stdout, render.Findings(all))
	return findings.ExitCode(all, o.exitOn)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
