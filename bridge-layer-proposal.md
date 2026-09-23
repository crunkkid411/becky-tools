

We need a Local Deterministic Workflow Engine (a "Factory Foreman") for Windows.
Similar to a human assembly line, we need a system where human intent (a button click or a Markdown protocol) is compiled into a rigid sequence of steps. If an AI hallucinates, the system catches it, rejects the output, and retries or escalates—without the human having to manually prompt it.

We are building a manufacturing plant for data. The AI is just a guy with a wrench on the assembly line. The Foreman is the one holding the clipboard.

Philosophy: "Code is law." Prose/Markdown is just documentation; the actual rules must be compiled into executable logic. AI outputs must be validated against strict JSON contracts before moving to the next step.

We are running into the fundamental conflict between Native OS primitives (Win32 Named Pipes, C++, Go) and Sandboxed Environments (Chrome). Our core daemon (Go/C++) is listening on a Win32 Named Pipe, is that correct?
Chrome cannot talk to a Win32 Named Pipe. Chrome must use stdio via Native Messaging.
VS Code can talk to a Named Pipe, but requires a specific Node.js implementation.

The Bottlenecks:
1. Orchestration via Chat: We are currently relying on Claude Code to "chain" tools together manually. There is no persistent background service managing the assembly line when I'm not actively prompting.
2. The Sandbox Gap: Browsers, extensions and web UIs cannot talk to local EXEs or file systems. Local Windows execution is completely sandboxed from Chrome in any reliable manner
3. State Amnesia: Tools run, finish, and disappear. Do we have a central "clipboard" tracking a job's lifecycle (e.g., Queued → Transcribing → Needs Review → Done)?
Jargon Overload: Terms like "Temporal," "Event Bus," and "State Machines" are dense. You just need a simple way for Program A to tell Program B to do work and report back.

## Specific IPC Mechanisms:
Both Chrome and VS Code provide native, OS-level escape hatches specifically designed to talk to local binaries without spinning up a web server.

- stdio (Standard Input/Output): What you referred to as "STIO". This is the oldest IPC mechanism. Process A spawns Process B and pipes data into its stdin and reads from its stdout.
- Chrome Native Messaging: The only way a Chrome Extension talks to a local Windows binary. Chrome spawns your .exe and communicates strictly via stdio. It requires a JSON manifest registered in the Windows Registry (HKCU\Software\Google\Chrome\NativeMessagingHosts\).
- VS Code Extension Host (Node.js): VS Code extensions run in a Node.js process. To talk to your Go daemon, the extension uses Node's child_process.spawn() (communicating via stdio) or Node's net module to connect directly to a Win32 Named Pipe (\\.\pipe\your_pipe).
- PowerShell: Uses the .NET [System.IO.Pipes.NamedPipeClientStream] class to connect directly to Win32 Named Pipes, or Start-Process -RedirectStandardInput for stdio.

(i don't know what they're called for .cmd or .bat)

## Zero Web Bloat Proposed Solution:
Shims (tiny translation binaries) that bridge the sandboxed environments to our existing Win32 Named Pipe daemon (if it even exists).

We need a tiny, headless Go executable whose only job is to read from stdin (Chrome), format it, and push it to our existing Win32 Named Pipe.


