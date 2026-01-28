# Known Issues and Limitations

## Transcode Detection False Positives on Legitimate Masters

**Status:** Resolved (2026-01-28)
**Severity:** Moderate
**Discovered:** 2026-01-28

### Problem

The `lossy-transcode` check was flagging files with a ~20-20.5 kHz brickwall cutoff as "likely Opus 128" with 95% confidence. However, this cutoff frequency is ambiguous:

1. **Lossy codecs** (Opus 128, AAC 256) cut around 20 kHz
2. **Legitimate CD masters** often apply a low-pass filter at 20-21 kHz as standard mastering practice

This resulted in false positives on professionally mastered CDs.

### Case Study: Pat Metheny Group - "The Way Up" (Nonesuch, 2005)

- **Mastering:** Ted Jensen at Sterling Sound (world-class engineer)
- **Format:** CD rip, 16-bit/44.1kHz WAV
- **Before fix:** `[severe] lossy-transcode: Lossy transcode detected: likely Opus 128 (cutoff 20500 Hz) (95% confidence)`
- **After fix:** `[no issue] No lossy transcode detected (100% confidence)`

Spectral analysis confirmed a sharp brickwall at 20.5 kHz:

```
Frequency    Energy (dB)    Drop rate
19000 Hz     -46.7 dB       gradual
20000 Hz     -51.0 dB       steepening
20500 Hz     -54.5 dB       steep
21000 Hz     -60.3 dB       very steep
21500 Hz     -70.9 dB       brickwall
22000 Hz     -94.5 dB       noise floor
```

The rolloff from 20.5→21.5 kHz is ~16 dB/500Hz (~32 dB/octave), which is steep but **not necessarily indicative of lossy encoding**.

### Root Cause

Many professional mastering engineers apply a steep low-pass filter around 20 kHz because:

- Human hearing limit is ~20 kHz (often lower with age)
- Removes ultrasonic content that serves no musical purpose
- Prevents intermodulation distortion on some playback systems
- Provides headroom before the 22.05 kHz Nyquist limit
- Was especially common practice in mid-2000s mastering

The original detection algorithm only considered **cutoff frequency**, which cannot distinguish between a legitimate mastering low-pass filter and a high-bitrate lossy codec.

### Solution Implemented

Enhanced transcode detection (`detectTranscodeV2`) with multi-factor confidence adjustment:

#### New Fields in `SpectralResult`

| Field | Type | Description |
|-------|------|-------------|
| `TranscodeConfidence` | float64 | 0.0-1.0 confidence score after all checks |
| `CutoffConsistency` | float64 | Stddev of cutoff frequency across windows (Hz) |
| `HasUltrasonicContent` | bool | Whether any content exists above the detected cutoff |

#### Confidence Adjustments

| Check | Weight | Rationale |
|-------|--------|-----------|
| Ultrasonic content present | -0.40 | **Strongest signal.** Codecs completely eliminate everything above their cutoff. Any content above = mastering filter, not codec. |
| Cutoff ≥20 kHz | -0.10 to -0.20 | High cutoffs more likely to be mastering decisions than codec artifacts. |
| Cutoff consistency <50 Hz stddev | -0.00 to -0.20 | Very stable cutoff frequency suggests deliberate mastering filter. |
| Sharpness <40 dB/octave | -0.10 | Moderate rolloff more consistent with mastering filters. |

#### Detection Threshold

If confidence drops below **0.50**, the transcode flag is removed.

#### Case Study Results

Pat Metheny "The Way Up" analysis:

```
transcode_confidence: 0.44 (below threshold)
is_transcode: false
has_ultrasonic_content: true  ← decisive factor
cutoff_consistency_hz: 1120.66
transcode_cutoff: 20500
transcode_sharpness: 180.25
```

The presence of ultrasonic content above the 20.5 kHz cutoff was the decisive factor. Lossy codecs create a hard wall with absolutely nothing above; this file has content, therefore it's a mastering filter.

