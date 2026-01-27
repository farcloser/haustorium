#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<EOF
Usage: $(basename "$0") [--issue=<check>] <report.jsonl>

Produce a summary digest from a haustorium JSONL report.

Arguments:
  report.jsonl        Path to a haustorium-report.jsonl file

Options:
  --issue=<check>     Show files affected by a specific issue type with details
                      (e.g., --issue=clipping, --issue=noise-floor)
EOF
  exit 1
}

# --- Parse arguments ---
issue_filter=""
report=""

for arg in "$@"; do
  case "$arg" in
    --issue=*) issue_filter="${arg#--issue=}" ;;
    -h|--help) usage ;;
    *)
      if [[ -z "$report" ]]; then
        report="$arg"
      else
        echo "Error: unexpected argument '$arg'" >&2
        usage
      fi
      ;;
  esac
done

if [[ -z "$report" ]]; then
  echo "Error: no report file specified" >&2
  usage
fi

if [[ ! -f "$report" ]]; then
  echo "Error: '$report' not found" >&2
  exit 1
fi

if ! command -v jq &>/dev/null; then
  echo "Error: jq not found in PATH" >&2
  exit 1
fi

# --- Main digest ---
jq -r -s '
  length as $total |
  ([.[] | select(.error)] | length) as $errors |
  ($total - $errors) as $analyzed |
  [.[] | select(.error | not)] as $ok |

  # --- Issue count distribution ---
  [
    $ok[] | .analysis.summary.issue_count // 0
  ] as $counts |
  ($counts | max // 0) as $max_issues |
  [range(0; $max_issues + 1)] |
  map(. as $n | {
    issues: $n,
    count: ([$counts[] | select(. == $n)] | length)
  }) |
  [.[] | select(.count > 0)] as $dist |

  # --- Per-check severity breakdown ---
  [
    $ok[] | .analysis.issues[]? | select(.detected)
  ] as $all_issues |
  ([$all_issues[].check] | unique) as $checks |
  [
    $checks[] | . as $check |
    {
      check: $check,
      total: ([$all_issues[] | select(.check == $check)] | length),
      severe: ([$all_issues[] | select(.check == $check and .severity == "severe")] | length),
      moderate: ([$all_issues[] | select(.check == $check and .severity == "moderate")] | length),
      mild: ([$all_issues[] | select(.check == $check and .severity == "mild")] | length)
    }
  ] | sort_by(-.total) as $breakdown |

  # --- Worst severity distribution ---
  [
    $ok[] | .analysis.summary.worst_severity // "no issue"
  ] as $severities |
  {
    severe: ([$severities[] | select(. == "severe")] | length),
    moderate: ([$severities[] | select(. == "moderate")] | length),
    mild: ([$severities[] | select(. == "mild")] | length),
    clean: ([$severities[] | select(. == "no issue")] | length)
  } as $sev_dist |

  # --- Output ---
  "=== Haustorium Report Digest ===",
  "",
  "Total tracks:  \($total)",
  "Failed:        \($errors)",
  "Analyzed:      \($analyzed)",
  "",
  "--- Worst Severity ---",
  "  Clean:     \($sev_dist.clean)",
  "  Mild:      \($sev_dist.mild)",
  "  Moderate:  \($sev_dist.moderate)",
  "  Severe:    \($sev_dist.severe)",
  "",
  "--- Issues Per Track ---",
  ($dist[] | "  \(.issues) issues:  \(.count) tracks"),
  "",
  "--- Issues By Type ---",
  ($breakdown[] |
    "  \(.check)",
    "    total: \(.total)  severe: \(.severe)  moderate: \(.moderate)  mild: \(.mild)"
  )
' "$report"

# --- Issue detail section ---
if [[ -n "$issue_filter" ]]; then
  echo ""
  jq -r -s --arg check "$issue_filter" '
    # Map check names to analysis keys
    {
      "clipping": "clipping",
      "truncation": "truncation",
      "fake-bit-depth": "bit_depth",
      "fake-sample-rate": "spectral",
      "lossy-transcode": "spectral",
      "dc-offset": "dc_offset",
      "fake-stereo": "stereo",
      "phase-issues": "stereo",
      "inverted-phase": "stereo",
      "channel-imbalance": "stereo",
      "silence-padding": "silence",
      "hum": "spectral",
      "noise-floor": "spectral",
      "inter-sample-peaks": "true_peak",
      "loudness": "loudness",
      "dynamic-range": "loudness",
      "dropouts": "dropouts"
    } as $key_map |

    [.[] | select(.error | not)] |
    [
      .[] | . as $entry |
      ($entry.file // "(redacted)") as $file |
      [$entry.analysis.issues[]? | select(.detected and .check == $check)] |
      if length > 0 then
        .[0] as $issue |
        {
          file: $file,
          severity: $issue.severity,
          summary: $issue.summary,
          confidence: $issue.confidence,
          detail: ($entry.analysis[$key_map[$check]] // null)
        }
      else empty end
    ] |
    if length == 0 then
      "No tracks affected by \($check)"
    else
      sort_by(
        if .severity == "severe" then 0
        elif .severity == "moderate" then 1
        elif .severity == "mild" then 2
        else 3 end
      ) |
      "=== \($check): \(length) tracks ===",
      "",
      (.[] |
        "  \(.file)",
        "    severity: \(.severity)  confidence: \(.confidence)",
        "    \(.summary)",
        if .detail then
          (.detail | to_entries | map("    \(.key): \(.value | if type == "array" then "\(length) entries" else tostring end)") | .[])
        else empty end,
        ""
      )
    end
  ' "$report"
fi
