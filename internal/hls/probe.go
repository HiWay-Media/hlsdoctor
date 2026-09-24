// Package hls probes a stream the way a player would: fetch the master, fetch each
// variant, fetch again after a target duration to see it move, download the newest
// segment and look at its first bytes. It gathers facts; the verdicts live in findings.
package hls

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hiway-media/hlsdoctor/internal/fetch"
	"github.com/hiway-media/hlsdoctor/internal/m3u8"
)

// Options tune the probe; every zero value has a sensible meaning.
type Options struct {
	// Wait between the two fetches of a live playlist; 0 means one target duration,
	// capped at MaxWait. Negative disables the second fetch.
	Wait    time.Duration
	MaxWait time.Duration
	// Segments is how many of the newest segments to download per variant (0 = none).
	Segments int
	// Variants caps how many variants of a master are probed (0 = all).
	Variants int
	// Concurrency bounds the variant probes running at once.
	Concurrency int
	// PlaylistLimit and SegmentKeep are byte budgets: the playlist body kept, and the
	// segment prefix kept for container detection (the rest is drained and counted).
	PlaylistLimit int64
	SegmentKeep   int64
	// Now and Sleep are injectable for tests.
	Now   func() time.Time
	Sleep func(context.Context, time.Duration)
}

func (o Options) withDefaults() Options {
	if o.MaxWait == 0 {
		o.MaxWait = 15 * time.Second
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 4
	}
	if o.PlaylistLimit <= 0 {
		o.PlaylistLimit = 4 << 20
	}
	if o.SegmentKeep <= 0 {
		o.SegmentKeep = 64 << 10
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = func(ctx context.Context, d time.Duration) {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
			case <-t.C:
			}
		}
	}
	return o
}

// SegmentReport is one downloaded segment.
type SegmentReport struct {
	URL       string        `json:"url"`
	Sequence  int64         `json:"sequence"`
	Duration  float64       `json:"duration"`
	Status    int           `json:"status"`
	Bytes     int64         `json:"bytes"`
	Latency   time.Duration `json:"latency"`
	Container string        `json:"container"` // ts, fmp4, text, unknown
	Error     string        `json:"error,omitempty"`
}

// PlaylistReport is one media playlist, fetched once or twice.
type PlaylistReport struct {
	URL                string          `json:"url"`
	Status             int             `json:"status"`
	Latency            time.Duration   `json:"latency"`
	Bytes              int64           `json:"bytes"`
	Error              string          `json:"error,omitempty"`
	Live               bool            `json:"live"`
	PlaylistType       string          `json:"playlist_type,omitempty"`
	Version            int             `json:"version"`
	TargetDuration     float64         `json:"target_duration"`
	PartTarget         float64         `json:"part_target,omitempty"`
	SegmentCount       int             `json:"segment_count"`
	Window             float64         `json:"window_seconds"`
	MaxSegmentDuration float64         `json:"max_segment_duration"`
	MediaSequence      int64           `json:"media_sequence"`
	LastSequence       int64           `json:"last_sequence"`
	Discontinuities    int             `json:"discontinuities"`
	Gaps               int             `json:"gaps"`
	HasMap             bool            `json:"has_map"`
	LastPDT            time.Time       `json:"last_pdt,omitempty"`
	Behind             float64         `json:"behind_seconds"` // -1 when no PROGRAM-DATE-TIME
	Wait               time.Duration   `json:"wait"`
	Refetched          bool            `json:"refetched"`
	Advanced           bool            `json:"advanced"`
	SecondLastSequence int64           `json:"second_last_sequence"`
	Segments           []SegmentReport `json:"segments,omitempty"`
}

// VariantReport is a variant of the master with its playlist probed.
type VariantReport struct {
	m3u8.Variant
	Label    string         `json:"label"`
	Playlist PlaylistReport `json:"playlist"`
}

// Report is the whole probe of one URL.
type Report struct {
	URL        string          `json:"url"`
	Node       string          `json:"node,omitempty"` // the IP when pinned
	At         time.Time       `json:"at"`
	Status     int             `json:"status"`
	Latency    time.Duration   `json:"latency"`
	Error      string          `json:"error,omitempty"`
	Master     bool            `json:"master"`
	Renditions int             `json:"renditions"`
	Variants   []VariantReport `json:"variants,omitempty"`
	Playlist   *PlaylistReport `json:"playlist,omitempty"` // when the URL was a media playlist
}

