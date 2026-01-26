package stereo

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/farcloser/primordium/fault"

	"github.com/farcloser/haustorium/internal/types"
)

func Analyze(r io.Reader, format types.PCMFormat) (*types.StereoResult, error) {
	if format.Channels != 2 {
		return &types.StereoResult{
			Correlation:    0,
			DifferenceDb:   0,
			MonoSumDb:      0,
			StereoRmsDb:    0,
			CancellationDb: 0,
			LeftRmsDb:      0,
			RightRmsDb:     0,
			ImbalanceDb:    0,
			Frames:         0,
		}, nil
	}

	bytesPerSample := int(format.BitDepth / 8)
	frameSize := bytesPerSample * 2
	buf := make([]byte, frameSize*4096)

	var sumL, sumR, sumLL, sumRR, sumLR float64
	var sumDiffSq, sumMonoSq, sumStereoSq float64
	var frames uint64

	var maxVal float64
	switch format.BitDepth {
	case types.Depth16:
		maxVal = 32768.0
	case types.Depth24:
		maxVal = 8388608.0
	case types.Depth32:
		maxVal = 2147483648.0
	}

	for {
		n, err := r.Read(buf)
		if n > 0 {
			completeFrames := (n / frameSize) * frameSize
			data := buf[:completeFrames]

			switch format.BitDepth {
			case types.Depth16:
				for i := 0; i < len(data); i += 4 {
					left := float64(int16(binary.LittleEndian.Uint16(data[i:]))) / maxVal
					right := float64(int16(binary.LittleEndian.Uint16(data[i+2:]))) / maxVal

					sumL += left
					sumR += right
					sumLL += left * left
					sumRR += right * right
					sumLR += left * right

					diff := left - right
					sumDiffSq += diff * diff

					mono := (left + right) / 2
					sumMonoSq += mono * mono
					sumStereoSq += (left*left + right*right) / 2
					frames++
				}
			case types.Depth24:
				for i := 0; i < len(data); i += 6 {
					leftRaw := int32(data[i]) | int32(data[i+1])<<8 | int32(data[i+2])<<16
					if leftRaw&0x800000 != 0 {
						leftRaw |= ^0xFFFFFF
					}
					rightRaw := int32(data[i+3]) | int32(data[i+4])<<8 | int32(data[i+5])<<16
					if rightRaw&0x800000 != 0 {
						rightRaw |= ^0xFFFFFF
					}
					left := float64(leftRaw) / maxVal
					right := float64(rightRaw) / maxVal

					sumL += left
					sumR += right
					sumLL += left * left
					sumRR += right * right
					sumLR += left * right

					diff := left - right
					sumDiffSq += diff * diff

					mono := (left + right) / 2
					sumMonoSq += mono * mono
					sumStereoSq += (left*left + right*right) / 2
					frames++
				}
			case types.Depth32:
				for i := 0; i < len(data); i += 8 {
					left := float64(int32(binary.LittleEndian.Uint32(data[i:]))) / maxVal
					right := float64(int32(binary.LittleEndian.Uint32(data[i+4:]))) / maxVal

					sumL += left
					sumR += right
					sumLL += left * left
					sumRR += right * right
					sumLR += left * right

					diff := left - right
					sumDiffSq += diff * diff

					mono := (left + right) / 2
					sumMonoSq += mono * mono
					sumStereoSq += (left*left + right*right) / 2
					frames++
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

	if frames == 0 {
		return &types.StereoResult{
			Correlation:    0,
			DifferenceDb:   -120.0,
			MonoSumDb:      -120.0,
			StereoRmsDb:    -120.0,
			CancellationDb: 0,
			LeftRmsDb:      -120.0,
			RightRmsDb:     -120.0,
			ImbalanceDb:    0,
			Frames:         0,
		}, nil
	}

	n := float64(frames)

	// Pearson correlation
	numerator := n*sumLR - sumL*sumR
	denominator := math.Sqrt((n*sumLL - sumL*sumL) * (n*sumRR - sumR*sumR))

	var correlation float64
	if denominator > 0 {
		correlation = numerator / denominator
	}

	// RMS values
	diffRms := math.Sqrt(sumDiffSq / n)
	monoRms := math.Sqrt(sumMonoSq / n)
	stereoRms := math.Sqrt(sumStereoSq / n)
	leftRms := math.Sqrt(sumLL / n)
	rightRms := math.Sqrt(sumRR / n)

	diffDb := 20 * math.Log10(diffRms)
	monoDb := 20 * math.Log10(monoRms)
	stereoDb := 20 * math.Log10(stereoRms)
	leftDb := 20 * math.Log10(leftRms)
	rightDb := 20 * math.Log10(rightRms)

	if math.IsInf(diffDb, -1) {
		diffDb = -120.0
	}
	if math.IsInf(monoDb, -1) {
		monoDb = -120.0
	}
	if math.IsInf(stereoDb, -1) {
		stereoDb = -120.0
	}
	if math.IsInf(leftDb, -1) {
		leftDb = -120.0
	}
	if math.IsInf(rightDb, -1) {
		rightDb = -120.0
	}

	return &types.StereoResult{
		Correlation:    correlation,
		DifferenceDb:   diffDb,
		MonoSumDb:      monoDb,
		StereoRmsDb:    stereoDb,
		CancellationDb: stereoDb - monoDb,
		LeftRmsDb:      leftDb,
		RightRmsDb:     rightDb,
		ImbalanceDb:    leftDb - rightDb,
		Frames:         frames,
	}, nil
}