### Remaining Proposed Improvements

#### Pre-Echo Detection (Medium priority)

Lossy codecs using MDCT transforms create temporal smearing artifacts:

- Energy appears 2-10ms **before** sharp transients
- Caused by transform windowing
- Not present in legitimate PCM masters

**Implementation:**
- Detect sharp transients in the signal
- Analyze energy in the 2-10ms window preceding each transient
- Flag if pre-echo energy exceeds threshold

**Effort:** ~1-2 days

#### Spectral Hole Detection (Medium priority)

Lossy codecs remove content deemed "inaudible" by psychoacoustic models:

- Creates sudden dips/holes in mid-frequency spectrum
- Pattern differs from natural audio
- More pronounced at lower bitrates

**Implementation:**
- Analyze spectral continuity in 1-16 kHz range
- Flag unnatural gaps that don't correlate with musical content

**Effort:** ~1-2 days

#### Noise Floor Shape Analysis (Low priority)

Lossy codecs add quantization noise with characteristic spectral shapes:

- Differs from PCM dither (TPDF, shaped dither)
- Codec-specific patterns

**Implementation:**
- Analyze noise floor during quiet passages
- Compare to known codec noise profiles
- Requires database of codec signatures

**Effort:** ~1 week

### Additional Consideration: Bootleg CDs

Note that a "CD rip" only proves audio came from a CD, not that the CD was legitimate. Counterfeit/bootleg CDs are often burned from lossy sources and would correctly show transcode artifacts. The detection is working correctly in those cases - if there's no ultrasonic content, it likely IS a transcode regardless of the physical medium.

---

## Inter-Sample Peak (ISP) Detection Severity Weighting

**Status:** Partially Resolved (2026-01-28)
**Severity:** Moderate
**Discovered:** 2026-01-28

### Problem

The original ISP detection counted all inter-sample peaks equally and reported only:
- Total ISP count
- Maximum overshoot (dBTP)

This was misleading because **not all ISPs are equally problematic**. The severity depends on:
1. **Frequency** - ISPs near Fs/4 (11 kHz at 44.1kHz) are worst-case
2. **Magnitude** - +0.3dB vs +2dB is a huge difference in audible distortion
3. **Density** - Clustered ISPs during loud passages create sustained distortion
4. **Musical context** - ISPs during quiet passages are more audible (no masking)

### Technical Background

