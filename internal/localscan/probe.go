package localscan

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// probeResult is what ffprobe tells about one audio file.
type probeResult struct {
	tags          map[string]string // keys in lower case
	duration      time.Duration
	codec         string
	sampleRate    int
	bits          int // 0 for lossy formats
	kbps          int // lossy: bitrate (average for VBR)
	avgKbps       int // the file's average bitrate (with tags and pictures)
	vbr           bool
	embeddedCover bool
}

func findProgram(name string) (string, error) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", ErrNoFFprobe
	}
	return p, nil
}

type ffprobeOutput struct {
	Format struct {
		Duration string            `json:"duration"`
		BitRate  string            `json:"bit_rate"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		CodecType        string            `json:"codec_type"`
		CodecName        string            `json:"codec_name"`
		SampleRate       string            `json:"sample_rate"`
		BitsPerRawSample string            `json:"bits_per_raw_sample"`
		BitsPerSample    int               `json:"bits_per_sample"`
		BitRate          string            `json:"bit_rate"`
		Tags             map[string]string `json:"tags"`
		Disposition      struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
}

// runFFprobe reads one file with ffprobe.
func runFFprobe(ctx context.Context, ffprobe, path string) (probeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// stdin/stderr stay nil (/dev/null), so that ffprobe never touches the
	// terminal of the TUI.
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", "--", path) //nolint:gosec // our paths
	out, err := cmd.Output()
	if err != nil {
		return probeResult{}, fmt.Errorf("ffprobe %s: %w", path, err)
	}
	return parseProbe(out)
}

// lossless are the codecs whose bit depth means something.
var lossless = map[string]bool{
	"flac": true, "alac": true, "wavpack": true, "ape": true, "tta": true, "mlp": true, "truehd": true,
}

func parseProbe(data []byte) (probeResult, error) {
	var o ffprobeOutput
	if err := json.Unmarshal(data, &o); err != nil {
		return probeResult{}, err
	}
	pr := probeResult{tags: map[string]string{}}
	add := func(tags map[string]string) {
		for k, v := range tags {
			k = strings.ToLower(k)
			if _, ok := pr.tags[k]; !ok && strings.TrimSpace(v) != "" {
				pr.tags[k] = strings.TrimSpace(v)
			}
		}
	}
	add(o.Format.Tags)
	audio := false
	for _, s := range o.Streams {
		switch {
		case s.CodecType == "audio" && !audio:
			audio = true
			pr.codec = s.CodecName
			pr.sampleRate, _ = strconv.Atoi(s.SampleRate)
			bits, _ := strconv.Atoi(s.BitsPerRawSample)
			if bits == 0 {
				bits = s.BitsPerSample
			}
			if lossless[s.CodecName] || strings.HasPrefix(s.CodecName, "pcm_") || strings.HasPrefix(s.CodecName, "dsd_") {
				pr.bits = bits
			} else {
				// Lossy: the stream bitrate, else the file's average.
				br, _ := strconv.Atoi(s.BitRate)
				if br == 0 {
					br, _ = strconv.Atoi(o.Format.BitRate)
				}
				pr.kbps = (br + 500) / 1000
				avg, _ := strconv.Atoi(o.Format.BitRate)
				pr.avgKbps = (avg + 500) / 1000
				pr.vbr = s.CodecName == "opus" || s.CodecName == "vorbis" // always variable in practice
			}
			add(s.Tags) // Ogg/Opus keep the tags on the stream
		case s.CodecType == "video" && s.Disposition.AttachedPic == 1:
			pr.embeddedCover = true
		}
	}
	if !audio {
		return probeResult{}, fmt.Errorf("no audio stream")
	}
	if d, err := strconv.ParseFloat(o.Format.Duration, 64); err == nil {
		pr.duration = time.Duration(d * float64(time.Second))
	}
	return pr, nil
}

// first returns the first non-empty tag of keys.
func first(tags map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := tags[k]; v != "" {
			return v
		}
	}
	return ""
}

// numberPair parses "3", "3/10", or "03" into 3 and 10.
func numberPair(s string) (n, total int) {
	a, b, _ := strings.Cut(strings.TrimSpace(s), "/")
	n, _ = strconv.Atoi(strings.TrimSpace(a))
	total, _ = strconv.Atoi(strings.TrimSpace(b))
	return n, total
}
