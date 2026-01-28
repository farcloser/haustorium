package dropout

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/farcloser/primordium/fault"

	"github.com/farcloser/haustorium/internal/audit/shared"
	"github.com/farcloser/haustorium/internal/types"
)

// scannerV2 adds cross-channel correlation to filter out intentional transients.
type scannerV2 struct {
	scanner

	// Per-frame delta candidates (not yet committed as events).
	deltaCandidates []deltaCandidate
}

type deltaCandidate struct {
	channel int
	prev    float64
	cur     float64
	delta   float64
	frame   uint64
}

func newScannerV2(opts Options, sampleRate float64, numChannels int) *scannerV2 {
	return &scannerV2{
		scanner:         *newScanner(opts, sampleRate, numChannels),
		deltaCandidates: make([]deltaCandidate, 0, numChannels),
	}
}

// processSampleV2 runs detection but defers delta events for cross-channel check.
func (s *scannerV2) processSampleV2(channel int, sample float64) {
	if !s.firstSample {
		// Delta detection - store as candidate, don't emit yet.
		delta := math.Abs(sample - s.prevSample[channel])
		if delta > s.opts.DeltaThreshold &&
			isDeltaDropout(s.prevSample[channel], sample, s.opts.DeltaNearZero) {
			s.deltaCandidates = append(s.deltaCandidates, deltaCandidate{
				channel: channel,
				prev:    s.prevSample[channel],
				cur:     sample,
				delta:   delta,
				frame:   s.totalFrames,
			})
		}

		// Zero run detection - same as original.
		if sample == 0 {
			if s.zeroStart[channel] < 0 {
				s.zeroStart[channel] = int64(s.totalFrames) //nolint:gosec // frame count fits in int64
				s.zeroStartRms[channel] = rmsDb(s.sqSum[channel], s.sqFilled[channel])
			}
		} else if s.zeroStart[channel] >= 0 {
			runLength := int64(s.totalFrames) - s.zeroStart[channel] //nolint:gosec // frame count fits in int64
			if runLength >= int64(s.minZeroSamples) && s.zeroStartRms[channel] >= s.opts.ZeroRunQuietDb {
				durationMs := float64(runLength) / s.sampleRate * 1000
				s.result.Events = append(s.result.Events, types.Event{
					Frame:      uint64(s.zeroStart[channel]), //nolint:gosec // value is non-negative by construction
					TimeSec:    float64(s.zeroStart[channel]) / s.sampleRate,
					Channel:    channel,
					Type:       types.EventZeroRun,
					Severity:   float64(runLength) / s.sampleRate,
					DurationMs: durationMs,
				})
				s.result.ZeroRunCount++
			}

			s.zeroStart[channel] = -1
		}
	}

	// DC offset tracking - same as original.
	old := s.dcBuf[channel][s.dcPos[channel]]
	s.dcBuf[channel][s.dcPos[channel]] = sample
	s.dcSum[channel] = s.dcSum[channel] - old + sample

	s.dcPos[channel] = (s.dcPos[channel] + 1) % s.dcWindowSize
	if s.dcFilled[channel] < s.dcWindowSize {
		s.dcFilled[channel]++
	}

	if s.dcFilled[channel] == s.dcWindowSize {
		currentDC := s.dcSum[channel] / float64(s.dcWindowSize)
		if s.dcInitialized[channel] {
			dcDelta := math.Abs(currentDC - s.prevDC[channel])
			if dcDelta > s.opts.DCJumpThreshold {
				s.result.Events = append(s.result.Events, types.Event{
					Frame:    s.totalFrames,
					TimeSec:  float64(s.totalFrames) / s.sampleRate,
					Channel:  channel,
					Type:     types.EventDCJump,
					Severity: dcDelta,
				})
				s.result.DCJumpCount++
			}
		}

		s.prevDC[channel] = currentDC
		s.dcInitialized[channel] = true
	}

	// RMS tracking - same as original.
	oldSq := s.sqBuf[channel][s.sqPos[channel]]
	sq := sample * sample
	s.sqBuf[channel][s.sqPos[channel]] = sq
	s.sqSum[channel] = s.sqSum[channel] - oldSq + sq

	s.sqPos[channel] = (s.sqPos[channel] + 1) % s.dcWindowSize
	if s.sqFilled[channel] < s.dcWindowSize {
		s.sqFilled[channel]++
	}

	s.prevSample[channel] = sample
}

// endFrameV2 processes delta candidates with cross-channel correlation.
func (s *scannerV2) endFrameV2(numChannels int) {
	if len(s.deltaCandidates) > 0 {
		s.processDeltas(numChannels)
		s.deltaCandidates = s.deltaCandidates[:0] // reset for next frame
	}

	s.totalFrames++
	s.firstSample = false
}

