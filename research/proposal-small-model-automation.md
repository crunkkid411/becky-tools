# Proposal: a small always-on "doer" model for becky (MiniCPM5-2B)

Written 2026-09-18 by the local agent. The evidence is in `research/model-minicpm5-2b.md`.
**Nothing is built. This waits for Jordan's yes.**

## The idea in one paragraph

Gemma-4 E4B stays becky's **judge**: it checks work, makes yes/no decisions, and watches video. A
much smaller model becomes the **doer**. It turns a plain-English request ("back up my projects
every night at 11", "transcribe this, then search it for the county clerk") into the right becky
tool call with the right details. MiniCPM5-2B did that 5 out of 5 times in my test, about twice as
fast as Gemma, using about 60% of the graphics memory (~1.8 GB). That's small enough to leave
loaded while other things run.

## What the tests already settled

- **The doer must not retype file paths.** When I forced MiniCPM5's answers into JSON, it mangled
  `X:\footage\23_hj-fbi-recap` into `X:\x0cootage\x08rand_hj-fbi-recap`, because backslashes are
  special in JSON. In its own XML format the paths came out exactly right. The safe design either
  way: **code pulls the paths out of the request, and the model only picks which one goes where.**
  That's the same "select, don't generate" rule the Jev docs teach.
- **It isn't the decision model.** It's weak at quick yes/no judgements (0.686 vs Qwen3.5-4B's
  0.813 in an independent test), so becky-decide stays on Gemma-4 E4B.
- **Its benchmark lead comes from long thinking.** For quick jobs we call it with thinking off,
  and the gains shrink. Judge it only on becky's own tasks.

## Steps

### Step 1: make its tool calls readable (small)

- First, see whether a newer llama.cpp already understands MiniCPM5's XML tool format. If it does,
  update the one llama.cpp build and there's nothing to write.
- If not, a small Go reader for its XML calls, with unit tests built from the real outputs saved
  in the research doc. Paths are checked against the paths found in the request. A path the model
  invented is rejected, never used.
- The tool menu is generated from becky's real tool list, not written by hand.

### Step 2: a real test, on your own requests

- 100+ requests in your own words, taken from your chats and past becky-ask use. Shape it like
  Katanemo's test: include two-step requests, follow-ups ("do that again for the other folder"),
  and requests no tool fits.
- Run MiniCPM5-2B, Qwen3.5-4B (becky-ask's current model) and Gemma-4 E4B on all of it. Measure
  right tool, right details, time and memory. **The numbers pick the model, not the benchmark
  table.**

### Step 3: wire in the winner

- It becomes the tool-picker behind becky-ask. Anything it's unsure about, or anything that fails
  the path check, goes up to Gemma, then to you as a one-line question.
- **Scheduled tasks:** the model only turns your sentence into a task entry, and you confirm it
  once. The timer itself stays plain Windows Task Scheduler, a dumb, predictable scheduler with no
  AI in the loop. That matches the dark-factory rule and the standing rule that nothing gets
  scheduled unless you ask for it.
- Anything that deletes, overwrites or sends data somewhere always asks you first, however sure
  the model is.

### Worth measuring during step 1 (not promised)

Whether ~1.8 GB fits beside Whoretana's brain and voice under the 8 GB card limit. If it does,
Whoretana could hand "do this" requests to the doer without unloading anything.

## Not proposed now

- **Fine-tuning MiniCPM5 into a decision model.** Only if becky-decide on Gemma turns out not good
  enough on your data. decider-2b shows fine-tuning can take a 2B model from 0.62 to 0.81.
- **Needle.** Only if a job needs tool-picking with no graphics card at all (for example during a
  render). It would need fine-tuning on becky's tools and telemetry switched off.
- **Arch-Router / Plano.** Older and more restrictively licensed. The three models above already
  route 5/5.
