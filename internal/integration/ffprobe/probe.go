//nolint:tagliatelle
package ffprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/farcloser/primordium/fault"

	"github.com/farcloser/haustorium/internal/integration/binary"
)

// Result contains the marshalled output of ffprobe.
type Result struct {
	Streams []Stream `json:"streams"`
	Format  Format   `json:"format"`
}

/*

  ┌──────────────┬─────────────────────┬─────────────────┬──────────────────────────────┐
  │    Codec     │ bits_per_raw_sample │ bits_per_sample │            Notes             │
  ├──────────────┼─────────────────────┼─────────────────┼──────────────────────────────┤
  │ FLAC         │ ✅ Yes              │ Often 0         │ Most reliable source         │                                                                                                                                                                                                                                                                                                                     ─
  ├──────────────┼─────────────────────┼─────────────────┼──────────────────────────────┤
  │ ALAC         │ ✅ Usually          │ Sometimes       │                              │
  ├──────────────┼─────────────────────┼─────────────────┼──────────────────────────────┤
  │ WAV/PCM      │ Sometimes           │ ✅ Yes          │ Container reports it         │
  ├──────────────┼─────────────────────┼─────────────────┼──────────────────────────────┤
  │ AIFF         │ Sometimes           │ ✅ Yes          │                              │
  ├──────────────┼─────────────────────┼─────────────────┼──────────────────────────────┤
  │ MP3/AAC/Opus │ ❌ N/A              │ ❌ N/A          │ Lossy - no bit depth concept │
  ├──────────────┼─────────────────────┼─────────────────┼──────────────────────────────┤
  │ DSD          │ ❌ N/A              │ ❌ N/A          │ 1-bit, different paradigm    │
  └──────────────┴─────────────────────┴─────────────────┴──────────────────────────────┘

*/

// BaseStream contains common stream fields.
type BaseStream struct {
	// Tier 1
	Index            int                 `json:"index"`
	CodecName        string              `json:"codec_name"`                    // flac
	CodecType        string              `json:"codec_type"`                    // audio
	SampleRate       string              `json:"sample_rate,omitempty"`         // 44100
	Channels         int                 `json:"channels,omitempty"`            // 2
	ChannelLayout    string              `json:"channel_layout,omitempty"`      // stereo
	Duration         string              `json:"duration,omitempty"`            // 310.666667
	StartTime        string              `json:"start_time,omitempty"`          // 0.00000123
	BitRate          string              `json:"bit_rate,omitempty"`            // 956821
	Tags             map[string][]string `json:"-"`                             // Metadata tags (from dhowden/tag library, not ffprobe)
	BitsPerRawSample string              `json:"bits_per_raw_sample,omitempty"` // see above table - this is shitshow territory
}

// Stream represents the remainder a stream properties.
// These are only marginally useful for our use-case, and not to be displayed to the user.
type Stream struct {
	BaseStream

	// Tier 2
	CodecLongName string `json:"codec_long_name"`           // FLAC (Free Lossless Audio Codec)
	MaxBitRate    string `json:"max_bit_rate,omitempty"`    // only for lossy
	Profile       string `json:"profile,omitempty"`         // codec specific, flac does not have it for eg
	SampleFmt     string `json:"sample_fmt,omitempty"`      // s16 - this is the format that ffmpeg uses internally to represent audio
	BitsPerSample int    `json:"bits_per_sample,omitempty"` // see above table - this is shitshow territory

	// Technical, useful for precise measurements.
	TimeBase   string `json:"time_base"`             // The time unit for all timestamps in this stream. For audio it's typically 1/<sample_rate> (e.g., 1/44100). All PTS/duration values are in these units
	DurationTS int64  `json:"duration_ts,omitempty"` // Duration in TimeBase units (not seconds). To get seconds: DurationTS * TimeBase. E.g., 13660160 with timebase 1/44100 = 309.75 seconds.

	// Tier 3
	// Only meaningful when streams are muxed in containers like MP4, MKV or AVI
	CodecTagString string `json:"codec_tag_string"` // [0][0][0][0]
	CodecTag       string `json:"codec_tag"`        // 0x0000

	// Tier 4
	InitialPadding int    `json:"initial_padding,omitempty"` // Encoder delay - samples added at stream start by lossy codecs (MP3, AAC, Opus). Decoders skip these. FLAC/lossless typically 0.
	StartPts       int64  `json:"start_pts,omitempty"`       // Presentation timestamp of the first frame. Usually 0, but can be non-zero if stream doesn't start at beginning of container.
	NbFrames       string `json:"nb_frames,omitempty"`       // Number of frames in stream. For FLAC, each frame is a block of samples (typically 4096 samples).
	ExtradataSize  int    `json:"extradata_size,omitempty"`  // Size of codec-specific header data (e.g., FLAC's STREAMINFO block, AAC's AudioSpecificConfig).
	// 	Disposition    `json:"disposition"`               // Flags for stream role - default track, dubbed audio,
	// original language, commentary, lyrics, hearing impaired, etc.

	// Video-specific fields.
	// RFrameRate         string `json:"r_frame_rate"`   // Frame rates - meaningful for video, usually "0/0" for audio
	// streams. AvgFrameRate       string `json:"avg_frame_rate"` // Frame rates - meaningful for video, usually "0/0"
	// for audio streams.
	// Width              int    `json:"width,omitempty"`
	// Height             int    `json:"height,omitempty"`
	// CodedWidth         int    `json:"coded_width,omitempty"`
	// CodedHeight        int    `json:"coded_height,omitempty"`
	// ClosedCaptions     int    `json:"closed_captions,omitempty"`
	// FilmGrain          int    `json:"film_grain,omitempty"`
	// HasBFrames         int    `json:"has_b_frames,omitempty"`
	// SampleAspectRatio  string `json:"sample_aspect_ratio,omitempty"`
	// DisplayAspectRatio string `json:"display_aspect_ratio,omitempty"`
	// PixFmt             string `json:"pix_fmt,omitempty"`
	// Level              int    `json:"level,omitempty"`
	// ColorRange         string `json:"color_range,omitempty"`
	// ColorSpace         string `json:"color_space,omitempty"`
	// ColorTransfer      string `json:"color_transfer,omitempty"`
	// ColorPrimaries     string `json:"color_primaries,omitempty"`
	// ChromaLocation     string `json:"chroma_location,omitempty"`
	// FieldOrder         string `json:"field_order,omitempty"`
	// Refs               int    `json:"refs,omitempty"`
	// IsAvc              string `json:"is_avc,omitempty"`
	// NalLengthSize      string `json:"nal_length_size,omitempty"`
}