// Probe runs the whole thing. It never returns an error: everything is in the report.
func Probe(ctx context.Context, f fetch.Fetcher, rawURL string, o Options) Report {
	o = o.withDefaults()
	rep := Report{URL: fetch.Redact(rawURL), At: o.Now()}
	resp, err := f.Get(ctx, rawURL, o.PlaylistLimit)
	if err != nil {
		rep.Error = err.Error()
		if resp != nil {
			rep.Status = resp.Status
		}
		return rep
	}
	rep.Status, rep.Latency = resp.Status, resp.Latency
	if resp.Status/100 != 2 {
		rep.Error = fmt.Sprintf("HTTP %d", resp.Status)
		return rep
	}
	pl, err := m3u8.Parse(resp.Body)
	if err != nil {
		rep.Error = err.Error() + describeBody(resp)
		return rep
	}
	if !pl.Master {
		pr := probeMedia(ctx, f, rawURL, pl, resp, o)
		rep.Playlist = &pr
		return rep
	}
	rep.Master = true
	rep.Renditions = len(pl.Renditions)
	vars := pl.Variants
	if o.Variants > 0 && len(vars) > o.Variants {
		vars = vars[:o.Variants]
	}
	rep.Variants = make([]VariantReport, len(vars))
	var wg sync.WaitGroup
	sem := make(chan struct{}, o.Concurrency)
	for i, v := range vars {
		rep.Variants[i] = VariantReport{Variant: v, Label: v.Label()}
		u, err := m3u8.Resolve(rawURL, v.URI)
		if err != nil {
			rep.Variants[i].Playlist = PlaylistReport{URL: fetch.Redact(v.URI), Error: "bad variant URI: " + err.Error(), Behind: -1}
			continue
		}
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rep.Variants[i].Playlist = probeMedia(ctx, f, u, nil, nil, o)
		}(i, u)
	}
	wg.Wait()
	return rep
}

// probeMedia fetches (or reuses) a media playlist, waits, refetches, downloads segments.
func probeMedia(ctx context.Context, f fetch.Fetcher, rawURL string, pl *m3u8.Playlist, resp *fetch.Response, o Options) PlaylistReport {
	pr := PlaylistReport{URL: fetch.Redact(rawURL), Behind: -1}
	if pl == nil {
		var err error
		resp, err = f.Get(ctx, rawURL, o.PlaylistLimit)
		if err != nil {
			pr.Error = err.Error()
			if resp != nil {
				pr.Status = resp.Status
			}
			return pr
		}
		pr.Status, pr.Latency, pr.Bytes = resp.Status, resp.Latency, resp.Bytes
		if resp.Status/100 != 2 {
			pr.Error = fmt.Sprintf("HTTP %d", resp.Status)
			return pr
		}
		pl, err = m3u8.Parse(resp.Body)
		if err != nil {
			pr.Error = err.Error() + describeBody(resp)
			return pr
		}
	} else {
		pr.Status, pr.Latency, pr.Bytes = resp.Status, resp.Latency, resp.Bytes
	}
	if pl.Master {
		pr.Error = "a master playlist where a media playlist was expected"
		return pr
	}
	fill(&pr, pl, o.Now())

	if pr.Live && o.Wait >= 0 && len(pl.Segments) > 0 {
		wait := o.Wait
		if wait == 0 {
			wait = time.Duration(pl.TargetDuration * float64(time.Second))
			if wait <= 0 {
				wait = 2 * time.Second
			}
		}
		if wait > o.MaxWait {
			wait = o.MaxWait
		}
		pr.Wait = wait
		o.Sleep(ctx, wait)
		if ctx.Err() == nil {
			pr.Refetched = true
			if r2, err := f.Get(ctx, rawURL, o.PlaylistLimit); err == nil && r2.Status/100 == 2 {
				if p2, err := m3u8.Parse(r2.Body); err == nil && !p2.Master {
					pr.SecondLastSequence = p2.LastSequence()
					pr.Advanced = p2.LastSequence() > pl.LastSequence() || p2.PartCount > pl.PartCount || (len(p2.Segments) > 0 && len(pl.Segments) > 0 && p2.Segments[len(p2.Segments)-1].URI != pl.Segments[len(pl.Segments)-1].URI)
					if pr.Advanced {
						pl = p2
						fill(&pr, pl, o.Now())
						pr.Status, pr.Latency, pr.Bytes = r2.Status, r2.Latency, r2.Bytes
					}
				} else if err != nil {
					pr.Error = "second fetch: " + err.Error()
				}
			} else if err != nil {
				pr.Error = "second fetch: " + err.Error()
			} else {
				pr.Error = fmt.Sprintf("second fetch: HTTP %d", r2.Status)
			}
		}
	}

	if o.Segments > 0 && len(pl.Segments) > 0 {
		segs := pl.Segments
		if len(segs) > o.Segments {
			segs = segs[len(segs)-o.Segments:]
		}
		for _, s := range segs {
			if s.Gap {
				continue
			}
			pr.Segments = append(pr.Segments, fetchSegment(ctx, f, rawURL, s, o))
		}
	}
	return pr
}

