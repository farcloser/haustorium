# Known issues

### noise-floor: detection conflates spectral rolloff with noise

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

The spectral analyzer caps at 100 FFT windows (8192 samples, 50% overlap)
which covers roughly 9 seconds of 44.1 kHz audio. All spectral-derived checks
(noise floor, hum, lossy transcode, sample rate authenticity, spectral centroid)
are based on this sample.

For tracks where the character changes significantly (e.g. quiet intro
followed by dense arrangement), the spectral results may not represent the
full recording.

**Planned fix:** sample windows evenly across the full track duration
instead of taking the first N windows.

### dropouts: detection counts zero crossings in decaying signal tails

The dropout detector flags zero runs of >= 1ms as discontinuities. In a
decaying signal (fade-out, note release, trailing silence), the waveform
amplitude drops low enough that consecutive samples land on exactly zero
for just over 1ms as the signal oscillates through the zero line.

This produces dozens or hundreds of "zero run" events clustered at the
end of a track, none of which are actual dropouts. For example, a chamber
music track with a natural decay into silence reported 358 discontinuities
— all located in the final 1-2 seconds of decay, all 1-2ms long.

**Planned fix:** exclude zero runs that occur during a sustained low-energy
passage (e.g. RMS below -60 dB in the surrounding window). Real dropouts
are sudden silence in the middle of audible content, not gradual decay.
Alternatively, raise the minimum zero run duration to something less
sensitive (e.g. 5-10ms) so that natural zero crossings are ignored.