// Disposition indicates stream disposition flags.
// type Disposition struct {
//	Default         int `json:"default"`
//	Dub             int `json:"dub"`
//	Original        int `json:"original"`
//	Comment         int `json:"comment"`
//	Lyrics          int `json:"lyrics"`
//	Karaoke         int `json:"karaoke"`
//	Forced          int `json:"forced"`
//	HearingImpaired int `json:"hearing_impaired"`
//	VisualImpaired  int `json:"visual_impaired"`
//	CleanEffects    int `json:"clean_effects"`
//	AttachedPic     int `json:"attached_pic"`
//	TimedThumbnails int `json:"timed_thumbnails"`
//	NonDiegetic     int `json:"non_diegetic"`
//	Captions        int `json:"captions"`
//	Descriptions    int `json:"descriptions"`
//	Metadata        int `json:"metadata"`
//	Dependent       int `json:"dependent"`
//	StillImage      int `json:"still_image"`
// }

// BaseFormat contains common format fields for display.
type BaseFormat struct {
	Filename   string `json:"filename"`             // Full path to the file
	NbStreams  int    `json:"nb_streams"`           // Total number of streams (audio + video + subtitle + data)
	FormatName string `json:"format_name"`          // Short container name(s), e.g. "flac", "mov,mp4,m4a,3gp,3g2,mj2"
	StartTime  string `json:"start_time,omitempty"` // Start time of the container in seconds (usually "0.000000")
	Duration   string `json:"duration,omitempty"`   // Total duration in seconds as float string, e.g. "310.666667"
	ProbeScore int    `json:"probe_score"`          // Confidence in format detection (0-100). 100 = certain, lower = guessed.
}

// Format represents container-level information that are not particularly useful.
type Format struct {
	BaseFormat

	BitRate        string `json:"bit_rate,omitempty"` // Overall bitrate in bits/sec (all streams combined), e.g. "956821"
	FormatLongName string `json:"format_long_name"`   // Human-readable format name, e.g. "raw FLAC"
	NbPrograms     int    `json:"nb_programs"`        // Number of programs (for broadcast streams like MPEG-TS). Usually 0 for music files.
	Size           string `json:"size,omitempty"`     // File size in bytes as string, e.g. "37189284"
}

// Probe runs ffprobe on the given file path and returns parsed metadata.
// It requires ffprobe to be available in the system PATH.
func Probe(ctx context.Context, filePath string) (*Result, error) {
	slog.Debug("ffprobe.Probe", "file path", filePath)

	ffprobePath, found := binary.Available(name)
	if !found {
		return nil, fmt.Errorf("%w: %s", fault.ErrMissingRequirements, name)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	//nolint:gosec // filePath is intentionally user-provided input for probing media files
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		filePath,
	)

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	output, err := cmd.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: after %v", fault.ErrTimeout, timeout)
		}

		return nil, fmt.Errorf("%w: %s: %w", fault.ErrCommandFailure, stderr.String(), err)
	}

	var result Result
	if err = json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("%w: %w", fault.ErrInvalidJSON, err)
	}

	return &result, nil
}
