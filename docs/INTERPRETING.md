# Interpretation of results

## clipping

n.a.
It clips, or it does not.

Severity is decided on the number of clipping events:
- Mild: 1
- Moderate: 10
- Severe: 100

## truncation

Track truncation detection is inherently dependent on RmsDb at the end of a track.
Vinyl rips and live music will definitely have more noise.

To avoid false positives, pass `--source=vinyl` or `--source=live` accordingly
to adapt the detection heuristics.

If a digital source does report likely truncation, it could mean:
- this is a continuous live recording with no fade to silence (if you know it is, just pass the right source)
- or this is actually a vinyl rip (if you knew, fine, if you did not and believed it was a digital transfer, then you definitely got punked on this one)
- or the track has been indeed truncated (genuine defect: bad rip, or very bad job on the publisher side)

Interpretation data for digital is as follows:
- Mild: -40
- Moderate: -30
- Severe: -20

For vinyls and live music:
- Mild: -30
- Moderate: -20
- Severe: -10

And obviously, if your source is continuous live music across tracks, you will presumably
get plenty of false positives from this.


## fake-bit-depth

n.a.
It claims to be N bits. Does it have bits there or not?
If it does not, then it is lying — a 24-bit file with only 16 bits of actual data
is just 16-bit zero-padded. You paid for hi-res, but got baloney.

## fake-sample-rate

Similar, but a bit more subtle.
If there is no signal up there, then it has been upsampled and you got punked.
However, having signal still does not positively mean that it has not been upsampled.

For high sample rates (88200, 96000, etc.), we conservatively report 50% confidence
when no upsampling is detected, meaning: "found no conclusive evidence this was upsampled".

For base sample rates (44100, 48000), there is no standard lower rate to upsample from,
so the check is not applicable and we report 100% confidence.

## lossy-transcode

This one is similar to sample rate.
We can positively identify some types of lossless files that have been transcoded from a lossy source,
but we cannot positively ascertain that this is not the case.
We conservatively report 50% confidence that something is genuine, meaning:
we did not find evidence of a lossy transcode.

For a concrete example: high bit-rate lossy with a cutoff close to Nyquist will look legit.

### What the detector actually measures

The check looks for a spectral brick wall: a sharp energy dropoff at a frequency
consistent with a known lossy codec and bitrate. It reports the cutoff frequency,
rolloff sharpness, and a likely codec match.

### Ambiguity: who introduced the lossy encoding?

A positive detection means "there is a brick wall in the spectrum consistent with
lossy encoding." It does **not** tell you who in the chain introduced it.
Possible causes include:

1. **User fraud** — someone ripped a lossy stream (MP3, Opus, AAC) and wrapped
   it in a lossless container (FLAC, ALAC) to pass it off as lossless.
2. **Label fraud** — the label or distributor worked from a lossy source.
   Budget reissue labels sometimes master from whatever digital source they can
   obtain, which may already be lossy. The resulting CD or "lossless" download
   carries the lossy spectral signature baked in from the source.
3. **Aggressive mastering** — the mastering engineer applied a steep low-pass
   filter near 20 kHz, producing a brick wall that is indistinguishable from
   a lossy codec cutoff. This is rare but not impossible.

The tool cannot distinguish between these cases. It reports the spectral evidence
and lets you decide. Context matters: a 2016 budget reissue of 1960 recordings
with a 20.5 kHz cutoff could be any of the above, while a brand-new release
from a major label with a 16 kHz cutoff is almost certainly a lossy transcode.

## dc-offset

A constant voltage bias in the signal. Usually caused by faulty hardware (sound card, ADC)
or a bad analog chain. It shifts the entire waveform away from the zero line.

Not audible on its own, but it wastes headroom and can cause clicks at edit points.

Measured in dB (higher = worse). Vinyl rips and live recordings tolerate more
because of the analog path involved.

- Mild: -40 dB (digital), -26 dB (vinyl), -30 dB (live)
- Moderate: -26 dB (digital), -13 dB (vinyl), -20 dB (live)
- Severe: -13 dB (digital), 0 dB (vinyl), -10 dB (live)

## fake-stereo

Binary detection: correlation > 0.98 and channel difference < -60 dB.

If both channels are virtually identical, this is a mono recording dressed up as stereo.
Not a defect per se, but dishonest if sold as stereo content.
Always reported as moderate severity when detected.

## phase-issues

Measures how much signal is lost when summing to mono (cancellation in dB).
A well-mixed stereo track should be mono-compatible with minimal cancellation.

