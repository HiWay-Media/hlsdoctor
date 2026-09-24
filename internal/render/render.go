// Package render prints the reports and the findings for a terminal.
package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/hiway-media/hlsdoctor/internal/findings"
	"github.com/hiway-media/hlsdoctor/internal/hls"
	"github.com/hiway-media/hlsdoctor/internal/rtmp"
)

func table(headers []string, rows [][]string) string {
	w := make([]int, len(headers))
	for i, h := range headers {
		w[i] = len([]rune(h))
	}
	for _, r := range rows {
		for i, c := range r {
			if n := len([]rune(c)); n > w[i] {
				w[i] = n
			}
		}
	}
	line := func(cells []string) string {
		parts := make([]string, len(cells))
		for i, c := range cells {
			parts[i] = c + strings.Repeat(" ", w[i]-len([]rune(c)))
		}
		return "│ " + strings.Join(parts, " │ ") + " │"
	}
	sep := make([]string, len(w))
	for i, n := range w {
		sep[i] = strings.Repeat("─", n+2)
	}
	out := []string{line(headers), "├" + strings.Join(sep, "┼") + "┤"}
	for _, r := range rows {
		out = append(out, line(r))
	}
	return strings.Join(out, "\n")
}

func ms(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func row(label string, v hls.VariantReport, pl hls.PlaylistReport) []string {
	if pl.Error != "" && !strings.HasPrefix(pl.Error, "second fetch") {
		return []string{label, kbps(v.Bandwidth), v.Resolution.String(), v.Codecs, "-", "-", "-", "-", "-", "-", "-", pl.Error}
	}
	kind := "vod"
	if pl.Live {
		kind = "live"
	}
	adv := "-"
	if pl.Refetched {
		adv = "no"
		if pl.Advanced {
			adv = "yes"
		}
	}
	behind := "-"
	if pl.Behind >= 0 {
		behind = fmt.Sprintf("%.0fs", pl.Behind)
	}
	seg := "-"
	if len(pl.Segments) > 0 {
		s := pl.Segments[len(pl.Segments)-1]
		if s.Error != "" {
			seg = s.Error
		} else {
			seg = fmt.Sprintf("%s %dk %s", s.Container, s.Bytes/1000, ms(s.Latency))
		}
	}
	return []string{label, kbps(v.Bandwidth), v.Resolution.String(), v.Codecs, kind, fmt.Sprintf("%g", pl.TargetDuration), fmt.Sprintf("%d/%.0fs", pl.SegmentCount, pl.Window), fmt.Sprint(pl.LastSequence), adv, behind, ms(pl.Latency), seg}
}

func kbps(b int64) string {
	if b == 0 {
		return "-"
	}
	return fmt.Sprintf("%dk", b/1000)
}

// Report prints one HLS report as a table, one row per variant.
func Report(r hls.Report) string {
	head := fmt.Sprintf("hlsdoctor · %s · %s", r.URL, r.At.UTC().Format("2006-01-02 15:04:05Z"))
	if r.Node != "" {
		head += " · via " + r.Node
	}
	if r.Error != "" {
		return head + "\n  " + r.Error + "\n"
	}
	headers := []string{"variant", "bw", "resolution", "codecs", "kind", "target", "segs/window", "seq", "advanced", "behind", "fetch", "newest segment"}
	rows := [][]string{}
	if r.Master {
		head += fmt.Sprintf(" · master, %d variant(s), %d rendition(s)", len(r.Variants), r.Renditions)
		for _, v := range r.Variants {
			rows = append(rows, row(v.Label, v, v.Playlist))
		}
	} else if r.Playlist != nil {
		head += " · media playlist"
		rows = append(rows, row("media", hls.VariantReport{}, *r.Playlist))
	}
	return head + "\n" + table(headers, rows) + "\n"
}

// RTMP prints one handshake result.
func RTMP(r rtmp.Result) string {
	head := fmt.Sprintf("hlsdoctor · %s · rtmp %s", r.URL, r.Addr)
	if r.Error != "" {
		return fmt.Sprintf("%s\n  %s (stage %s)\n", head, r.Error, r.Stage)
	}
	return fmt.Sprintf("%s\n  handshake ok: connect %s, handshake %s, server time %d, echo %v\n", head, ms(r.Connect), ms(r.Handshake), r.ServerTime, r.Echo)
}

var glyph = map[findings.Level]string{findings.OK: "🟢 OK   ", findings.WARN: "🟡 WARN ", findings.BAD: "🔴 BAD  ", findings.ERROR: "⚫ ERROR"}

// Findings prints the verdicts, worst first, and a one-line tally.
func Findings(fs []findings.Finding) string {
	var b strings.Builder
	tally := map[findings.Level]int{}
	for _, f := range fs {
		tally[f.Level]++
		target := f.URL
		if f.Node != "" {
			target += " @" + f.Node
		}
		v := f.Variant
		if v == "" {
			v = "-"
		}
		fmt.Fprintf(&b, "%s %-20s %-16s %s\n      %s\n", glyph[f.Level], f.Code, v, f.Message, target)
	}
	fmt.Fprintf(&b, "\n%d findings: %d OK, %d WARN, %d BAD, %d ERROR\n", len(fs), tally[findings.OK], tally[findings.WARN], tally[findings.BAD], tally[findings.ERROR])
	return b.String()
}
