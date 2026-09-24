// Package m3u8 parses HLS playlists (RFC 8216 and the low-latency extensions) into a
// plain structure. It is deliberately tolerant: an unknown tag is counted, not fatal,
// because the point of the probe is to say what a real player would see.
package m3u8

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Resolution is the RESOLUTION attribute of a variant.
type Resolution struct {
	Width, Height int
}

func (r Resolution) String() string {
	if r.Width == 0 && r.Height == 0 {
		return ""
	}
	return fmt.Sprintf("%dx%d", r.Width, r.Height)
}

// Variant is one EXT-X-STREAM-INF entry of a master playlist.
type Variant struct {
	URI              string     `json:"uri"`
	Bandwidth        int64      `json:"bandwidth"`
	AverageBandwidth int64      `json:"average_bandwidth,omitempty"`
	Resolution       Resolution `json:"resolution"`
	Codecs           string     `json:"codecs,omitempty"`
	FrameRate        float64    `json:"frame_rate,omitempty"`
	Audio            string     `json:"audio,omitempty"`
	Subtitles        string     `json:"subtitles,omitempty"`
	Name             string     `json:"name,omitempty"`
}

// Label is the short name a finding uses for a variant: "1280x720@2500k", or
// "audio@75k" for an audio-only rung.
func (v Variant) Label() string {
	bw := ""
	if v.Bandwidth > 0 {
		bw = fmt.Sprintf("@%dk", v.Bandwidth/1000)
	}
	res := v.Resolution.String()
	if res == "" {
		res = "variant"
		switch {
		case v.Name != "":
			res = v.Name
		case v.AudioOnly():
			res = "audio"
		}
	}
	return res + bw
}

// AudioOnly is true when CODECS names no video codec — such a rung legitimately has
// no RESOLUTION.
func (v Variant) AudioOnly() bool {
	if v.Codecs == "" {
		return false
	}
	for _, c := range strings.Split(v.Codecs, ",") {
		c = strings.ToLower(strings.TrimSpace(c))
		switch {
		case strings.HasPrefix(c, "mp4a"), strings.HasPrefix(c, "ac-3"), strings.HasPrefix(c, "ec-3"), strings.HasPrefix(c, "opus"), strings.HasPrefix(c, "flac"), strings.HasPrefix(c, "alac"):
		default:
			return false
		}
	}
	return true
}

// Rendition is one EXT-X-MEDIA entry (audio, subtitles, closed captions).
type Rendition struct {
	Type       string `json:"type"`
	GroupID    string `json:"group_id"`
	Name       string `json:"name"`
	Language   string `json:"language,omitempty"`
	URI        string `json:"uri,omitempty"`
	Default    bool   `json:"default"`
	Autoselect bool   `json:"autoselect"`
}

// Segment is one media segment of a media playlist.
type Segment struct {
	URI             string    `json:"uri"`
	Sequence        int64     `json:"sequence"`
	Duration        float64   `json:"duration"`
	Title           string    `json:"title,omitempty"`
	Discontinuity   bool      `json:"discontinuity,omitempty"`
	ProgramDateTime time.Time `json:"program_date_time,omitempty"`
	Map             string    `json:"map,omitempty"`
	ByteRange       string    `json:"byte_range,omitempty"`
	Gap             bool      `json:"gap,omitempty"`
}

// Playlist is either a master (Variants) or a media (Segments) playlist.
type Playlist struct {
	Master                bool
	Version               int
	TargetDuration        float64
	MediaSequence         int64
	DiscontinuitySequence int64
	EndList               bool
	PlaylistType          string
	IndependentSegments   bool
	IFramesOnly           bool
	PartTargetDuration    float64
	PartCount             int
	ServerControl         map[string]string
	Segments              []Segment
	Variants              []Variant
	Renditions            []Rendition
	// Tags counts every tag seen, known or not, for diagnostics.
	Tags map[string]int
}

// ErrNotPlaylist is returned when the body does not start with #EXTM3U.
var ErrNotPlaylist = errors.New("not an M3U8 playlist: #EXTM3U missing")

// Live is true for a media playlist without EXT-X-ENDLIST.
func (p *Playlist) Live() bool { return !p.Master && !p.EndList }

// Duration is the sum of the segment durations — the DVR window of a live playlist.
func (p *Playlist) Duration() float64 {
	d := 0.0
	for _, s := range p.Segments {
		d += s.Duration
	}
	return d
}

// MaxSegmentDuration is the longest EXTINF.
func (p *Playlist) MaxSegmentDuration() float64 {
	m := 0.0
	for _, s := range p.Segments {
		if s.Duration > m {
			m = s.Duration
		}
	}
	return m
}

// Discontinuities counts the EXT-X-DISCONTINUITY tags.
func (p *Playlist) Discontinuities() int {
	n := 0
	for _, s := range p.Segments {
		if s.Discontinuity {
			n++
		}
	}
	return n
}

// LastSequence is the media sequence number of the last segment, -1 when empty.
func (p *Playlist) LastSequence() int64 {
	if len(p.Segments) == 0 {
		return -1
	}
	return p.Segments[len(p.Segments)-1].Sequence
}