## AI-peer-review advised the following:
The Prompt for Claude Code: "Write a headless Go program that acts as a Chrome Native Messaging host. It must read 4-byte little-endian length-prefixed JSON messages from os.Stdin, strip the Chrome wrapper, and forward the raw JSON payload to a Windows Named Pipe named \\.\pipe\becky_ipc. It must then read the response from the pipe and write it back to os.Stdout with the correct 4-byte length prefix."
Registry Setup: You must create a .json manifest file and point the Windows Registry (HKCU\Software\Google\Chrome\NativeMessagingHosts\com.becky.host) to it.
Phase 2: The VS Code Extension Client
Your VS Code extension needs to bypass HTTP and connect directly to your Named Pipe.
The Prompt for Claude Code: "Write a TypeScript snippet for a VS Code extension that uses the Node.js net module to connect to a Windows Named Pipe (\\.\pipe\becky_ipc). It should send a JSON buffer and listen for the JSON response, handling the stream data events correctly."
Phase 3: Standardizing the Payload (The Contract)
Since you are using raw pipes and stdio, you must define how messages are framed so the pipes don't bleed into each other.
The Rule: Every message sent through the Named Pipe or stdio must be prefixed with a 32-bit integer defining the byte length of the JSON payload. (This is how Chrome Native Messaging and standard LSP protocols handle stream framing).
The Prompt for Claude Code: "Write a Go helper function WriteFramedJSON(pipe io.Writer, data any) and ReadFramedJSON(pipe io.Reader) that handles the 4-byte uint32 length prefixing for our Win32 Named Pipe IPC."
Summary of Your Next Steps
Acknowledge the Sandbox: Accept that browsers and Electron apps require specific OS-level bridges (Native Messaging / Node net).
Build the Shim: Have Claude Code write the Go stdin/stdout Native Messaging host.
Register the Host: Write the PowerShell script to register the .json manifest in the Windows Registry so Chrome can find your Go binary.
Enforce Framing: Implement the 4-byte length prefix on your existing Go daemon's Named Pipe listener so it can handle stream-based IPC reliably.
You already have the engine. You just need the exact adapter plugs to let the sandboxed UIs talk to it.

### Alternate AI-peer-review adivce:

This is the first time I think I know exactly where you're stuck.

And the good news is...

**I don't think you need to learn more AI.**

I think you need to learn one boring field that has existed for decades.

## Workflow engines.

Not LangGraph.

Not AutoGen.

Not CrewAI.

Not MCP.

Those are all trying to solve "AI."

You're trying to solve **execution**.

---

## Let's use your green "+" button.

Forget AI for a second.

You click:

```
+
```

What ACTUALLY happens?

Probably something very close to this.

```
UI

↓

Run becky-transcribe.exe

↓

Read transcript.json

↓

Confidence < threshold?

       │
       ├── No → Finish
       │
       └── Yes

↓

Run diarization

↓

Multiple speakers?

       │
       ├── No
       │
       └── Yes

↓

Run identify

↓

Low confidence?

↓

Run Gemma

↓

Still low?

↓

Run 12B

↓

Return transcript.json
```

Notice something?

**There is almost zero AI in that diagram.**

It's mostly:

```
IF

THEN

ELSE

RUN

WAIT

RETURN
```

That's workflow automation.

---

# This is the important realization.

The LLM is not your application.

It's one box.

Just like FFmpeg.

Just like OCR.

Just like Motion Detection.

Just like Whisper.

Just another executable.

---

# Here's what I think you should learn.

Not Python.

Not Go.

Not YAML.

Those are implementation details.

Learn the concept called:

## State Machines

This is the thing I think you've accidentally built.

Example.

```
Video

↓

Queued

↓

Transcribing

↓

Transcript Complete

↓

Needs Review

↓

Reviewed

↓

Corroborated

↓

Done
```

A video is always in ONE state.

Not twelve.

ONE.

---

Transitions move it.

```
Queued

↓

Run Tool

↓

Transcribing

↓

Tool Finished

↓

Transcript Complete
```

No guessing.

---

# Then learn one more thing.

## Job Queue

Think RV factory.

The floor doesn't yell:

> BUILD ME

The foreman hands out work orders.

```
Job #481

Type:
Transcribe

Input:
video.mp4

Status:
Queued
```

Worker picks it up.

Runs it.

Marks complete.

Next worker.

---

That's literally what your green button does.

It creates a work order.

---

# Then...

## Workflow Engine

This is where people start saying things like:

> Temporal

> Prefect

> Airflow

> Durable Functions

Ignore the buzzwords.

A workflow engine is simply...

```
Step 1

↓

Finished?

↓

Step 2

↓

Finished?

↓

Step 3

↓

Retry?

↓

Escalate?

↓

Done
```

That's it.

---

# Then...

## Event Bus

This sounds terrifying.

It isn't.

Imagine Vegas.

```
Clip Imported
```

An event happened.

Somebody listens.

```
New Video

↓

Create Transcript Job
```

