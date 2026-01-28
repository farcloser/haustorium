//nolint:wrapcheck
package main

import (
	"os"

	"github.com/farcloser/primordium/format"

	haustorium "github.com/farcloser/haustorium"
	"github.com/farcloser/haustorium/internal/output"
)

func outputResult(filePath string, result *haustorium.Result, formatName string, verbose bool) error {
	if formatName == "console" {
		printResult(filePath, result, verbose)

		return nil
	}

	formatter, err := format.GetFormatter(formatName)
	if err != nil {
		return err
	}

	data := &format.Data{
		Object: filePath,
		Meta:   output.ResultToMap(result),
	}

	return formatter.PrintAll([]*format.Data{data}, os.Stdout)
}
