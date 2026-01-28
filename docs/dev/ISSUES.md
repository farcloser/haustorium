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