Another event.

```
Transcript Finished

↓

Update UI
```

Another event.

```
Transcript Low Confidence

↓

Queue Validation
```

Everything is reacting.

Nobody is polling.

---

# I think your architecture becomes...

```
GUI

↓

Create Work Order

↓

Workflow Runtime

↓

State Machine

↓

Tool Runner

↓

becky-transcribe.exe

↓

JSON

↓

Decision Engine

↓

Next Step

↓

GUI Updates
```

Notice...

Claude isn't deciding this.

The runtime is.

---

# Here's the biggest thing I'd standardize.

Right now you have...

```
Tool

↓

Some orchestration

↓

Magic
```

I'd make it...

```
Tool

↓

Contract

↓

State

↓

Workflow

↓

Audit
```

Every single time.

---

# What do I think the runtime actually is?

Not AI.

It's basically...

```
Load workflow

↓

Read current state

↓

Execute current station

↓

Validate output

↓

Persist state

↓

Determine next station

↓

Repeat
```

That's all.

---

# This is why I don't actually think YAML matters.

YAML is just a blueprint.

JSON is just a clipboard.

Go is just steel.

The runtime is the factory manager.

---

# If I were giving you homework...

Not coding homework.

Concept homework.

I'd learn exactly four ideas.

### 1.

State Machines

Everything has ONE state.

---

### 2.

Work Orders

Everything is a job.

Never "do a thing."

Create a job.

---

### 3.

Workflow Runtime

Something executes jobs.

Not Claude.

A runtime.

---

### 4.

Event Driven Architecture

Nothing polls.

Everything reacts.

---

# And here's the thing that made me smile.

You asked:

> "What is actually happening when I click the green '+'?"

You expected a technical answer.

I think the answer is much simpler.

You're **issuing a work order**.

The runtime receives it.

The runtime consults the blueprint.

The runtime dispatches workstations.

The runtime inspects outputs.

The runtime advances the job.

The UI is simply watching the clipboard on the factory wall.

---

## One concrete recommendation

I actually think you should stop asking Claude Code to "build a feature."

Instead, give it a much narrower mission:

> **Build the factory manager.**

Not the workers.

Not the GUI.

Not the models.

The manager.

I'd define it in plain English before a single line of code exists:

* A work order is a JSON document with an ID, current state, inputs, outputs, and audit log.
* Every workstation is a deterministic executable (`becky-*.exe`) with a strict JSON-in / JSON-out contract.
* The workflow runtime is the **only** component allowed to decide what station runs next.
* Stations never invoke other stations directly. They finish their assigned work and return.
* The runtime evaluates the result against the protocol, records the outcome, queues the next station, or escalates.
* The UI never contains business logic. It only creates work orders, displays state, and sends user actions back to the runtime.

That architecture scales astonishingly well because every new capability becomes "add another workstation and update the blueprint," not "teach Claude another giant prompt."

And the thing that convinces me you're on the right track is that your favorite feature, the giant green "+" button, isn't really a transcription button at all.

It's the moment a user says:

> **"Create Work Order #1847 and let the factory take it from here."**

Everything after that should be boring, inspectable, repeatable, and almost completely unsurprising. That's exactly what good manufacturing feels like, and it's exactly what AI systems are missing today.

## User response:
webhooks and named pipes. any crossover whatsoever? my local agent guys can literally create tutorials for me based on this chat - but until web and local can communicate, you're both sandboxed how do i solve for that (barebones, direct approach). likely not an ai solution - piping

## AI response:
Funny enough... this is where the AI buzzword soup collides with 40 years of boring systems engineering. 😄

I actually think you're asking a simpler question than the AI world makes it sound.

> **How do two independent systems tell each other that work is ready?**

That's it.

Not AI.

Not agents.

Not MCP.

Not orchestration.

Just communication.

There are only a handful of fundamental patterns.

---

# Pattern 1: Command (what you already use)

```text
GUI

↓

becky-transcribe.exe

↓

JSON
```

One program launches another.

Vegas scripts work like this.

This is the simplest.

---

# Pattern 2: Named Pipes

Think of this as a permanent phone line.

