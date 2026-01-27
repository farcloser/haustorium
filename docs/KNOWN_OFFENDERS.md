# Known offenders and other curiosities or things to debug

These albums and tracks have legitimate sonic characteristics that make them report false positives, or are
otherwise problematic.
This is seemingly very hard to solve, as what was recorded is virtually indistinguishable from "defects".

## Hum

Hum detection still fails a lot on legit music that sounds... like a hum...

> Status: maybe bug

Again getting punked by Zorn.
Sounds like legitimate music being reported as hum.

* /Volumes/Anisotope/gill/sweep.zorn/Zorn, John/1998 - Filmworks VIII_ 1997/[1998-FIX_MEDIA-FIX_LABEL-]/18-21 Engaño.m4a
* /Volumes/Anisotope/gill/sweep.zorn/Zorn, John/1998 - Filmworks VIII_ 1997/[1998-FIX_MEDIA-FIX_LABEL-]/17-21 Olvido.m4a
* /Volumes/Anisotope/gill/sweep.zorn/Zorn, John/2001-03-27 - The Gift/[2001-03-27-CD-Tzadik-TZ 7332-702397733225]/03-10 Samarkan.m4a
* /Volumes/Anisotope/gill/sweep.zorn/Zorn, John/1998 - Filmworks VIII_ 1997/[1998-FIX_MEDIA-FIX_LABEL-]/15-21 Locura.m4a
* /Volumes/Anisotope/gill/sweep.zorn/Zorn, John/1998 - Filmworks VIII_ 1997/[1998-FIX_MEDIA-FIX_LABEL-]/12-21 Deseo.m4a

## Dropouts

> Status: OMFG

This reports 8713 jumps...  aggressive transients, abrupt dynamic shifts, deliberate sonic disruptions...

/Volumes/Anisotope/gill/sweep.zorn/Zorn, John/1998-10-20 - Music for Children/[1998-10-20-CD-Tzadik-TZ 7321-702397732129]/07-08 Cycles du nord.m4a

Right now, we do nothing here. Experimental will always fool detectors...

What we could do:
1. Raise the delta severity threshold — from 0.50 to something like 0.65 or 0.70. This would eliminate most of these false positives but might miss genuine subtle dropouts.
2. Add a density heuristic — if thousands of deltas appear across both channels following musical structure, it's likely intentional content, not defects. Real dropouts are sparse.
3. Correlate with musical energy — if delta events coincide with high RMS sections (loud passages), they're transients, not glitches. Genuine dropouts tend to appear in quieter sections or at random.

## Inter sample peaks

> Status: unclear

LOTS of John Zorn flagged with this.
Probably not intentional?

This type of recording WILL clip any DAC that does not attenuate before reconstruction.


## Dynamic range

> Status: confirmed

Legit music, inside an album with a good DR, this specific track is simply pushed hard.

/Volumes/Anisotope/gill/sweep.zorn/Zorn, John/1998-10-20 - Music for Children/[1998-10-20-CD-Tzadik-TZ 7321-702397732129]/07-08 Cycles du nord.m4a

## DC Offset

> Status: confirmed

Still Zorn.
Confirmed real offset.
"Why" is unclear.

/Volumes/Anisotope/gill/sweep.zorn/Zorn, John/1986 - The Big Gundown_ John Zorn Plays the Music of Ennio Morricone/[2000-08-22-CD-Tzadik-TZ 7328-702397732822]/10-16 Once Upon a Time in the West.m4a

## Phase issues

> Status: confirmed

Another Zorn, with a lot of resonance.
Whether the phase cancellation is actually on purpose is anyone's guess.

/Volumes/Anisotope/gill/sweep.zorn/Zorn, John/1998 - Filmworks VIII_ 1997/[1998-FIX_MEDIA-FIX_LABEL-]/15-21 Locura.m4a

## Lossy transcode

> Status: unconfirmed

Very low frequency music throughout. Nothing above 20kHz really...
Purposeful?
Maybe?
To be confirmed against a fresh rip.

/Volumes/Anisotope/gill/sweep.zorn/Zorn, John/1998 - Filmworks VIII_ 1997/[1998-FIX_MEDIA-FIX_LABEL-]/18-21 Engaño.m4a

> Status: probably confirmed

A 1986 vinyl from a 60s master being cuttoff at 20kHz is not unusualy.
Or it is an ALAC reencode of a lossy format.

/Volumes/Anisotope/gill/sweep.zorn/Sonny Clark Memorial Quartet, The/1986 - Voodoo/[1986-12_ Vinyl-FIX_LABEL-]