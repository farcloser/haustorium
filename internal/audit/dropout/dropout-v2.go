package dropout

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/farcloser/primordium/fault"

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
func (s *scannerV2) processSampleV2(ch int, sample float64) {
	if !s.firstSample {
		// Delta detection - store as candidate, don't emit yet.
		delta := math.Abs(sample - s.prevSample[ch])
		if delta > s.opts.DeltaThreshold &&
			isDeltaDropout(s.prevSample[ch], sample, s.opts.DeltaNearZero) {
			s.deltaCandidates = append(s.deltaCandidates, deltaCandidate{
				channel: ch,
				prev:    s.prevSample[ch],
				cur:     sample,
				delta:   delta,
				frame:   s.totalFrames,
			})
		}

		// Zero run detection - same as original.
		if sample == 0 {
			if s.zeroStart[ch] < 0 {
				s.zeroStart[ch] = int64(s.totalFrames)
				s.zeroStartRms[ch] = rmsDb(s.sqSum[ch], s.sqFilled[ch])
			}
		} else if s.zeroStart[ch] >= 0 {
			runLength := int64(s.totalFrames) - s.zeroStart[ch]
			if runLength >= int64(s.minZeroSamples) && s.zeroStartRms[ch] >= s.opts.ZeroRunQuietDb {
				durationMs := float64(runLength) / s.sampleRate * 1000
				s.result.Events = append(s.result.Events, types.Event{
					Frame:      uint64(s.zeroStart[ch]),
					TimeSec:    float64(s.zeroStart[ch]) / s.sampleRate,
					Channel:    ch,
					Type:       types.EventZeroRun,
					Severity:   float64(runLength) / s.sampleRate,
					DurationMs: durationMs,
				})
				s.result.ZeroRunCount++
			}

			s.zeroStart[ch] = -1
		}
	}

	// DC offset tracking - same as original.
	old := s.dcBuf[ch][s.dcPos[ch]]
	s.dcBuf[ch][s.dcPos[ch]] = sample
	s.dcSum[ch] = s.dcSum[ch] - old + sample

	s.dcPos[ch] = (s.dcPos[ch] + 1) % s.dcWindowSize
	if s.dcFilled[ch] < s.dcWindowSize {
		s.dcFilled[ch]++
	}

	if s.dcFilled[ch] == s.dcWindowSize {
		currentDC := s.dcSum[ch] / float64(s.dcWindowSize)
		if s.dcInitialized[ch] {
			dcDelta := math.Abs(currentDC - s.prevDC[ch])
			if dcDelta > s.opts.DCJumpThreshold {
				s.result.Events = append(s.result.Events, types.Event{
					Frame:    s.totalFrames,
					TimeSec:  float64(s.totalFrames) / s.sampleRate,
					Channel:  ch,
					Type:     types.EventDCJump,
					Severity: dcDelta,
				})
				s.result.DCJumpCount++
			}
		}

		s.prevDC[ch] = currentDC
		s.dcInitialized[ch] = true
	}

	// RMS tracking - same as original.
	oldSq := s.sqBuf[ch][s.sqPos[ch]]
	sq := sample * sample
	s.sqBuf[ch][s.sqPos[ch]] = sq
	s.sqSum[ch] = s.sqSum[ch] - oldSq + sq

	s.sqPos[ch] = (s.sqPos[ch] + 1) % s.dcWindowSize
	if s.sqFilled[ch] < s.dcWindowSize {
		s.sqFilled[ch]++
	}

	s.prevSample[ch] = sample
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
		c := candidates[0]
		s.result.Events = append(s.result.Events, types.Event{
			Frame:    c.frame,
			TimeSec:  float64(c.frame) / s.sampleRate,
			Channel:  c.channel,
			Type:     types.EventDelta,
			Severity: c.delta,
		})
		s.result.DeltaCount++

		return
	}

	// Multiple channels: check correlation.
	// For stereo, if both channels jump in the same direction with similar magnitude,
	// it's almost certainly intentional music.
	if numChannels == 2 && len(candidates) == 2 {
		c0, c1 := candidates[0], candidates[1]

		// Same direction? (both positive-going or both negative-going)
		dir0 := c0.cur - c0.prev
		dir1 := c1.cur - c1.prev
		sameDirection := (dir0 > 0) == (dir1 > 0)

		// Similar magnitude? (within 50% of each other)
		maxDelta := math.Max(c0.delta, c1.delta)
		minDelta := math.Min(c0.delta, c1.delta)
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

			for _, c := range outliers {
				s.result.Events = append(s.result.Events, types.Event{
					Frame:    c.frame,
					TimeSec:  float64(c.frame) / s.sampleRate,
					Channel:  c.channel,
					Type:     types.EventDelta,
					Severity: c.delta,
				})
				s.result.DeltaCount++
			}

			return
		}
	}

	// Uncorrelated: emit all as potential dropouts.
	for _, c := range candidates {
		s.result.Events = append(s.result.Events, types.Event{
			Frame:    c.frame,
			TimeSec:  float64(c.frame) / s.sampleRate,
			Channel:  c.channel,
			Type:     types.EventDelta,
			Severity: c.delta,
		})
		s.result.DeltaCount++
	}
}

// finalizeV2 is identical to finalize but on scannerV2.
func (s *scannerV2) finalizeV2() *types.DropoutResult {
	return s.finalize()
}

func DetectV2(r io.Reader, format types.PCMFormat, opts Options) (*types.DropoutResult, error) {
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

	bytesPerSample := int(format.BitDepth / 8)
	numChannels := int(format.Channels)
	frameSize := bytesPerSample * numChannels
	sampleRate := float64(format.SampleRate)

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

	s := newScannerV2(opts, sampleRate, numChannels)

	for {
		n, err := r.Read(buf)
		if n > 0 {
			completeFrames := (n / frameSize) * frameSize
			data := buf[:completeFrames]

			switch format.BitDepth {
			case types.Depth16:
				for i := 0; i < len(data); i += frameSize {
					for ch := range numChannels {
						sample := float64(int16(binary.LittleEndian.Uint16(data[i+ch*2:]))) / maxVal
						s.processSampleV2(ch, sample)
					}

					s.endFrameV2(numChannels)
				}
			case types.Depth24:
				for i := 0; i < len(data); i += frameSize {
					for ch := range numChannels {
						offset := i + ch*3

						raw := int32(data[offset]) | int32(data[offset+1])<<8 | int32(data[offset+2])<<16
						if raw&0x800000 != 0 {
							raw |= ^0xFFFFFF
						}

						sample := float64(raw) / maxVal
						s.processSampleV2(ch, sample)
					}

					s.endFrameV2(numChannels)
				}
			case types.Depth32:
				for i := 0; i < len(data); i += frameSize {
					for ch := range numChannels {
						sample := float64(int32(binary.LittleEndian.Uint32(data[i+ch*4:]))) / maxVal
						s.processSampleV2(ch, sample)
					}

					s.endFrameV2(numChannels)
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

	return s.finalizeV2(), nil
}