```
GUI
═══════╗
        ║
        ║
Worker
```

Nobody launches anyone.

Both are already running.

One writes.

One reads.

Windows has supported this forever.

Visual Studio uses them.

SQL Server uses them.

Lots of Windows-native software uses them.

They're extremely low latency and great for **local** communication.

---

# Pattern 3: HTTP / REST

This is what almost every AI tool is secretly doing.

```
POST /transcribe

↓

JSON

↓

JSON
```

Claude Desktop.

Ollama.

OpenAI.

llama.cpp.

Everything speaks HTTP because it's dead simple.

---

# Pattern 4: WebSocket

Same idea as HTTP...

...except the connection never closes.

Perfect for:

```
Transcribing...

14%

28%

42%

Done
```

instead of polling every second.

---

# Pattern 5: Message Queue

Factory clipboard.

```
Job

↓

Queue

↓

Worker

↓

Complete
```

Nobody knows who the worker is.

Someone eventually grabs it.

---

# Pattern 6: File Watching

Honestly...

People underestimate this one.

```
Drop JSON

↓

Watch folder

↓

Process

↓

Write Result.json
```

It's ugly.

It's incredibly reliable.

Factories have literally done this with folders for decades.

---

# Webhooks

Now...

Webhooks are NOT pipes.

They're just...

> "Call me when you're done."

Example.

```
Local

↓

Finished

↓

POST

↓

https://...
```

That's it.

They only make sense when two computers can already reach each other.

They're notification mechanisms.

---

# Named Pipes

These are much closer to what I think you want.

```
Becky GUI

↓

Named Pipe

↓

Runtime

↓

Named Pipe

↓

Tool Runner
```

Everything stays local.

No browser.

No localhost server.

Just Windows talking to Windows.

---

# But...

Here's the part I think you're actually trying to solve.

You said:

> web and local can communicate

That's NOT really a pipe problem.

That's an **authority problem.**

Browsers are intentionally prisoners.

A webpage cannot suddenly execute

```
X:\AI-2\becky-tools\becky.exe
```

Imagine the chaos if it could. Every shady ad would become "Congratulations! Your antivirus has been replaced with a cryptocurrency miner." Browsers are paranoid because history forced them to be.

---

# So how do professionals solve it?

Exactly one bridge.

```
Browser

↓

Native Bridge

↓

Runtime

↓

Everything Else
```

Notice...

ONE bridge.

Not 50.

---

Chrome actually has this.

It's called **Native Messaging**.

Browser Extension

↓

Native Messaging Host

↓

Your EXE

That's literally why it exists.

---

# For Claude Code...

I'd do something even simpler.

```
Claude

↓

HTTP

↓

Runtime

↓

Named Pipe

↓

Worker

↓

JSON
```

Claude never talks to workers.

Only the runtime.

---

# This is where your "factory manager" comes back.

The runtime becomes...

The only thing everyone is allowed to talk to.

```
GUI
        │
Browser │
        │
Claude  │
        │
API     │
        │
────────▼────────

Runtime

────────▲────────

Tools

Workers

Models

Storage
```

Nobody skips the foreman.

---

# If I were standardizing your ecosystem...

I'd probably make this rule:

> **Every interaction, regardless of source, becomes a Work Order.**

Whether it comes from:

* GUI
* Browser
* Claude
* Becky
* another tool
* future phone app

doesn't matter.

Everything becomes:

```json
{
  "job":"transcribe",
  "input":"video.mp4",
  "state":"queued"
}
```

The runtime owns it from there.

---

## My prediction

I actually don't think your next breakthrough is another AI model.

It's building a tiny, boring Windows service.

Not glamorous.

No LLM.

No reasoning.

No embeddings.

Its entire job is:

* accept work orders
* launch the right executable
* track state
* expose one local API
* notify the UI

Everything, including Claude Code, browser extensions, your NLE, future AI assistants, and even automation tools, talks to **that one service**.

Ironically, it's the least "AI" part of the system that makes the AI feel reliable. It's the digital equivalent of the foreman with a clipboard. Nobody on the factory floor argues with the clipboard. The clipboard says what's next.
