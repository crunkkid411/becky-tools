package main

// video.go - --video: the same workflow for a YouTube video Jordan recorded to
// post, not a livestream (Jordan, 2026-10-12: "this footage was NOT a
// livestream... should be treated the same way as the livestream edits (minus
// the live-chat considerations)"). Everything he says to his viewers is the
// content; what goes is the clap and getting ready, flubs he says again, what
// he tells the editor (instructions.go follows those), and filler. Only the
// words the models read change here - every step and check is the same.

// recWords is how the prompts name the recording.
type recWords struct {
	thing, upper, title, s1Kind, s1State, s1Keep string
}

var rec = recWords{
	thing:   "livestream",
	upper:   "STREAM",
	title:   "Livestream edit",
	s1Kind:  "Hair Jordan is livestreaming.",
	s1State: "A livestream recording by Hair Jordan, a YouTuber, being edited down to the topics the editor asks for.",
	s1Keep:  "`sentence` belongs in the edit: Jordan is talking about a topic the editor's `guidance` asks to keep - not reading or answering chat, thanking for a super chat, taking a break, starting a sentence he then says again, or doing stream housekeeping. A sentence counts while he is still on that topic, even when it does not name it (`before` is what he said just before).",
}

const defaultVideoGuidance = "Keep everything Jordan says to his viewers. Cut the clap and getting ready before he starts, false starts and flubbed sentences he says again (keep the attempt he finishes), whatever he tells the editor to cut, his instructions to the editor, and anything after he says goodbye."

// fixedTopics, when set, replaces the wanted topics a model reads from the
// guidance. A video has one: on 2026-10-12 Gemma read "Cut the clap and getting
// ready" in the video guidance as a topic to KEEP.
var fixedTopics []string

// useVideoWords switches every prompt to a YouTube video recording.
func useVideoWords() {
	rec = recWords{
		thing:   "video recording",
		upper:   "RECORDING",
		title:   "YouTube video edit",
		s1Kind:  "Hair Jordan is recording a YouTube video.",
		s1State: "The raw camera recording of a YouTube video by Hair Jordan, being edited into the video he will post.",
		s1Keep:  "`sentence` belongs in the finished video: Jordan is saying it to his viewers - not talking to his video editor (cut this, move this, put this part at the beginning), not getting ready or wrapping up the recording, not mumbling to himself, and not a sentence he breaks off and then says again. (`before` is what he said just before; `guidance` is what the editor was asked to keep.)",
	}
	fixedTopics = []string{"Anything Jordan says to his viewers"}
	labels = []string{"narrative", "retake", "instruction", "meta", "break"}
	labelDesc = map[string]string{
		"narrative":   "Jordan talking to his viewers (the real content, including his intro and goodbye)",
		"retake":      "a false start, flubbed or broken sentence that he says again right after",
		"instruction": "talking to his video editor, not the viewers: cut this, move this, put this part at the beginning (\"I forgot to mention\" said to the viewers is narrative)",
		"meta":        "getting ready or wrapping up the recording: the clap, checking the camera or mic, is it recording, after the goodbye",
		"break":       "a pause, drinking, mumbling to himself, filler while nothing happens",
	}
	editorRole = `You are the video editor for Hair Jordan, a YouTuber. You are editing the raw camera recording of one of his YouTube videos into the finished video he will post. You get the transcript one sentence at a time and decide, for every sentence, whether it stays in the edit.

Everything Jordan says to his viewers is the content and stays. The editor's GUIDANCE says what to keep and what to cut.

Give every sentence exactly one label:
- narrative: Jordan talking to his viewers (the real content, including his intro, his asides, his jokes and his goodbye)
- retake: a false start, flubbed or broken sentence that Jordan says again right after
- instruction: Jordan talking to his video EDITOR instead of the viewers, e.g. "cut that out", "put this part earlier", "whoever edits this, that goes at the beginning", "that goes before the part about...". "Oh wait, I forgot to mention" said to the viewers, followed by more of his story, is narrative.
- meta: getting ready or wrapping up the recording: the clap, checking the camera or microphone, "is it recording", "okay, let's go", what he does after the goodbye
- break: a pause, drinking, mumbling to himself, filler while nothing happens`
	outlinePrompt = `You summarize one part of the transcript of a YouTube video by Hair Jordan, a YouTuber, in one short line that names the topics he talks about.`
	systemPrompt = editorRole + topicRule
	readPrompt = `You are the video editor for Hair Jordan, a YouTuber. You are checking the edit of one of his YouTube videos. You get ONLY the sentences still in the edit, in the order the viewer will hear them. "~ a part was cut here ~" shows where something was taken out between two lines.

The finished video will be posted on YouTube. Everything Jordan says to his viewers stays: his story, opinions, reasons, examples, jokes, asides, rhetorical questions to the camera, short reactions that carry his point, his intro and his goodbye.

Cut a line only when it clearly is one of these:
- talking to his video editor instead of the viewers: "cut that", "put this earlier", "whoever edits this, that goes at the beginning", "edit this out". ("Oh wait, I forgot to mention" said to the viewers is part of his story and stays.)
- getting ready or wrapping up the recording: the clap, checking the camera or microphone, "is it recording", what he does after the goodbye.
- a flubbed or broken sentence that he says again right after (keep the attempt he finishes), or a fragment that makes no sense next to the lines around it.
- mumbling to himself or filling time, with nothing in it: "um", "ugh", "okay", "where was I".
When unsure, keep the line.`
}