// Parse reads a playlist. A missing header is the one fatal error; everything else is
// recorded as best it can be.
func Parse(data []byte) (*Playlist, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	p := &Playlist{Tags: map[string]int{}, ServerControl: map[string]string{}}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	started := false
	var pending Segment
	havePending := false
	var pendingVariant *Variant
	currentMap := ""
	seq := int64(0)
	seqSet := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if !started {
			if !strings.HasPrefix(line, "#EXTM3U") {
				return nil, ErrNotPlaylist
			}
			started = true
			continue
		}
		if strings.HasPrefix(line, "#EXT") {
			tag, val, _ := strings.Cut(line, ":")
			p.Tags[tag]++
			switch tag {
			case "#EXT-X-VERSION":
				p.Version, _ = strconv.Atoi(val)
			case "#EXT-X-TARGETDURATION":
				p.TargetDuration, _ = strconv.ParseFloat(val, 64)
			case "#EXT-X-MEDIA-SEQUENCE":
				p.MediaSequence, _ = strconv.ParseInt(val, 10, 64)
				if !seqSet {
					seq = p.MediaSequence
					seqSet = true
				}
			case "#EXT-X-DISCONTINUITY-SEQUENCE":
				p.DiscontinuitySequence, _ = strconv.ParseInt(val, 10, 64)
			case "#EXT-X-ENDLIST":
				p.EndList = true
			case "#EXT-X-PLAYLIST-TYPE":
				p.PlaylistType = strings.ToUpper(val)
			case "#EXT-X-INDEPENDENT-SEGMENTS":
				p.IndependentSegments = true
			case "#EXT-X-I-FRAMES-ONLY":
				p.IFramesOnly = true
			case "#EXT-X-PART-INF":
				p.PartTargetDuration, _ = strconv.ParseFloat(ParseAttributes(val)["PART-TARGET"], 64)
			case "#EXT-X-SERVER-CONTROL":
				p.ServerControl = ParseAttributes(val)
			case "#EXT-X-PART":
				p.PartCount++
			case "#EXTINF":
				d, title, _ := strings.Cut(val, ",")
				pending.Duration, _ = strconv.ParseFloat(strings.TrimSpace(d), 64)
				pending.Title = strings.TrimSpace(title)
				havePending = true
			case "#EXT-X-DISCONTINUITY":
				pending.Discontinuity = true
				havePending = true
			case "#EXT-X-PROGRAM-DATE-TIME":
				if t, err := time.Parse(time.RFC3339Nano, val); err == nil {
					pending.ProgramDateTime = t
				} else if t, err := time.Parse("2006-01-02T15:04:05.999999999Z0700", val); err == nil {
					pending.ProgramDateTime = t
				}
				havePending = true
			case "#EXT-X-BYTERANGE":
				pending.ByteRange = val
				havePending = true
			case "#EXT-X-GAP":
				pending.Gap = true
				havePending = true
			case "#EXT-X-MAP":
				currentMap = ParseAttributes(val)["URI"]
			case "#EXT-X-STREAM-INF":
				p.Master = true
				v := parseVariant(ParseAttributes(val))
				pendingVariant = &v
			case "#EXT-X-I-FRAME-STREAM-INF":
				p.Master = true
			case "#EXT-X-MEDIA":
				p.Master = true
				a := ParseAttributes(val)
				p.Renditions = append(p.Renditions, Rendition{Type: a["TYPE"], GroupID: a["GROUP-ID"], Name: a["NAME"], Language: a["LANGUAGE"], URI: a["URI"], Default: a["DEFAULT"] == "YES", Autoselect: a["AUTOSELECT"] == "YES"})
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		// A URI line.
		switch {
		case pendingVariant != nil:
			pendingVariant.URI = line
			p.Variants = append(p.Variants, *pendingVariant)
			pendingVariant = nil
		case havePending:
			pending.URI = line
			pending.Map = currentMap
			pending.Sequence = seq
			seq++
			p.Segments = append(p.Segments, pending)
			pending = Segment{}
			havePending = false
		default:
			p.Tags["orphan-uri"]++
		}
	}
	if !started {
		return nil, ErrNotPlaylist
	}
	return p, nil
}

func parseVariant(a map[string]string) Variant {
	v := Variant{Codecs: a["CODECS"], Audio: a["AUDIO"], Subtitles: a["SUBTITLES"], Name: a["NAME"]}
	v.Bandwidth, _ = strconv.ParseInt(a["BANDWIDTH"], 10, 64)
	v.AverageBandwidth, _ = strconv.ParseInt(a["AVERAGE-BANDWIDTH"], 10, 64)
	v.FrameRate, _ = strconv.ParseFloat(a["FRAME-RATE"], 64)
	if w, h, ok := strings.Cut(a["RESOLUTION"], "x"); ok {
		v.Resolution.Width, _ = strconv.Atoi(w)
		v.Resolution.Height, _ = strconv.Atoi(h)
	}
	return v
}

// ParseAttributes splits an attribute list (KEY=value,KEY="quoted, value") into a map.
// Quoted values keep their commas; the quotes are removed.
func ParseAttributes(s string) map[string]string {
	out := map[string]string{}
	i := 0
	for i < len(s) {
		eq := strings.IndexByte(s[i:], '=')
		if eq < 0 {
			break
		}
		key := strings.TrimSpace(s[i : i+eq])
		i += eq + 1
		var val string
		if i < len(s) && s[i] == '"' {
			end := strings.IndexByte(s[i+1:], '"')
			if end < 0 {
				val = s[i+1:]
				i = len(s)
			} else {
				val = s[i+1 : i+1+end]
				i += end + 2
			}
			if c := strings.IndexByte(s[i:], ','); c >= 0 {
				i += c + 1
			} else {
				i = len(s)
			}
		} else {
			c := strings.IndexByte(s[i:], ',')
			if c < 0 {
				val = s[i:]
				i = len(s)
			} else {
				val = s[i : i+c]
				i += c + 1
			}
			val = strings.TrimSpace(val)
		}
		if key != "" {
			out[key] = val
		}
	}
	return out
}

// Resolve joins a playlist URL with a segment or variant URI.
func Resolve(base string, ref string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return "", err
	}
	return b.ResolveReference(r).String(), nil
}