Reference: [Benchmark Media - Intersample Overs in CD Recordings](https://benchmarkmedia.com/blogs/application_notes/intersample-overs-in-cd-recordings)

#### Frequency Dependence

The worst-case ISP occurs at **Fs/4** (quarter of sample rate):

| Sample Rate | Worst-Case Frequency | Audibility |
|-------------|---------------------|------------|
| 44.1 kHz | **11.025 kHz** | Fully audible, most problematic |
| 48 kHz | 12 kHz | Fully audible |
| 88.2 kHz | 22.05 kHz | Barely audible |
| 96 kHz | 24 kHz | Ultrasonic, less problematic |
| 192 kHz | 48 kHz | Ultrasonic, least problematic |

**Why Fs/4?** At this frequency, samples can fall exactly at zero crossings, completely missing the peaks. Maximum theoretical overshoot is **+3.01 dB** (√2 factor).

#### Why High-Frequency ISPs Are Less Severe

1. **Many DACs filter ultrasonic content** - reconstruction filters attenuate >20kHz
2. **Amplifiers and speakers have limited bandwidth** - may not reproduce the clipped waveform
3. **Human hearing rolls off** - less sensitive above 15-16kHz

However, ISP clipping still creates **intermodulation distortion (IMD)** that can fold back into audible frequencies, so ultrasonic ISPs are not completely benign.

#### Density Matters More Than Count

| Pattern | Severity | Audibility |
|---------|----------|------------|
| Single isolated ISP | Low | Masked by transient, sounds like part of attack |
| Sparse random ISPs | Low-Medium | Occasional micro-clicks, often masked |
| Dense sustained ISPs | **High** | Continuous distortion, audible "crunch" or harshness |
| ISPs during quiet passages | **High** | No masking, clearly audible artifacts |

**Example from Benchmark's analysis:**
- Steely Dan "Gaslighting Abbie": 1,129 ISPs over 5+ minutes (~3.7/sec), max +0.8dB
- This is actually **mild** - many loudness-war masters have 10,000+ ISPs with +2dB overshoots

### Solution Implemented

#### New Fields in `TruePeakResult`

| Field | Type | Description |
|-------|------|-------------|
| `ISPDensityPeak` | float64 | Worst-case ISPs per second (1-second window) |
| `ISPDensityAvg` | float64 | Average ISPs per second across entire file |
| `ISPsAboveHalfdB` | uint64 | Count of ISPs with >0.5dB overshoot |
| `ISPsAbove1dB` | uint64 | Count of ISPs with >1.0dB overshoot |
| `ISPsAbove2dB` | uint64 | Count of ISPs with >2.0dB overshoot |
| `WorstDensitySec` | float64 | Timestamp (seconds) of peak density window |

#### Density Analysis

ISPs are now tracked per 1-second window:
- **Peak density:** Maximum ISPs in any single second (identifies worst "hot spots")
- **Average density:** ISPs/second across the file (overall severity indicator)
- **Location:** Timestamp of the worst density window

#### Magnitude Distribution

ISPs are counted by severity threshold:
- `>0.5 dB`: Mild overshoots (may clip sensitive DACs)
- `>1.0 dB`: Moderate overshoots (will clip most DACs)
- `>2.0 dB`: Severe overshoots (significant distortion)

### Remaining Proposed Improvements

#### Frequency-Weighted Severity (Medium priority)

Weight ISPs by proximity to Fs/4:

```
severity_weight = 1.0 - abs(isp_frequency - Fs/4) / (Fs/4)
```

ISPs at 11kHz (44.1kHz source) get weight 1.0, ultrasonic ISPs get lower weights.

**Implementation:** Requires FFT analysis around ISP locations to estimate frequency content causing the ISP.

**Effort:** ~1-2 days

#### Musical Context Awareness (Low priority)

Correlate ISPs with signal level:
- ISPs during loud passages: less severe (masked)
- ISPs during quiet passages: more severe (audible)
- Report "unmasked ISP count" for ISPs occurring below -20dBFS

**Effort:** ~1-2 days

### New Fields for `TruePeakResult`

| Field | Type | Description |
|-------|------|-------------|
| `ISPDensityPeak` | float64 | Worst-case ISPs per second (1-second window) |
| `ISPDensityAvg` | float64 | Average ISPs per second during active sections |
| `WeightedSeverity` | float64 | Combined score accounting for frequency, magnitude, density |
| `ISPsAbove1dB` | uint64 | Count of ISPs with >1dB overshoot |
| `WorstFrequencyHz` | float64 | Frequency of the most severe ISP |

### Current vs. Improved Severity Reporting

**Current output:**
```
inter-sample-peaks: Pervasive ISPs: 60816 events, max overshoot 1.14 dB
```

**Improved output (proposed):**
```
inter-sample-peaks: 60816 ISPs (severity: high)
  - Peak density: 847/sec at 2:34
  - 12,403 ISPs >1dB (20%), worst at 11.2kHz
  - Weighted severity: 0.78 (dense mid-frequency ISPs)
```

### References

- [Benchmark Media: Intersample Overs in CD Recordings](https://benchmarkmedia.com/blogs/application_notes/intersample-overs-in-cd-recordings)
- AES Convention Paper: "Sample-Peak and True-Peak Measurement" (TC Electronic)
- EBU R128: Loudness normalisation and permitted maximum level of audio signals
