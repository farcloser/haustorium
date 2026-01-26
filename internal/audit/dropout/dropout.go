package dropout

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/farcloser/haustorium/internal/types"
	"github.com/farcloser/primordium/fault"
)

type Options struct {
	DeltaThreshold  float64 // normalized; default 0.5 (50% of full scale jump)
	ZeroRunMinMs    float64 // minimum zero run to report; default 1.0ms
	DCWindowMs      float64 // window for DC average; default 50ms
	DCJumpThreshold float64 // DC change threshold; default 0.1
}

func DefaultOptions() Options {
	return Options{
		DeltaThreshold:  0.5,
		ZeroRunMinMs:    1.0,
		DCWindowMs:      50.0,
		DCJumpThreshold: 0.1,
	}
}

func Detect(r io.Reader, format types.PCMFormat, opts Options) (*types.DropoutResult, error) {
	if opts.DeltaThreshold == 0 {
		opts.DeltaThreshold = 0.5
	}
	if opts.ZeroRunMinMs == 0 {
		opts.ZeroRunMinMs = 1.0
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

	// Per-channel state
	prevSample := make([]float64, numChannels)
	zeroStart := make([]int64, numChannels) // frame where zero run started (-1 = not in run)
	for i := range zeroStart {
		zeroStart[i] = -1
	}

	// DC offset tracking (simple moving average)
	dcWindowSize := int(sampleRate * opts.DCWindowMs / 1000)
	if dcWindowSize < 1 {
		dcWindowSize = 1
	}
	dcBuf := make([][]float64, numChannels)
	dcPos := make([]int, numChannels)
	dcSum := make([]float64, numChannels)
	dcFilled := make([]int, numChannels)
	prevDC := make([]float64, numChannels)
	dcInitialized := make([]bool, numChannels)

	for ch := 0; ch < numChannels; ch++ {
		dcBuf[ch] = make([]float64, dcWindowSize)
	}

	minZeroSamples := int(sampleRate * opts.ZeroRunMinMs / 1000)
	if minZeroSamples < 1 {
		minZeroSamples = 1
	}

	result := &types.DropoutResult{}
	var totalFrames uint64
	var firstSample = true

	for {
		n, err := r.Read(buf)
		if n > 0 {
			completeFrames := (n / frameSize) * frameSize
			data := buf[:completeFrames]

			switch format.BitDepth {
			case types.Depth16:
				for i := 0; i < len(data); i += frameSize {
					for ch := 0; ch < numChannels; ch++ {
						sample := float64(int16(binary.LittleEndian.Uint16(data[i+ch*2:]))) / maxVal

						if !firstSample {
							// Delta detection
							delta := math.Abs(sample - prevSample[ch])
							if delta > opts.DeltaThreshold {
								result.Events = append(result.Events, types.Event{
									Frame:    totalFrames,
									TimeSec:  float64(totalFrames) / sampleRate,
									Channel:  ch,
									Type:     types.EventDelta,
									Severity: delta,
								})
								result.DeltaCount++
							}

							// Zero run detection
							if sample == 0 {
								if zeroStart[ch] < 0 {
									zeroStart[ch] = int64(totalFrames)
								}
							} else {
								if zeroStart[ch] >= 0 {
									runLength := int64(totalFrames) - zeroStart[ch]
									if runLength >= int64(minZeroSamples) {
										durationMs := float64(runLength) / sampleRate * 1000
										result.Events = append(result.Events, types.Event{
											Frame:      uint64(zeroStart[ch]),
											TimeSec:    float64(zeroStart[ch]) / sampleRate,
											Channel:    ch,
											Type:       types.EventZeroRun,
											Severity:   float64(runLength) / sampleRate, // duration in seconds
											DurationMs: durationMs,
										})
										result.ZeroRunCount++
									}
									zeroStart[ch] = -1
								}
							}
						}

						// DC offset tracking
						old := dcBuf[ch][dcPos[ch]]
						dcBuf[ch][dcPos[ch]] = sample
						dcSum[ch] = dcSum[ch] - old + sample
						dcPos[ch] = (dcPos[ch] + 1) % dcWindowSize
						if dcFilled[ch] < dcWindowSize {
							dcFilled[ch]++
						}

						if dcFilled[ch] == dcWindowSize {
							currentDC := dcSum[ch] / float64(dcWindowSize)
							if dcInitialized[ch] {
								dcDelta := math.Abs(currentDC - prevDC[ch])
								if dcDelta > opts.DCJumpThreshold {
									result.Events = append(result.Events, types.Event{
										Frame:    totalFrames,
										TimeSec:  float64(totalFrames) / sampleRate,
										Channel:  ch,
										Type:     types.EventDCJump,
										Severity: dcDelta,
									})
									result.DCJumpCount++
								}
							}
							prevDC[ch] = currentDC
							dcInitialized[ch] = true
						}

						prevSample[ch] = sample
					}
					totalFrames++
					firstSample = false
				}
			case types.Depth24:
				for i := 0; i < len(data); i += frameSize {
					for ch := 0; ch < numChannels; ch++ {
						offset := i + ch*3
						raw := int32(data[offset]) | int32(data[offset+1])<<8 | int32(data[offset+2])<<16
						if raw&0x800000 != 0 {
							raw |= ^0xFFFFFF
						}
						sample := float64(raw) / maxVal

						if !firstSample {
							delta := math.Abs(sample - prevSample[ch])
							if delta > opts.DeltaThreshold {
								result.Events = append(result.Events, types.Event{
									Frame:    totalFrames,
									TimeSec:  float64(totalFrames) / sampleRate,
									Channel:  ch,
									Type:     types.EventDelta,
									Severity: delta,
								})
								result.DeltaCount++
							}

							if sample == 0 {
								if zeroStart[ch] < 0 {
									zeroStart[ch] = int64(totalFrames)
								}
							} else {
								if zeroStart[ch] >= 0 {
									runLength := int64(totalFrames) - zeroStart[ch]
									if runLength >= int64(minZeroSamples) {
										durationMs := float64(runLength) / sampleRate * 1000
										result.Events = append(result.Events, types.Event{
											Frame:      uint64(zeroStart[ch]),
											TimeSec:    float64(zeroStart[ch]) / sampleRate,
											Channel:    ch,
											Type:       types.EventZeroRun,
											Severity:   float64(runLength) / sampleRate,
											DurationMs: durationMs,
										})
										result.ZeroRunCount++
									}
									zeroStart[ch] = -1
								}
							}
						}

						old := dcBuf[ch][dcPos[ch]]
						dcBuf[ch][dcPos[ch]] = sample
						dcSum[ch] = dcSum[ch] - old + sample
						dcPos[ch] = (dcPos[ch] + 1) % dcWindowSize
						if dcFilled[ch] < dcWindowSize {
							dcFilled[ch]++
						}

						if dcFilled[ch] == dcWindowSize {
							currentDC := dcSum[ch] / float64(dcWindowSize)
							if dcInitialized[ch] {
								dcDelta := math.Abs(currentDC - prevDC[ch])
								if dcDelta > opts.DCJumpThreshold {
									result.Events = append(result.Events, types.Event{
										Frame:    totalFrames,
										TimeSec:  float64(totalFrames) / sampleRate,
										Channel:  ch,
										Type:     types.EventDCJump,
										Severity: dcDelta,
									})
									result.DCJumpCount++
								}
							}
							prevDC[ch] = currentDC
							dcInitialized[ch] = true
						}

						prevSample[ch] = sample
					}
					totalFrames++
					firstSample = false
				}
			case types.Depth32:
				for i := 0; i < len(data); i += frameSize {
					for ch := 0; ch < numChannels; ch++ {
						sample := float64(int32(binary.LittleEndian.Uint32(data[i+ch*4:]))) / maxVal

						if !firstSample {
							delta := math.Abs(sample - prevSample[ch])
							if delta > opts.DeltaThreshold {
								result.Events = append(result.Events, types.Event{
									Frame:    totalFrames,
									TimeSec:  float64(totalFrames) / sampleRate,
									Channel:  ch,
									Type:     types.EventDelta,
									Severity: delta,
								})
								result.DeltaCount++
							}

							if sample == 0 {
								if zeroStart[ch] < 0 {
									zeroStart[ch] = int64(totalFrames)
								}
							} else {
								if zeroStart[ch] >= 0 {
									runLength := int64(totalFrames) - zeroStart[ch]
									if runLength >= int64(minZeroSamples) {
										durationMs := float64(runLength) / sampleRate * 1000
										result.Events = append(result.Events, types.Event{
											Frame:      uint64(zeroStart[ch]),
											TimeSec:    float64(zeroStart[ch]) / sampleRate,
											Channel:    ch,
											Type:       types.EventZeroRun,
											Severity:   float64(runLength) / sampleRate,
											DurationMs: durationMs,
										})
										result.ZeroRunCount++
									}
									zeroStart[ch] = -1
								}
							}
						}

						old := dcBuf[ch][dcPos[ch]]
						dcBuf[ch][dcPos[ch]] = sample
						dcSum[ch] = dcSum[ch] - old + sample
						dcPos[ch] = (dcPos[ch] + 1) % dcWindowSize
						if dcFilled[ch] < dcWindowSize {
							dcFilled[ch]++
						}

						if dcFilled[ch] == dcWindowSize {
							currentDC := dcSum[ch] / float64(dcWindowSize)
							if dcInitialized[ch] {
								dcDelta := math.Abs(currentDC - prevDC[ch])
								if dcDelta > opts.DCJumpThreshold {
									result.Events = append(result.Events, types.Event{
										Frame:    totalFrames,
										TimeSec:  float64(totalFrames) / sampleRate,
										Channel:  ch,
										Type:     types.EventDCJump,
										Severity: dcDelta,
									})
									result.DCJumpCount++
								}
							}
							prevDC[ch] = currentDC
							dcInitialized[ch] = true
						}

						prevSample[ch] = sample
					}
					totalFrames++
					firstSample = false
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

	// Flush any trailing zero runs
	for ch := 0; ch < numChannels; ch++ {
		if zeroStart[ch] >= 0 {
			runLength := int64(totalFrames) - zeroStart[ch]
			if runLength >= int64(minZeroSamples) {
				durationMs := float64(runLength) / sampleRate * 1000
				result.Events = append(result.Events, types.Event{
					Frame:      uint64(zeroStart[ch]),
					TimeSec:    float64(zeroStart[ch]) / sampleRate,
					Channel:    ch,
					Type:       types.EventZeroRun,
					Severity:   float64(runLength) / sampleRate,
					DurationMs: durationMs,
				})
				result.ZeroRunCount++
			}
		}
	}

	// Find worst severity
	var worstSeverity float64
	for _, e := range result.Events {
		if e.Type == types.EventDelta || e.Type == types.EventDCJump {
			if e.Severity > worstSeverity {
				worstSeverity = e.Severity
			}
		}
	}
	if worstSeverity > 0 {
		result.WorstDb = 20 * math.Log10(worstSeverity)
	} else {
		result.WorstDb = -120
	}

	result.Frames = totalFrames

	return result, nil
}
