# Haustorium

> * a Go audio analysis tool specialized in music defect detection
> * [a rootlike structure that grows into or around another structure to absorb water or nutrients](https://en.wikipedia.org/wiki/Haustorium)

![Haustorium](logo.jpg)

## Purpose

For music lovers who care and who have a local collection, it is essential to be able
to assess the quality of these files.

This ranges from identifying bad files (for example: lossy compression masquerading as lossless),
bad masters (example: brickwalled), bad vinyl rip (example: clipping), and other audible defects.

Further, it is interesting to analyze track properties (are their vocals, peak detection,
frequency response, BPM, key, etc).

Haustorium aims at providing all of that in a Go library, with a simple example binary.

We are prioritizing feature development in go, but might in the future shell out to
dedicated third-party tools for the more complex analysis.

## Installation

You need Go installed on your machine (`brew install go` if you are on macOS).

```bash
go install github.com/farcloser/haustorium/cmd/haustorium@latest
```

## Usage

### Easy

Install ffmpeg.
On macOS `brew install ffmpeg`

```bash
haustorium process mymusicfile
```

If you want to do it on a folder:

```bash
find my_music_folder -type f \( -iname "*.m4a" -o -iname "*.flac" \) -exec haustorium process {} \;
```

### Advanced

You can take care of transcoding yourself (expected `-f s32le -acodec pcm_s32le` by default, but can be overriden) and feed it to `haustorium`,

```bash
./myconverter myfile | haustorium analyze --sample-rate XX --expected-bit-depth YY --bit-depth WW --channels ZZZ -
```

--sample-rate, --expected-bit-depth, --channels is what you have in the original file.

--bit-depth is what you convert to internally.

### Results

```txt

File: /Volumes/Anisotope/gill/1966-El Chico/[1966-US-12_ Vinyl-impulse!-A 9102-]/04-08-This Dream.m4a
Issues found: 2 (worst severity: mild)

   [no issue] [clipping] No clipping detected (confidence: 100%)
   [no issue] [truncation] Clean ending (confidence: 80%)
   [no issue] [fake-bit-depth] Genuine 16-bit (confidence: 100%)
   [no issue] [fake-sample-rate] Genuine 44100 Hz (confidence: 100%)
   [no issue] [lossy-transcode] No lossy transcode detected (confidence: 50%)
   [no issue] [dc-offset] No DC offset (confidence: 100%)
   [no issue] [fake-stereo] Real stereo content (confidence: 100%)
   [no issue] [phase-issues] Mono-compatible (confidence: 100%)
   [no issue] [inverted-phase] Phase polarity OK (confidence: 100%)
!! [mild] [channel-imbalance] Slight imbalance: right louder by 5.0 dB (confidence: 100%)
   [no issue] [silence-padding] No excessive silence padding (confidence: 100%)
   [no issue] [hum] No mains hum detected (confidence: 90%)
!! [mild] [noise-floor] Slightly elevated noise floor (-19.5 dB) (confidence: 85%)
   [no issue] [inter-sample-peaks] No inter-sample peaks (true peak -3.4 dBTP) (confidence: 100%)
   [no issue] [loudness] Loudness: -19.6 LUFS, range 9.8 LU (confidence: 100%)
   [no issue] [dynamic-range] Excellent dynamics (DR15) (confidence: 100%)
   [no issue] [dropouts] No dropouts or glitches (confidence: 90%)

Properties:
  Loudness:          -19.6 LUFS (range: 9.8 LU)
  Dynamic Range:     DR15
  True Peak:         -3.4 dBTP
  Spectral Centroid: 3688 Hz
  Stereo Width:      Wide (correlation: 0.28)
```

If you need help or clarification on interpreting and understanding these results and
why they are bad, refer to the [interpretation document](docs/INTERPRETING.md).

### Optional flags

Vinyls, CDs, live recordings have different tolerance for certain types of defects.

Interpreting a 60s jazz vinyl cannot be done the same way as a modern master on a CD.

You can specify `--source=vinyl`, `--source=digital`, `--source=live` to select
a specific interpretation profile.

### Performance

Expect roughly 2 seconds processing time per file on a reasonable laptop, with a USB SSD drive.

## Known issues and limitations

See [ISSUES](docs/ISSUES.md) for know problems.

## Design

See [DESIGN](docs/DESIGN.md) for the initial research.

## Roadmap

### Cli

Haustorium cli is provided as a convenience, but it is first and foremost
meant to be used as a library in more ambitious / broader tools.

As such, it is unlikely we will spend much time refining the cli output.
If you want fancy output (cute console, json, filtered results), you should be able to very easily
roll your own cli, using haustorium to power it.

However, contributions to the cli are welcome, if they do not introduce large dependencies,
too much code, bloat, or non-generic / specialized use-cases.

### Library

#### Currently missing

- BPM
- Vocal presence
- Key
- Wow / flutter
- Click / pops

These are hard problems, will likely take a significant amount of work,
or accepting a third-party binary dependency.

If you really want this, create a feature request, and get other interested
people to +1 it, so that I can prioritize efforts.

#### Bug fixing / refining results

I am currently testing this on my own collection, which is a single, already biased data-point.

It would be very helpful to get other people to report their results to be able
to investigate false positive / false negatives and refine the detection bands.

