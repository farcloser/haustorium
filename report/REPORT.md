# Generating reports

## Why?

I am testing against my local collection.
This is very biased by how I have ripped my CDs and vinyls, the kind of music I am
listening to, and by the fact I have already culled it multiple times.

For the tool to get better, I need more diversity, for it to encounter issues
that would never show up in my collection.

Further, I would like the project to be efficient at prioritizing effort.
To do that, I need a larger sample size to figure out what are the most prevalent
issues and (likely) false positives.

## How?

If you are interested, you can follow these simple steps to generate a report.

```bash
# Install golang, ffmpeg, and jq on your machine
# If you are on macos
brew install go ffmpeg jq

# Install Haustorium
go install github.com/farcloser/haustorium/cmd/haustorium@latest

# Copy the report.sh script to your machine, or simply git clone the repository and call:
./report/report.sh "my_music_directory"
```

The generated report will be in `haustorium-report.txt`, and compressed in `haustorium-report.txt.gz`.

You can then [open an issue on the Haustorium Github repository](https://github.com/farcloser/haustorium/issues) and attach the *archive*.

The tool should take about 4 seconds per audio file.

You do not have to run it on your entire collection. Anything helps!

## Concerns

You may have legitimate concerns over this process.
I will try to address these proactively below:

### "Will the tool damage my files?"

Absolutely not.
The tool is read-only and never writes anything to the filesystem (except the report).

However, you may of course elect to run the tool on a **copy** of (some of) your files
to alleviate any concerns.

### "How do I know what information the tool is capturing?"

Just open the report and inspect it.
The report is _NOT_ uploaded automatically, so, you are in control of what gets out.

### "I do not want everyone to know which files I have, and the report contains that"

Yes, the report does include full file paths.
This is useful information as there is usually some provenance info in the directory
or file names that do help qualifying the results.

However, if this is a concern, and if you do not want to disclose which files
you have, you can pass an extra arguments to the tool:

```bash
./report/report.sh --redact-path some_music_directory
```

That will strip out the paths, effectively "anonymizing" the analysis.

### "How do I know this tool is not going to steal my crypto and burn down my laptop?"

You don't.
And you are right to be wary of running random software from random stranger.

That being said, this is not distributed in binary form.
You compile it yourself from source, and the source is available for you to review.
You can (and you should!) inspect it, and confirm for yourself that:
- the tool does not perform any write operation on the filesystem (except the report)
- the tool does not perform any network request
- the tool does not do anything evil or suspicious

If you are not competent in go, you can and should ask someone else to have a look.

Finally, I do have Github history showing that I have contributed large quantities
of code that have been accepted in prominent open-source projects, specifically
nerdctl (https://github.com/containerd/nerdctl/graphs/contributors).
Eg: I am not a fresh GitHub account popping out of nowhere.

But at the end of the day, it is of course your informed decision!

### "I still do not feel like sending this on the open bug tracker. Any other way?"

I respect that.
Yes, you can send an email if you don't want your report out there.
`haustorium+report@farcloser.world`

Thanks for reading!