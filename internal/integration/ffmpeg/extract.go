package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"

	"github.com/farcloser/primordium/fault"

	"github.com/farcloser/haustorium/internal/integration/binary"
	"github.com/farcloser/haustorium/internal/types"
)

// ExtractStream extracts a specific audio stream from a container.
func ExtractStream(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
	streamIndex int,
	format *types.PCMFormat,
) error {
	slog.Debug("ffmpeg.ExtractStream", "stream index", streamIndex, "stage", "start")

	ffmpegPath, found := binary.Available(name)
	if !found {
		return fmt.Errorf("%w: %s", fault.ErrMissingRequirements, name)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-i", "-",
		"-map", "0:a:"+strconv.Itoa(streamIndex),
		"-f", bitDepthToSpec(format.BitDepth),
		"-acodec", codec,
		"-v", "quiet",
		"-",
	)

	cmd.Stdout = output
	cmd.Stdin = input

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			slog.Debug("ffmpeg.ExtractStream", "stream index", streamIndex, "stage", "timeout")

			return fmt.Errorf("%w: after %v", fault.ErrTimeout, timeout)
		}

		slog.Debug("ffmpeg.ExtractStream", "stream index", streamIndex, "stage", "error")

		return fmt.Errorf("%w: %s: %w", fault.ErrCommandFailure, stderr.String(), err)
	}

	return nil
}
