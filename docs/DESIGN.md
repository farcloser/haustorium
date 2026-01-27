# Haustorium: Audio Analysis Features

## Complexity Legend

| Level | Meaning |
|-------|---------|
| **Easy** | Pure Go, straightforward implementation |
| **Medium** | Go + FFT (gonum) or significant algorithm work |
| **Hard** | Substantial effort; shell out to external tool instead |

---

## Roadmap

### Tier 1 — Easy + Critical

Ship first. Pure Go, high value, no excuses.

| Feature                 | Type | Notes |
|-------------------------|------|-------|
| Clipping          | Defect | Count consecutive samples at ceiling |
| Fake Hi-Res (Bit Depth) | Defect | Check if lower bits are zeros |
| Truncation              | Defect | Check final samples for fade/silence |

### Tier 2 — Easy + High

Quick wins after Tier 1.

| Feature | Type | Notes |
|---------|------|-------|
| DC Offset | Defect | Mean of all samples |
| Fake Stereo | Defect | Compare L/R channels |
| Phase Issues | Defect | Sum to mono, compare RMS |
| Inverted Phase | Defect | Channel correlation |
| Silence Segments | Feature | Scan for low RMS sections |

### Tier 3 — Critical + ffmpeg escape hatch

High value, but can shell out to ffmpeg instead of implementing from scratch.

| Feature | Type | ffmpeg approach |
|---------|------|-----------------|
| Loudness (LUFS) | Feature | `ebur128` filter |
| Dynamic Range (DR) | Feature | `astats` filter for peak/RMS, compute DR |

---

## Defect Detection

| Defect                               | What It Is                                            | Why It's Bad | Audiophile | Casual | Complexity |
|--------------------------------------|-------------------------------------------------------|--------------|:----------:|:------:|:-----------|
| [DONE, HL] **Clipping**              | Consecutive samples at digital ceiling (0dBFS) | Audible distortion, harshness on peaks. Unrecoverable. | 🔴 Critical | 🟡 Medium | Easy |
| [DONE, HL] **Truncation**                | Abrupt ending without fade or silence                 | Indicates incomplete rip, corruption, or bad edit | 🔴 Critical | 🟠 High | Easy |
| [DONE, HL] **Lossy Transcode**           | Lossy source (MP3/AAC) re-encoded to lossless (FLAC)  | Fraud. You paid for lossless, got upscaled garbage. | 🔴 Critical | 🟡 Medium | Medium — spectral analysis for brick wall cutoff |
| [DONE, HL] **Fake Hi-Res (Sample Rate)** | 44.1kHz upsampled to 96/192kHz                        | Fraud. No additional information, just larger files. | 🔴 Critical | 🟡 Medium | Medium — spectral analysis above Nyquist/2 |
| [DONE, HL] **Fake Hi-Res (Bit Depth)**   | 16-bit zero-padded to 24-bit                          | Fraud. Lower 8 bits are zeros. | 🔴 Critical | 🟢 Low | Easy |
| [DONE, HL] **Brickwalling**              | Extreme dynamic range compression (DR < 6)            | Fatiguing, lifeless sound. The "loudness war" casualty. | 🔴 Critical | 🟡 Medium | Medium — RMS vs peak over windows |
| [DONE, HL] **Dropouts/Glitches**         | Brief interruptions or digital artifacts              | Ripping errors, bad source, damaged files | 🔴 Critical | 🔴 Critical | Medium — statistical discontinuity detection |
| [DONE, HL] **DC Offset**                 | Constant voltage bias shifting waveform away from zero | Wastes headroom, causes clicks between tracks, stresses speakers | 🟠 High | 🟢 Low | Easy |
| [DONE, HL] **Fake Stereo**               | Identical L/R channels marketed as stereo             | Deceptive. Wastes storage. | 🟠 High | 🟢 Low | Easy |
| [DONE, HL] **Phase Issues**              | L/R cancellation when summed to mono                  | Sounds hollow on mono systems, disappearing instruments | 🟠 High | 🟢 Low | Easy |
| [DONE, HL] **Inverted Phase**            | One channel 180° out of phase                         | Weird imaging, bass cancellation on mono sum | 🟠 High | 🟢 Low | Easy |
| [DONE, HL] **Inter-Sample Peaks**        | Overs that only appear after D/A reconstruction       | Causes clipping in DACs. Invisible to naive peak meters. | 🟠 High | 🟢 Low | Medium — interpolation/upsampling |
| **Click/Pop**                        | Transient spikes from vinyl damage or bad edits       | Distracting, indicates source issues | 🟠 High | 🟠 High | Hard → **sox** |
| [DONE, HL] **60Hz Hum**                  | Mains frequency contamination                         | Poor recording/transfer, ground loop | 🟠 High | 🟡 Medium | Medium — FFT spike at 50/60Hz |
| [DONE, HL] **High Noise Floor**          | Excessive background noise/hiss                       | Poor source quality, bad transfer | 🟠 High | 🟡 Medium | Medium — RMS during quiet sections |
| **Wow/Flutter**                      | Pitch variations from tape/vinyl transfer             | Speed instability in analog source | 🟠 High | 🟢 Low | Hard → **aubio** |
| [DONE, HL] **Silence Padding**           | Excessive silence at start/end (>2-3 seconds)         | Hidden tracks are fine; unintentional padding is sloppy | 🟡 Medium | 🟢 Low | Easy |
| [DONE, HL] **Channel Imbalance**         | L/R volume difference                                 | Bad mastering, equipment issues | 🟡 Medium | 🟢 Low | Easy |

