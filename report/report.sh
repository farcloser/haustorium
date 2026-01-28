#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<EOF
Usage: $(basename "$0") [--redact-path] <folder>

Scan a music collection and write a haustorium JSON report.

Each file produces one JSON object per line (JSONL format).

Arguments:
  folder          Directory to scan recursively for .flac and .m4a files

Options:
  --redact-path   Strip file paths from the report before writing to disk

Output is written to haustorium-report.jsonl in the current directory.
EOF
  exit 1
}

# --- Parse arguments ---
redact=false
folder=""

for arg in "$@"; do
  case "$arg" in
    --redact-path) redact=true ;;
    -h|--help)     usage ;;
    *)
      if [[ -z "$folder" ]]; then
        folder="$arg"
      else
        echo "Error: unexpected argument '$arg'" >&2
        usage
      fi
      ;;
  esac
done

if [[ -z "$folder" ]]; then
  echo "Error: no folder specified" >&2
  usage
fi

if [[ ! -d "$folder" ]]; then
  echo "Error: '$folder' is not a directory" >&2
  exit 1
fi

# --- Check dependencies ---
if ! command -v haustorium &>/dev/null; then
  echo "Error: haustorium not found in PATH" >&2
  echo "Install with: go install github.com/farcloser/haustorium/cmd/haustorium@latest" >&2
  exit 1
fi

if ! command -v ffmpeg &>/dev/null; then
  echo "Error: ffmpeg not found in PATH" >&2
  echo "Install with: brew install ffmpeg (macOS) or apt install ffmpeg (Linux)" >&2
  exit 1
fi

if ! command -v ffprobe &>/dev/null; then
  echo "Error: ffprobe not found in PATH" >&2
  echo "Install with: brew install ffmpeg (macOS) or apt install ffmpeg (Linux)" >&2
  exit 1
fi

if ! command -v jq &>/dev/null; then
  echo "Error: jq not found in PATH" >&2
  echo "Install with: brew install jq (macOS) or apt install jq (Linux)" >&2
  exit 1
fi

# --- Collect files ---
mapfile -t files < <(find "$folder" -type f \( -iname "*.flac" -o -iname "*.m4a" \) | sort)

total=${#files[@]}

if [[ "$total" -eq 0 ]]; then
  echo "No .flac or .m4a files found in '$folder'" >&2
  exit 1
fi

echo "Found $total files to analyze"

# --- Run analysis ---
output_file="haustorium-report.jsonl"
start_time=$(date +%s)
processed=0
failed=0

# jq redaction filter
if [[ "$redact" == true ]]; then
  jq_redact='del(.file, .probe?.format?.filename?)'
else
  jq_redact='.'
fi

: > "$output_file"

for file in "${files[@]}"; do
  processed=$((processed + 1))
  echo "[$processed/$total] $file"

  # Detect source type from directory path
  source_flag=()
  dir=$(dirname "$file")
  if [[ "$dir" == *[Vv]inyl* ]]; then
    source_flag=(--source vinyl)
  fi

  # Get haustorium analysis as JSON
  if haus_json=$(haustorium process --format json "${source_flag[@]}" "$file" 2>/dev/null); then
    # Get ffprobe metadata as JSON
    if probe_json=$(ffprobe -v quiet -print_format json -show_format -show_streams "$file" 2>/dev/null \
        | jq 'del(.format.tags, .streams[]?.tags, .streams[]?.disposition)'); then
      # Merge haustorium analysis + ffprobe — pipe via stdin to avoid ARG_MAX
      { echo "$haus_json"; echo "$probe_json"; } \
        | jq -n -c --arg file "$file" \
          '(input | .[0].meta) as $analysis | input as $probe | {file: $file, analysis: $analysis, probe: $probe}' \
        | jq -c "$jq_redact" >> "$output_file"
    else
      # Haustorium succeeded but ffprobe failed
      echo "$haus_json" \
        | jq -n -c --arg file "$file" \
          '(input | .[0].meta) as $analysis | {file: $file, analysis: $analysis, probe_error: "ffprobe failed"}' \
        | jq -c "$jq_redact" >> "$output_file"
    fi
  else
    # Haustorium failed
    jq -n -c --arg file "$file" --arg error "analysis failed" \
      '{file: $file, error: $error}' \
      | jq -c "$jq_redact" >> "$output_file"
    failed=$((failed + 1))
  fi
done

end_time=$(date +%s)
elapsed=$((end_time - start_time))
minutes=$((elapsed / 60))
seconds=$((elapsed % 60))

gzip -kf "$output_file"

echo ""
echo "Done: $total files in ${minutes}m ${seconds}s ($failed failed)"
echo "Report written to $output_file (and ${output_file}.gz)"

# Print digest summary to stderr
script_dir="$(cd "$(dirname "$0")" && pwd)"
"${script_dir}/digest.sh" "$output_file" >&2
