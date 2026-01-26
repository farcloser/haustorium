# Haustorium

> an audio analysis tool specialized in music defect detection
> [a rootlike structure that grows into or around another structure to absorb water or nutrients](https://en.wikipedia.org/wiki/Haustorium)

![logo.png](logo.jpg)

## Purpose

For music lovers who care and who have a local collection, it is essential to be able
to assess the quality of these files.

This ranges from identifying bad files (for example: lossy compression masquerading as lossless),
bad masters (example: brickwalled), bad vinyl rip (example: clipping), and other audible defects.

Further, it is interesting to further analysis track (are their vocals, peak detection,
frequency response, BPM, key, etc).

Haustorium aims at providing all of that in a golang binary and library.

We are prioritizing feature development in go, but will shell out to sox
or ffmpeg in some cases.
