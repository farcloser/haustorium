#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<EOF
Usage: $(basename "$0") [--redact-path] <folder>

Scan a music collection and write a haustorium report.

Arguments:
  folder          Directory to scan recursively for .flac and .m4a files

Options:
  --redact-path   Strip file paths from the report before writing to disk

Output is written to haustorium-report.txt in the current directory.
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

# --- Collect files ---
mapfile -t files < <(find "$folder" -type f \( -iname "*.flac" -o -iname "*.m4a" \) | sort)

total=${#files[@]}

if [[ "$total" -eq 0 ]]; then
  echo "No .flac or .m4a files found in '$folder'" >&2
  exit 1
fi

echo "Found $total files to analyze"

# --- Run analysis ---
output_file="haustorium-report.txt"
start_time=$(date +%s)
processed=0
failed=0
report=""

for file in "${files[@]}"; do
  processed=$((processed + 1))
  echo "[$processed/$total] $file"

  result=$(haustorium process "$file" 2>&1) || {
    result="File: $file
ERROR: analysis failed
"
    failed=$((failed + 1))
  }

  report+="$result"
  report+=$'\n\n'
done

end_time=$(date +%s)
elapsed=$((end_time - start_time))
minutes=$((elapsed / 60))
seconds=$((elapsed % 60))

# --- Build final report ---
header="Haustorium Report
Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)
Source:     $folder
Files:      $total ($((total - failed)) succeeded, $failed failed)
Duration:   ${minutes}m ${seconds}s
"

full_report="$header
$report"

# --- Redact paths if requested ---
if [[ "$redact" == true ]]; then
  full_report=$(echo "$full_report" | sed '/^File: /d; /^Source: /d')
fi

# --- Write to disk ---
echo "$full_report" > "$output_file"
gzip -k "$output_file"

echo ""
echo "Done: $total files in ${minutes}m ${seconds}s ($failed failed)"
echo "Report written to $output_file (and ${output_file}.gz)"
