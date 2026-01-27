# Known issues

### noise-floor: detection conflates spectral rolloff with noise

> **OPEN**

The noise floor check measures average energy in the 14-18 kHz band relative
to the 1-10 kHz reference band. This detects elevated broadband noise (tape hiss,
poor ADC), but it also fires on recordings whose musical content simply has
little high-frequency energy (e.g. a string quartet, solo piano, spoken word).

A string quartet with a perfectly clean noise floor will report an elevated
reading because its energy is naturally concentrated below 10 kHz: the HF
band is quiet because there is no musical content there, not because of noise.

**Planned fix:** replace the fixed-band measurement with a percentile-based
spectral floor. Instead of comparing 14-18 kHz to 1-10 kHz, find the 10th
percentile of bin energies across the full spectrum (above ~500 Hz). This
measures the actual minimum sustained energy level (true noise floor)
regardless of the spectral shape of the music.

### hum: detection triggers on musical content near mains frequencies

> **OPEN**

The hum detector looks for spectral peaks at 50 Hz and 60 Hz harmonics
(100, 150, 200, 250, 300 Hz and 120, 180, 240, 300, 360 Hz) relative to
surrounding bins. These frequencies overlap with common musical fundamentals:
cello open strings, bass guitar, low brass, and low piano notes all produce
energy near these exact frequencies.

A string quartet with prominent low cello passages can report severe 50 Hz
hum because musical harmonics at 100, 150, and 200 Hz look identical to
mains interference spikes in the averaged spectrum.

Additionally, the spectral analyzer only processes `WindowsMax` windows
(default: 100, covering ~9 seconds of audio). If those seconds contain
prominent low-frequency content, the detection is biased by that sample.

**Planned fix:** require hum peaks to appear at multiple harmonics
simultaneously (fundamental + at least 2 harmonics) rather than flagging
on any single harmonic spike. Real mains hum always has a harmonic series;
a musical note near 100 Hz does not also spike at exactly 50, 150, 200,
250, and 300 Hz. Increasing `WindowsMax` or sampling windows across the
full track would also reduce bias from localized musical content.

### Spectral analysis limited to first ~9 seconds

> **FIXED**

The spectral analyzer caps at 100 FFT windows (8192 samples, 50% overlap)
which covers roughly 9 seconds of 44.1 kHz audio. All spectral-derived checks
(noise floor, hum, lossy transcode, sample rate authenticity, spectral centroid)
are based on this sample.

For tracks where the character changes significantly (e.g. quiet intro
followed by dense arrangement), the spectral results may not represent the
full recording.

**Fix:** the spectral analyzer now reads the entire track into memory
and distributes `WindowsMax` FFT windows evenly across the full duration.
Short tracks that fit within `WindowsMax` windows are still fully analyzed.

### dropouts: detection counts zero crossings in decaying signal tails

> **FIXED**

The dropout detector flags zero runs of >= 1ms as discontinuities. In a
decaying signal (fade-out, note release, trailing silence), the waveform
amplitude drops low enough that consecutive samples land on exactly zero
for just over 1ms as the signal oscillates through the zero line.

This produces dozens or hundreds of "zero run" events clustered at the
end of a track, none of which are actual dropouts. For example, a chamber
music track with a natural decay into silence reported 358 discontinuities,
all located in the final 1-2 seconds of decay, all 1-2ms long.

**Fix:** the dropout detector now maintains a per-channel RMS ring buffer
(50ms window, sum of squares). When a zero run starts, the current RMS
level is captured. When the run ends, the event is only emitted if the
RMS at run start was above `ZeroRunQuietDb` (default: -50 dB). Zero runs
in quiet/decaying passages are silently ignored.

### dropouts: delta detector triggers on normal musical transients

> **FIXED**

The delta detector flags sample-to-sample jumps exceeding `DeltaThreshold`
(default: 0.5, i.e. 50% of full scale). In percussive or energetic music,
normal fast transients (drum hits, brass stabs, plucked strings) routinely
produce sample-to-sample changes of 50-110% of full scale at 44.1 kHz.

A John Zorn chamber music track reported 245 delta events, all in the
main body of the music (158-265s), at normal RMS levels (-10 to -26 dB),
with 237 on the left channel and 8 on the right. None were actual
discontinuities; the audio content simply has aggressive transients.

A real dropout has a specific signature: audio at normal level, sudden
drop to near-zero, then resumption. A fast musical transient has large
sample values on both sides of the jump.

**Fix:** the delta detector now requires that at least one of the two
samples flanking a jump is near zero (`DeltaNearZero`, default: 0.01 =
-40 dB). The `isDeltaDropout()` helper checks
`abs(prev) < nearZero || abs(cur) < nearZero`. A genuine dropout
transitions between normal audio and silence, so one side is always near
zero. Musical transients have significant amplitude on both sides (e.g.
prev=0.35, cur=-0.77) and are filtered out. The same test track dropped
from 245 false positives to 4 genuine events, all showing one side at
full level and the other near zero.

### dropouts: delta detector triggers on normal zero crossings in loud material

> **OPEN**

The near-zero filter (`isDeltaDropout`) successfully eliminates transient
false positives, but it cannot distinguish a real dropout from a normal
zero crossing in a loud waveform. A 500 Hz signal at 44.1 kHz crosses
zero ~1000 times per second. At each crossing, one sample lands near zero
while the adjacent sample is at full amplitude, producing a delta > 0.5
with one side near zero, exactly matching the dropout signature.

A John Zorn collage track (Battle of Algiers) reported 147 delta events
spread across the full duration (26-218s), all on the 32-bit decode path,
all at normal RMS (-11 to -26 dB). Every event has one sample in the
range 0.000-0.009 and the other at 0.5-0.8 full scale. Many occur in
rapid pairs at the same timestamp, consistent with the waveform passing
through zero on consecutive samples. None are actual dropouts; they are
normal zero crossings in aggressive, dynamically extreme music.

This is a fundamental limitation of single-sample delta detection: a
dropout edge (audio → silence) is indistinguishable from a zero crossing
(waveform passing through zero) when examined one sample at a time.

**Possible mitigations (none fully solve the problem):**
- Tighten `DeltaNearZero` from 0.01 to 0.001 (filters most false
  positives but also reduces sensitivity to real dropouts)
- Compare windowed RMS on both sides of the event (~5 samples each):
  a zero crossing has similar RMS on both sides, a dropout edge has
  high RMS on one side and low on the other (adds complexity, still
  fails on intentional hard edits)
- Raise `DeltaThreshold` above 0.8 (fewer false hits, fewer real catches)
- Disable delta detection by default and make it opt-in