---

## Feature Extraction (Non-Defects)

| Feature                       | What It Is | Use Case | Audiophile | Casual | Complexity |
|-------------------------------|-----------|----------|:----------:|:------:|:-----------|
| [DONE] **Loudness (LUFS)**    | Integrated loudness per EBU R128 | Volume normalization, ReplayGain | 🔴 Critical | 🟠 High | Medium — EBU R128 algorithm (or shell to **ffmpeg**) |
| [DONE] **Dynamic Range (DR)** | Crest factor / DR score | Identify "loud" vs "dynamic" masters | 🔴 Critical | 🟡 Medium | Medium — RMS vs peak over windows |
| [DONE] **True Peak**          | Maximum reconstructed sample level | Prevent DAC clipping | 🟠 High | 🟢 Low | Medium — oversampling required |
| [DONE] **Silence Segments**          | Locations and durations of silence | Track splitting, hidden track detection | 🟠 High | 🟡 Medium | Easy |
| [DONE] **Frequency Response** | Energy distribution across spectrum | Mastering comparison, EQ matching | 🟠 High | 🟢 Low | Medium — FFT binning |
| **BPM**                       | Tempo in beats per minute | DJ features, playlist matching | 🟡 Medium | 🟠 High | Hard → **aubio** |
| **Key**                       | Musical key (C major, A minor, etc.) | DJ features, harmonic mixing | 🟡 Medium | 🟠 High | Hard → **keyfinder** or **essentia** |
| [DONE] **Spectral Centroid**  | "Brightness" - where energy is concentrated | Tonal analysis, genre classification | 🟡 Medium | 🟢 Low | Medium — FFT + weighted average |
| **Vocal Presence**            | Detect if vocals are present | Instrumental detection, karaoke tagging | 🟡 Medium | 🟠 High | Hard → **demucs** or **spleeter** |
| [DONE] **Stereo Width**              | How "wide" the stereo image is | Mastering analysis | 🟡 Medium | 🟢 Low | Easy |

---

## Interest Legend

- 🔴 Critical — "I absolutely need this"
- 🟠 High — "Very useful"
- 🟡 Medium — "Nice to have"
- 🟢 Low — "Don't care"

---

## Out of Scope

Things we considered and explicitly decided NOT to implement in Haustorium.

| Feature | Rationale |
|---------|-----------|
| **Lyrics transcription (ASR)** | Transcribing sung lyrics from audio is a different beast than speech recognition. Whisper and similar models struggle with pitch variations, vibrato, background music, and effects. Non-English is even worse. The compute cost is high (ML inference, GPU helps), and the output quality is mediocre—you'll spend hours fixing garbage. If users want lyrics: (1) embedded tags via gill, (2) online fetch via Hypha (Musixmatch, Genius), or (3) manual transcription, which is still more accurate than ML for music. Not worth the ROI. |
| **Pre-echo detection** | MP3/AAC artifact appearing before transients. Already covered by lossy transcode detection—if we detect the brick wall cutoff, pre-echo is implied. No need for separate detection. |
| **Aliasing detection** | Artifacts from poor sample rate conversion. Hard to detect reliably and rare in practice. Not worth the effort. |
| **Jitter artifact detection** | Digital clock issues during rip. Essentially undetectable from the file alone—jitter is a real-time phenomenon that doesn't leave consistent forensic traces in the resulting PCM. |

---

## External Tool Summary

| Tool | Used For | License | Notes |
|------|----------|---------|-------|
| **ffmpeg** | Loudness, DR, True Peak | LGPL 2.1+ | Core libav* is LGPL. PCM-only build avoids GPL codec contamination. `ebur128` and `astats` filters are LGPL. |
| **sox** | Click/pop detection | GPL 2+ | Copyleft. Can't statically link into proprietary code. Shell out only. |
| **aubio** | BPM, wow/flutter | GPL 3 | Copyleft. Shell out only. |
| **keyfinder** | Key detection | GPL 3 | Copyleft. Shell out only. |
| **essentia** | Key detection (alternative) | **AGPL 3** | ⚠️ Network copyleft. Even running as a service triggers AGPL. Avoid unless fully open-sourcing. |
| **demucs** | Vocal presence | MIT | Permissive. Can embed if needed. But heavy (PyTorch). |
| **spleeter** | Vocal presence (alternative) | MIT | Permissive. Also heavy (TensorFlow). |