// processDeltas checks if delta candidates are correlated across channels.
// If multiple channels have similar deltas at the same frame, it's likely
// intentional (music), not a dropout.
func (s *scannerV2) processDeltas(numChannels int) {
	candidates := s.deltaCandidates

	// Single channel: probably a real dropout.
	if len(candidates) == 1 {
		candidate := candidates[0]
		s.result.Events = append(s.result.Events, types.Event{
			Frame:    candidate.frame,
			TimeSec:  float64(candidate.frame) / s.sampleRate,
			Channel:  candidate.channel,
			Type:     types.EventDelta,
			Severity: candidate.delta,
		})
		s.result.DeltaCount++

		return
	}

	// Multiple channels: check correlation.
	// For stereo, if both channels jump in the same direction with similar magnitude,
	// it's almost certainly intentional music.
	if numChannels == 2 && len(candidates) == 2 {
		candidate0, candidate1 := candidates[0], candidates[1]

		// Same direction? (both positive-going or both negative-going)
		dir0 := candidate0.cur - candidate0.prev
		dir1 := candidate1.cur - candidate1.prev
		sameDirection := (dir0 > 0) == (dir1 > 0)

		// Similar magnitude? (within 50% of each other)
		maxDelta := math.Max(candidate0.delta, candidate1.delta)
		minDelta := math.Min(candidate0.delta, candidate1.delta)
		similarMagnitude := minDelta > maxDelta*0.5

		if sameDirection && similarMagnitude {
			// Correlated transient across both channels = music, not dropout.
			return
		}
	}

	// For >2 channels or uncorrelated stereo: check how many channels are similar.
	// If majority are correlated, discard all. Otherwise emit the outliers.
	if numChannels > 2 {
		// Group by direction.
		positive := make([]deltaCandidate, 0)
		negative := make([]deltaCandidate, 0)

		for _, c := range candidates {
			if c.cur-c.prev > 0 {
				positive = append(positive, c)
			} else {
				negative = append(negative, c)
			}
		}

		// If all or most go same direction, it's music.
		majorityThreshold := (numChannels + 1) / 2
		if len(positive) >= majorityThreshold || len(negative) >= majorityThreshold {
			// Emit only the outliers (minority direction).
			var outliers []deltaCandidate
			if len(positive) < len(negative) {
				outliers = positive
			} else if len(negative) < len(positive) {
				outliers = negative
			}
			// If equal split, no clear outlier - discard all as ambiguous music.

			for _, candidate := range outliers {
				s.result.Events = append(s.result.Events, types.Event{
					Frame:    candidate.frame,
					TimeSec:  float64(candidate.frame) / s.sampleRate,
					Channel:  candidate.channel,
					Type:     types.EventDelta,
					Severity: candidate.delta,
				})
				s.result.DeltaCount++
			}

			return
		}
	}

	// Uncorrelated: emit all as potential dropouts.
	for _, candidate := range candidates {
		s.result.Events = append(s.result.Events, types.Event{
			Frame:    candidate.frame,
			TimeSec:  float64(candidate.frame) / s.sampleRate,
			Channel:  candidate.channel,
			Type:     types.EventDelta,
			Severity: candidate.delta,
		})
		s.result.DeltaCount++
	}
}

// finalizeV2 is identical to finalize but on scannerV2.
func (s *scannerV2) finalizeV2() *types.DropoutResult {
	return s.finalize()
}

func DetectV2(reader io.Reader, format types.PCMFormat, opts Options) (*types.DropoutResult, error) {
	if opts.DeltaThreshold == 0 {
		opts.DeltaThreshold = 0.6
	}

	if opts.DeltaNearZero == 0 {
		opts.DeltaNearZero = 0.01
	}

	if opts.ZeroRunMinMs == 0 {
		opts.ZeroRunMinMs = 1.0
	}

	if opts.ZeroRunQuietDb == 0 {
		opts.ZeroRunQuietDb = -50.0
	}

	if opts.DCWindowMs == 0 {
		opts.DCWindowMs = 50.0
	}

	if opts.DCJumpThreshold == 0 {
		opts.DCJumpThreshold = 0.1
	}

	bytesPerSample := int(format.BitDepth / 8) //nolint:gosec // bit depth and channel count are small constants
	numChannels := int(format.Channels)       //nolint:gosec // bit depth and channel count are small constants
	frameSize := bytesPerSample * numChannels
	sampleRate := float64(format.SampleRate)

	buf := make([]byte, frameSize*4096)

	var maxVal float64

	switch format.BitDepth {
	case types.Depth16:
		maxVal = shared.MaxValue16
	case types.Depth24:
		maxVal = shared.MaxValue24
	case types.Depth32:
		maxVal = shared.MaxValue32
	default:
	}

	scan := newScannerV2(opts, sampleRate, numChannels)

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			completeFrames := (n / frameSize) * frameSize
			data := buf[:completeFrames]

			switch format.BitDepth {
			case types.Depth16:
				for i := 0; i < len(data); i += frameSize {
					for ch := range numChannels {
						sample := float64(int16(binary.LittleEndian.Uint16(data[i+ch*2:]))) / maxVal //nolint:gosec // two's complement conversion for signed PCM samples
						scan.processSampleV2(ch, sample)
					}

					scan.endFrameV2(numChannels)
				}
			case types.Depth24:
				for i := 0; i < len(data); i += frameSize {
					for channel := range numChannels {
						offset := i + channel*3

						raw := int32(data[offset]) | int32(data[offset+1])<<8 | int32(data[offset+2])<<16
						if raw&0x800000 != 0 {
							raw |= ^0xFFFFFF
						}

						sample := float64(raw) / maxVal
						scan.processSampleV2(channel, sample)
					}

					scan.endFrameV2(numChannels)
				}
			case types.Depth32:
				for i := 0; i < len(data); i += frameSize {
					for ch := range numChannels {
						sample := float64(int32(binary.LittleEndian.Uint32(data[i+ch*4:]))) / maxVal //nolint:gosec // two's complement conversion for signed PCM samples
						scan.processSampleV2(ch, sample)
					}

					scan.endFrameV2(numChannels)
				}
			default:
			}
		}

		if err == io.EOF {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("%w: %w", fault.ErrReadFailure, err)
		}
	}

	return scan.finalizeV2(), nil
}
