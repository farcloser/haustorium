//nolint:staticcheck // too dumb
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

type Options struct {
	DeltaThreshold  float64 // normalized; default 0.6 (60% of full scale jump)
	DeltaNearZero   float64 // at least one side of a delta must be below this; default 0.01
	ZeroRunMinMs    float64 // minimum zero run to report; default 1.0ms
	ZeroRunQuietDb  float64 // RMS below this around a zero run = not a dropout; default -50
	DCWindowMs      float64 // window for DC average; default 50ms
	DCJumpThreshold float64 // DC change threshold; default 0.1
}

func DefaultOptions() Options {
	return Options{
		DeltaThreshold:  0.6,
		DeltaNearZero:   0.01,
		ZeroRunMinMs:    1.0,
		ZeroRunQuietDb:  -50.0,
		DCWindowMs:      50.0,
		DCJumpThreshold: 0.1,
	}
}

// scanner holds all per-channel state for the dropout detector.
type scanner struct {
	opts           Options
	sampleRate     float64
	dcWindowSize   int
	minZeroSamples int
	result         *types.DropoutResult
	totalFrames    uint64
	firstSample    bool

	// Per-channel state.
	prevSample    []float64
	zeroStart     []int64
	zeroStartRms  []float64
	dcBuf         [][]float64
	dcPos         []int
	dcSum         []float64
	dcFilled      []int
	prevDC        []float64
	dcInitialized []bool
	sqBuf         [][]float64
	sqPos         []int
	sqSum         []float64
	sqFilled      []int
}

func newScanner(opts Options, sampleRate float64, numChannels int) *scanner {
	dcWindowSize := max(int(sampleRate*opts.DCWindowMs/1000), 1)
	minZeroSamples := max(int(sampleRate*opts.ZeroRunMinMs/1000), 1)

	scan := &scanner{
		opts:           opts,
		sampleRate:     sampleRate,
		dcWindowSize:   dcWindowSize,
		minZeroSamples: minZeroSamples,
		result:         &types.DropoutResult{},
		firstSample:    true,

		prevSample:    make([]float64, numChannels),
		zeroStart:     make([]int64, numChannels),
		zeroStartRms:  make([]float64, numChannels),
		dcBuf:         make([][]float64, numChannels),
		dcPos:         make([]int, numChannels),
		dcSum:         make([]float64, numChannels),
		dcFilled:      make([]int, numChannels),
		prevDC:        make([]float64, numChannels),
		dcInitialized: make([]bool, numChannels),
		sqBuf:         make([][]float64, numChannels),
		sqPos:         make([]int, numChannels),
		sqSum:         make([]float64, numChannels),
		sqFilled:      make([]int, numChannels),
	}

	for i := range scan.zeroStart {
		scan.zeroStart[i] = -1
	}

	for ch := range numChannels {
		scan.dcBuf[ch] = make([]float64, dcWindowSize)
		scan.sqBuf[ch] = make([]float64, dcWindowSize)
	}

	return scan
}

// processSample runs all detection logic for a single sample on a single channel.
func (s *scanner) processSample(channel int, sample float64) {
	if !s.firstSample {
		// Delta detection.
		delta := math.Abs(sample - s.prevSample[channel])
		if delta > s.opts.DeltaThreshold &&
			isDeltaDropout(s.prevSample[channel], sample, s.opts.DeltaNearZero) {
			s.result.Events = append(s.result.Events, types.Event{
				Frame:    s.totalFrames,
				TimeSec:  float64(s.totalFrames) / s.sampleRate,
				Channel:  channel,
				Type:     types.EventDelta,
				Severity: delta,
			})
			s.result.DeltaCount++
		}

		// Zero run detection.
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

	// DC offset tracking.
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

	// RMS tracking (sum of squares).
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

// endFrame advances the frame counter and clears the first-sample flag.
func (s *scanner) endFrame() {
	s.totalFrames++
	s.firstSample = false
}

// flush emits any trailing zero runs still open at EOF.
func (s *scanner) flush() {
	for channel := range s.zeroStart {
		if s.zeroStart[channel] >= 0 {
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
		}
	}
}

// finalize computes the worst severity and sets the frame count on the result.
func (s *scanner) finalize() *types.DropoutResult {
	s.flush()

	var worstSeverity float64

	for _, e := range s.result.Events {
		if e.Type == types.EventDelta || e.Type == types.EventDCJump {
			if e.Severity > worstSeverity {
				worstSeverity = e.Severity
			}
		}
	}

	if worstSeverity > 0 {
		s.result.WorstDb = 20 * math.Log10(worstSeverity)
	} else {
		s.result.WorstDb = -120
	}

	s.result.Frames = s.totalFrames

	return s.result
}

// rmsDb returns the current RMS level in dB from a running sum-of-squares.
func rmsDb(sqSum float64, sqFilled int) float64 {
	if sqFilled == 0 {
		return -120
	}

	rms := math.Sqrt(sqSum / float64(sqFilled))
	if rms > 0 {
		return 20 * math.Log10(rms)
	}

	return -120
}

// isDeltaDropout returns true if a sample-to-sample jump looks like a real
// dropout rather than a normal musical transient. A dropout transitions
// between audible content and near-silence, so at least one of the two
// samples flanking the jump must be near zero.
func isDeltaDropout(prev, cur, nearZero float64) bool {
	return math.Abs(prev) < nearZero || math.Abs(cur) < nearZero
}

func Detect(r io.Reader, format types.PCMFormat, opts Options) (*types.DropoutResult, error) {
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
	numChannels := int(format.Channels)        //nolint:gosec // bit depth and channel count are small constants
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

	scan := newScanner(opts, sampleRate, numChannels)

	for {
		n, err := r.Read(buf)
		if n > 0 {
			completeFrames := (n / frameSize) * frameSize
			data := buf[:completeFrames]

			switch format.BitDepth {
			case types.Depth16:
				for i := 0; i < len(data); i += frameSize {
					for ch := range numChannels {
						sample := float64(int16(binary.LittleEndian.Uint16(data[i+ch*2:]))) / maxVal //nolint:gosec // two's complement conversion for signed PCM samples
						scan.processSample(ch, sample)
					}

					scan.endFrame()
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
						scan.processSample(channel, sample)
					}

					scan.endFrame()
				}
			case types.Depth32:
				for i := 0; i < len(data); i += frameSize {
					for ch := range numChannels {
						sample := float64(int32(binary.LittleEndian.Uint32(data[i+ch*4:]))) / maxVal //nolint:gosec // two's complement conversion for signed PCM samples
						scan.processSample(ch, sample)
					}

					scan.endFrame()
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

	return scan.finalize(), nil
}