High cancellation means stereo content that will sound thin or hollow on mono systems
(phone speakers, some Bluetooth, club PAs summed to mono).

- Mild: 3 dB cancellation
- Moderate: 6 dB cancellation
- Severe: 10 dB cancellation

## inverted-phase

Binary detection: inter-channel correlation < -0.95.

One channel's polarity is flipped relative to the other. This is almost always
a wiring or production error. The result is that mono summation nearly cancels
the entire signal. Always reported as severe when detected.

## channel-imbalance

The absolute dB difference between left and right channel levels.
A well-mastered stereo track should have reasonably balanced channels.

Slight imbalance can be intentional (artistic panning), but large imbalance
usually indicates a hardware problem or a bad transfer.

However, mono-era recordings pressed with early stereo techniques may present
significant channel imbalance, sometimes intentionally (hard panning as a norm).

For digital and live sources:
- Mild: 1 dB
- Moderate: 2 dB
- Severe: 3 dB

For vinyl, thresholds are wider to account for the analog path and era-specific
mastering practices (e.g. hard panning on early stereo pressings):
- Mild: 3 dB
- Moderate: 6 dB
- Severe: 10 dB

## silence-padding

Excessive silence at the beginning or end of a track. Measured as the worst of
leading or trailing silence duration in seconds.

Some silence is normal (pre-roll, fade-out tail), but long stretches suggest
a sloppy rip, a bad track split, or padding from a CD extraction tool.

Vinyl and live sources get higher thresholds since run-in/run-out grooves
and venue ambience can look like silence.

- Mild: 2s (digital), 5s (vinyl/live)
- Moderate: 5s (digital), 10s (vinyl/live)
- Severe: 10s (digital), 20s (vinyl/live)

## hum

Mains hum detection at 50 Hz (Europe, Asia) and/or 60 Hz (Americas, Japan).
Caused by electromagnetic interference from power lines bleeding into the audio chain.

Detection is binary (spectral peak present or not). Severity is graded by the
hum level in dB above the noise floor.

Vinyl rips almost always have some hum from the turntable motor and grounding.
Live recordings pick up hum from PA systems and stage lighting.

- Mild: 10 dB (digital), 20 dB (vinyl), 15 dB (live)
- Moderate: 20 dB (digital), 30 dB (vinyl), 25 dB (live)
- Severe: 30 dB (digital), 40 dB (vinyl), 35 dB (live)

## noise-floor

The overall background noise level of the recording in dB.
Lower (more negative) is cleaner.

A digital studio recording should have a very low noise floor.
Vinyl has surface noise. Live recordings have venue ambience.

- Mild: -30 dB (digital), -20 dB (vinyl/live)
- Moderate: -20 dB (digital), -10 dB (vinyl/live)
- Severe: -10 dB (digital), 0 dB (vinyl/live)

## inter-sample-peaks

True peak analysis. When a DAC reconstructs the analog signal between samples,
the interpolated waveform can exceed 0 dBFS even if no individual sample clips.
These are inter-sample peaks (ISPs).

Matters for broadcast compliance (EBU R128 requires true peak < -1 dBTP)
and for lossy encoding where ISPs cause audible distortion.

Severity is based on the number of ISP events:
- Mild: 1
- Moderate: 100
- Severe: 1000

## loudness

Informational only, no severity. Reports integrated loudness in LUFS
(Loudness Units Full Scale, per ITU-R BS.1770) and loudness range in LU.

Useful context for understanding other results. A very loud master
(e.g. -6 LUFS) is more likely to clip and have ISPs.

## dynamic-range

DR score, roughly equivalent to the crest factor measured per the
TT Dynamic Range methodology. Higher is more dynamic.

The scale is descending: lower scores are worse.

- DR12+: Excellent dynamics
- DR8-11: Good dynamics
- Mild: DR8 (compressed but acceptable)
- Moderate: DR6 (heavily compressed)
- Severe: DR4 (brickwalled, loudness war casualty)

## dropouts

Discontinuities in the audio stream: sudden amplitude jumps, runs of zero samples,
and DC level shifts. These are glitches from buffer underruns, bad disc reads,
or transmission errors.

The total count of all discontinuity types determines severity.

Vinyl rips get higher thresholds because surface damage (scratches, debris)
can look like dropouts.

- Mild: 1 (digital), 5 (vinyl)
- Moderate: 5 (digital), 15 (vinyl)
- Severe: 20 (digital), 40 (vinyl)
