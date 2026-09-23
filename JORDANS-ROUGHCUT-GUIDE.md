
This guide takes you through the process of how to create a rough-cut which is NOT AI slop.

The nuances and logic MATTERS in a way that Claude Code and Qwen / Qodercli's summaries failed to communicate, repeatedly

**My definition of a rough-cut is as follows;**
A "rough-cut" is not the same as an "AI-slop" cut. When a human intern produces a rough-cut, every cut was made at meaningful and intentional zero-crossing points (generally relative to speech); 80% of those cuts will never need to be adjusted and will likely remain in the final output. Watching a smooth, thoughful rough-cut gives the Editor a feel for the tempo and pacing, and allows the human Editor to get immersed in the storyline and delivery in realtime. Anything that stands out as requiring improvement is then adjusted to taste, but the fundamental edit is complete. AI-slop, by contrast, makes sloppy, haphazard cuts that completely ignore video editing fundamentals. Generally it cuts off words and phrases, leaves excess silence, the narrative does not yet make logical sense (extra words or phrases left in), and are not cut to zero-crossing points (and the ones that are, lack the precision to trim out non-speech before a line is delivered, such as the speaker adjusting their position before delivering a line - which a human editor then has to go trim). This does not provide a cohesive viewing experience and prevents the human Editor from getting a feel for the pacing and the narrative. Virtually every cut that is made in this way requires a human to adjust (even if only by one or two video frames). This is worse than producing no output at all - because not only is the video editor STILL touching every cut on the timeline, you've also wasted the human's time they spent by watching the incomplete work, becauwe a proficient video editor like myself can produce a proper rough-cut in significantly less time than it takes to fix a bad one. It also waste's the compute and system resources we have available, which **COULD HAVE** been used for something useful, instead, a bad rough-cut **ACTIVELY PREVENTS** other agents from completing their own useful tasks. Rough-cutting footage is top priority here...it's the SINGLE BIGGEST time suck for me, but ONLY when done correctly. Being given the time and resources I've allotted to rough-cutting footage is a privilege and it comes at a very real cost in many ways, do not take this lightly.

## EDITING

Here are a few critical FACTS about video editing, which most AI-video-editing tools and plugins DO NOT solve, and therefore, are useless to me (I've tried almost every AI video editing tool under the sun...they all fail...we are focusing on VERY SPECIFIC problems which are very solvable)

VIDEO EDITING IS:
- Visual by nature; finding the zero-crossing points is a **VISUAL** task - human editors LOOK at the waveform, and simply select the frame immediately before the speaking starts.
- Iterative; human video editors re-watch their editing decision **MULTIPLE TIMES** with different context and making tiny adjustments when necessary

VIDEO EDITING IS NOT:
- Deterministic; metrics fluctuate, even in a controlled environment. becky-cut is a great example of a deterministic tool, and it is only useful when I'm filming in a specific room, on a specific iPhone. It took MONTHS of tweaking to find those settings, and it STILL is only correct about 80% of the time, even with every variable controlled. The real world is flexible, video editing needs to be as well

## Workflow

This is the process I use when creating a proper rough-cut. You are expected to recreate this workflow within the becky-tools framework. The process is ITERATIVE and if not done properly, will be discared because, as stated above, producing a BAD rough-cut is WORSE than producing nothing at all. SOME of the steps are deterministic and should be treated as such, however, some steps are non-deterministic and will require intelligent callibration and / or decision making. Perhaps several iterations of it.

NOTE: I work on an NLE timeline (Vegas Pro 18), and I will be describing my workflow accordingly. You have demonstrated and specified that the cli is your preferred method of working. That's fine, so long as you can get results


1. Ensure audio is aligned with video [non-deterministic]
	This is required only when there is a hand clap at some point in the video (generally at the beginning). The purpose of the clap is to create an obvious audio spike, which needs to happen at the exact video frame where the person's hands are fully together. If no clap is present in the footage, skip this step
2. Remove all claps (this affects the next step)
3. Normalize Audio
4. Remove silence and non-speaking at zero-crossing points (relative to speech), which is exactly what becky-cut attempts to do (although I typically do this by hand). Normalizing the audio **without the clap audio spikes** generally makes it much more obvious for silence-detection tools, proficing higher accuracy
5. Undo the audio normalization (it sounds bad - but is very useful for VISUALIZING the audio waveforms and identifying the zero-crossing points...once the cuts are made, the original audio is restored)
6. Remove bad-takes / retakes; this does NOT remove actual content, it simply removes **OBVIOUS** re-takes where the speaker starts making a statement, STOPS SPEAKING, regathers himself, then delivers the **same line** a second time (or third, or fourth, or fifth, etc). Keep all the good takes, remove the bad ones
	good take = the staement is finished
	bad take = the statement is cut off or trails off
**DO NOT** remove bad takes which have no alternative; if it's our ONLY take, then it is the one we keep - we are NOT removing content or context at this point; simply cleaning up obvious mistakes which were fixed immediately following the mistake