func fill(pr *PlaylistReport, pl *m3u8.Playlist, now time.Time) {
	pr.Live = pl.Live()
	pr.PlaylistType = pl.PlaylistType
	pr.Version = pl.Version
	pr.TargetDuration = pl.TargetDuration
	pr.PartTarget = pl.PartTargetDuration
	pr.SegmentCount = len(pl.Segments)
	pr.Window = pl.Duration()
	pr.MaxSegmentDuration = pl.MaxSegmentDuration()
	pr.MediaSequence = pl.MediaSequence
	pr.LastSequence = pl.LastSequence()
	pr.Discontinuities = pl.Discontinuities()
	pr.Gaps = 0
	pr.HasMap = false
	for _, s := range pl.Segments {
		if s.Gap {
			pr.Gaps++
		}
		if s.Map != "" {
			pr.HasMap = true
		}
	}
	pr.LastPDT = time.Time{}
	pr.Behind = -1
	// The newest PROGRAM-DATE-TIME, advanced by the durations of the segments after it,
	// gives the wall-clock time the playlist's live edge stands at.
	var edge time.Time
	for _, s := range pl.Segments {
		if !s.ProgramDateTime.IsZero() {
			edge = s.ProgramDateTime
		} else if !edge.IsZero() {
			edge = edge.Add(time.Duration(s.Duration * float64(time.Second)))
		}
		if !edge.IsZero() {
			pr.LastPDT = edge
		}
	}
	if !pr.LastPDT.IsZero() && len(pl.Segments) > 0 {
		last := pl.Segments[len(pl.Segments)-1]
		end := pr.LastPDT
		if last.ProgramDateTime.IsZero() || last.ProgramDateTime.Equal(pr.LastPDT) {
			end = end.Add(time.Duration(last.Duration * float64(time.Second)))
		}
		pr.Behind = now.Sub(end).Seconds()
	}
}

func fetchSegment(ctx context.Context, f fetch.Fetcher, playlistURL string, s m3u8.Segment, o Options) SegmentReport {
	sr := SegmentReport{Sequence: s.Sequence, Duration: s.Duration, Container: "unknown"}
	u, err := m3u8.Resolve(playlistURL, s.URI)
	if err != nil {
		sr.URL, sr.Error = fetch.Redact(s.URI), "bad segment URI: "+err.Error()
		return sr
	}
	sr.URL = fetch.Redact(u)
	resp, err := f.Get(ctx, u, o.SegmentKeep)
	if resp != nil {
		sr.Status, sr.Bytes, sr.Latency = resp.Status, resp.Bytes, resp.Latency
	}
	if err != nil {
		sr.Error = err.Error()
		return sr
	}
	if resp.Status/100 != 2 {
		sr.Error = fmt.Sprintf("HTTP %d", resp.Status)
		return sr
	}
	sr.Container = Container(resp.Body)
	return sr
}

// Container guesses the segment format from its first bytes: MPEG-TS has a 0x47 sync
// byte every 188 bytes; fMP4 starts with a box whose type is ftyp, styp, moof or sidx;
// a playlist or HTML where a segment should be is "text".
func Container(b []byte) string {
	if len(b) == 0 {
		return "empty"
	}
	if b[0] == 0x47 && (len(b) <= 188 || b[188] == 0x47) {
		return "ts"
	}
	if len(b) >= 8 {
		switch string(b[4:8]) {
		case "ftyp", "styp", "moof", "sidx", "moov", "free", "skip", "prft", "emsg":
			return "fmp4"
		}
	}
	if len(b) >= 4 && string(b[:4]) == "ID3\x03" || len(b) >= 3 && string(b[:3]) == "ID3" {
		return "aac"
	}
	trim := bytes.TrimLeft(b, " \t\r\n\xef\xbb\xbf")
	if bytes.HasPrefix(trim, []byte("#EXTM3U")) || bytes.HasPrefix(trim, []byte("<")) || bytes.HasPrefix(trim, []byte("{")) {
		return "text"
	}
	return "unknown"
}

func describeBody(r *fetch.Response) string {
	if r == nil {
		return ""
	}
	ct := r.ContentType
	if ct == "" {
		ct = "no content-type"
	}
	return fmt.Sprintf(" (HTTP %d, %s, %d bytes)", r.Status, ct, r.Bytes)
}
