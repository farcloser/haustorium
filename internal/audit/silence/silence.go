package silence

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/farcloser/haustorium/internal/types"
	"github.com/farcloser/primordium/fault"
)

type Options struct {
	ThresholdDb   float64 // below this = silence (default -60)
	MinDurationMs int     // minimum silence to report (default 1000)
	WindowMs      int     // RMS window size (default 50)
}

func DefaultOptions() Options {
	return Options{
		ThresholdDb:   -60.0,
		MinDurationMs: 1000,
		WindowMs:      50,
	}
}

func Detect(r io.Reader, format types.PCMFormat, opts Options) (*types.SilenceResult, error) {
	if opts.ThresholdDb == 0 {
		opts.ThresholdDb = -60.0
	}
	if opts.MinDurationMs == 0 {
		opts.MinDurationMs = 1000
	}
	if opts.WindowMs == 0 {
		opts.WindowMs = 50
	}

	bytesPerSample := int(format.BitDepth / 8)
	frameSize := bytesPerSample * int(format.Channels)
	numChannels := int(format.Channels)

	// Window size in frames
	windowFrames := int(format.SampleRate) * opts.WindowMs / 1000
	if windowFrames < 1 {
		windowFrames = 1
	}
	minSilenceFrames := uint64(format.SampleRate) * uint64(opts.MinDurationMs) / 1000

	buf := make([]byte, frameSize*4096)

	var maxVal float64
	switch format.BitDepth {
	case types.Depth16:
		maxVal = 32768.0
	case types.Depth24:
		maxVal = 8388608.0
	case types.Depth32:
		maxVal = 2147483648.0
	}

	threshold := math.Pow(10, opts.ThresholdDb/20)

	var segments []types.SilenceSegment
	var currentFrame uint64
	var windowSumSq float64
	var windowCount int

	var inSilence bool
	var silenceStart uint64
	var silenceSumSq float64
	var silenceCount uint64

	processWindow := func() {
		if windowCount == 0 {
			return
		}

		rms := math.Sqrt(windowSumSq / float64(windowCount))
		isSilent := rms < threshold

		if isSilent && !inSilence {
			// Entering silence
			inSilence = true
			silenceStart = currentFrame - uint64(windowCount)
			silenceSumSq = windowSumSq
			silenceCount = uint64(windowCount)
		} else if isSilent && inSilence {
			// Continuing silence
			silenceSumSq += windowSumSq
			silenceCount += uint64(windowCount)
		} else if !isSilent && inSilence {
			// Exiting silence
			silenceEnd := currentFrame - uint64(windowCount)
			silenceFrames := silenceEnd - silenceStart

			if silenceFrames >= minSilenceFrames {
				silenceRms := math.Sqrt(silenceSumSq / float64(silenceCount))
				silenceDb := 20 * math.Log10(silenceRms)
				if math.IsInf(silenceDb, -1) {
					silenceDb = -120.0
				}

				segments = append(segments, types.SilenceSegment{
					StartSample: silenceStart,
					EndSample:   silenceEnd,
					StartSec:    float64(silenceStart) / float64(format.SampleRate),
					EndSec:      float64(silenceEnd) / float64(format.SampleRate),
					DurationSec: float64(silenceFrames) / float64(format.SampleRate),
					RmsDb:       silenceDb,
				})
			}
			inSilence = false
		}

		windowSumSq = 0
		windowCount = 0
	}

	for {
		n, err := r.Read(buf)
		if n > 0 {
			completeFrames := (n / frameSize) * frameSize
			data := buf[:completeFrames]

			switch format.BitDepth {
			case types.Depth16:
				for i := 0; i < len(data); i += frameSize {
					var frameSumSq float64
					for ch := 0; ch < numChannels; ch++ {
						sample := float64(int16(binary.LittleEndian.Uint16(data[i+ch*2:]))) / maxVal
						frameSumSq += sample * sample
					}
					windowSumSq += frameSumSq / float64(numChannels)
					windowCount++
					currentFrame++

					if windowCount >= windowFrames {
						processWindow()
					}
				}
			case types.Depth24:
				for i := 0; i < len(data); i += frameSize {
					var frameSumSq float64
					for ch := 0; ch < numChannels; ch++ {
						offset := i + ch*3
						raw := int32(data[offset]) | int32(data[offset+1])<<8 | int32(data[offset+2])<<16
						if raw&0x800000 != 0 {
							raw |= ^0xFFFFFF
						}
						sample := float64(raw) / maxVal
						frameSumSq += sample * sample
					}
					windowSumSq += frameSumSq / float64(numChannels)
					windowCount++
					currentFrame++

					if windowCount >= windowFrames {
						processWindow()
					}
				}
			case types.Depth32:
				for i := 0; i < len(data); i += frameSize {
					var frameSumSq float64
					for ch := 0; ch < numChannels; ch++ {
						sample := float64(int32(binary.LittleEndian.Uint32(data[i+ch*4:]))) / maxVal
						frameSumSq += sample * sample
					}
					windowSumSq += frameSumSq / float64(numChannels)
					windowCount++
					currentFrame++

					if windowCount >= windowFrames {
						processWindow()
					}
				}
			}
		}

		if err == io.EOF {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("%w: %w", fault.ErrReadFailure, err)
		}
	}

	// Process remaining window
	if windowCount > 0 {
		processWindow()
	}

	// Handle trailing silence
	if inSilence {
		silenceFrames := currentFrame - silenceStart
		if silenceFrames >= minSilenceFrames {
			silenceRms := math.Sqrt(silenceSumSq / float64(silenceCount))
			silenceDb := 20 * math.Log10(silenceRms)
			if math.IsInf(silenceDb, -1) {
				silenceDb = -120.0
			}

			segments = append(segments, types.SilenceSegment{
				StartSample: silenceStart,
				EndSample:   currentFrame,
				StartSec:    float64(silenceStart) / float64(format.SampleRate),
				EndSec:      float64(currentFrame) / float64(format.SampleRate),
				DurationSec: float64(silenceFrames) / float64(format.SampleRate),
				RmsDb:       silenceDb,
			})
		}
	}

	// Calculate totals
	var totalSilence float64
	for _, seg := range segments {
		totalSilence += seg.DurationSec
	}

	var leadingSec, trailingSec float64
	totalDuration := float64(currentFrame) / float64(format.SampleRate)

	if len(segments) > 0 {
		if segments[0].StartSample == 0 {
			leadingSec = segments[0].DurationSec
		}
		last := segments[len(segments)-1]
		if last.EndSample == currentFrame {
			trailingSec = last.DurationSec
		}
	}

	return &types.SilenceResult{
		Segments:      segments,
		TotalSilence:  totalSilence,
		LeadingSec:    leadingSec,
		TrailingSec:   trailingSec,
		TotalDuration: totalDuration,
		Frames:        currentFrame,
	}, nil
}
