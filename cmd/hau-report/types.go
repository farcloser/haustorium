//nolint:tagliatelle
package main

import "encoding/json"

// Record is a single line in the JSONL report file.
type Record struct {
	File       string          `json:"file,omitempty"`
	Analysis   map[string]any  `json:"analysis,omitempty"`
	Probe      json.RawMessage `json:"probe,omitempty"`
	ProbeError string          `json:"probe_error,omitempty"`
	Error      string          `json:"error,omitempty"`
	Timing     *RecordTiming   `json:"timing,omitempty"`
}

// RecordTiming captures per-file processing durations in milliseconds.
type RecordTiming struct {
	ProbeMs   float64 `json:"probe_ms"`
	DecodeMs  float64 `json:"decode_ms"`
	AnalyzeMs float64 `json:"analyze_ms"`
	TotalMs   float64 `json:"total_ms"`
}

// digestRecord holds the typed fields needed by the digest command.
type digestRecord struct {
	File     string          `json:"file,omitempty"`
	Analysis *digestAnalysis `json:"analysis,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type digestAnalysis struct {
	Summary digestSummary `json:"summary"`
	Issues  []digestIssue `json:"issues"`
}

type digestSummary struct {
	IssueCount    int    `json:"issue_count"`
	WorstSeverity string `json:"worst_severity"`
}

type digestIssue struct {
	Check      string  `json:"check"`
	Detected   bool    `json:"detected"`
	Severity   string  `json:"severity"`
	Summary    string  `json:"summary"`
	Confidence float64 `json:"confidence"`
}

// checkBreakdown tracks per-check severity counts for the digest.
type checkBreakdown struct {
	Check    string
	Total    int
	Severe   int
	Moderate int
	Mild     int
}
