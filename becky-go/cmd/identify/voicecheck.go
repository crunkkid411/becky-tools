// voicecheck.go - when two speakers in one video sound about equally like an enrolled
// person, becky does not stop at "not settled" (2026-10-11, Jordan: "when confidence
// scores are low, we verify with another model / datapoint, escalating as needed").
// Each step runs only when the one before did not settle it:
//  1. look: Gemma-4 E4B sees the person's enrolled photo next to frames taken while each
//     tied speaker talks - "is the person talking here this person?";
//  2. the words again: Gemma-4 12B rereads the transcript (E4B already read it);
//  3. ask Jordan: "Who is talking here?" in the who-is-this window, with the moment to play.
//     His answer settles it on the next run (`redimnet+jordan`).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"becky-go/internal/avlm"
	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/talkingface"
	"becky-go/internal/whois"
)

// escalateTies works through each tie and returns a pick for every one it settled, plus
// plain words for notes.voice_checks.
func escalateTies(cfg config.Config, input string, kb Knowledge, ties []voiceTie, lines []tLine, opts voiceOptions) ([]contextPick, []string) {
	var picks []contextPick
	var notes []string
	for _, t := range ties {
		p, note := settleTie(cfg, input, kb, t, lines, opts)
		notes = append(notes, note)
		if p != nil {
			picks = append(picks, *p)
		}
	}
	return picks, notes
}

func settleTie(cfg config.Config, input string, kb Knowledge, t voiceTie, lines []tLine, opts voiceOptions) (*contextPick, string) {
	var tried []string

	// 1. look
	if refs := refPhotos(kb, t.Key); len(refs) > 0 {
		var yes []string
		for _, l := range t.Labels {
			ans, by := lookAtSpeaker(cfg, input, t.Name, refs, t.Spans[l], opts.verbose)
			beckyio.Logf(opts.verbose, "voice check: %s talking = %s? %s: %s", l, t.Name, by, ans)
			tried = append(tried, fmt.Sprintf("the picture while %s talks: %s (%s)", l, ans, by))
			if ans == "yes" {
				yes = append(yes, l)
			}
		}
		if len(yes) == 1 {
			return &contextPick{Name: t.Name, Labels: yes, Confidence: "high", Via: "look",
				Evidence: "seen talking"}, t.Name + ": settled by looking - " + strings.Join(tried, "; ")
		}
	} else {
		tried = append(tried, "no enrolled photo to look with")
	}
	if t.Named {
		return nil, t.Name + ": kept the clear voice - " + strings.Join(tried, "; ")
	}

	// 2. the words again, on the bigger model (only for a voice tie: when the words already
	// disagree with the voice, rereading the same words adds nothing)
	if !t.Dispute && len(lines) > 0 && cfg.GemmaModel12B != "" && fileExists(cfg.GemmaModel12B) {
		h := personHint{Name: t.Name}
		if ent, ok := kb.Entities[t.Key]; ok {
			h.Aliases = ent.Aliases
			if !strings.HasPrefix(ent.Description, "Enrolled from") {
				h.About = ent.Description
			}
		}
		beckyio.Logf(opts.verbose, "voice check: Gemma-4 12B rereads the transcript for %s...", t.Name)
		picks, note := askContextWith(cfg, cfg.GemmaModel12B, []personHint{h}, lines, opts.verbose)
		if note != "" {
			tried = append(tried, "the words (12B): "+note)
		}
		for _, p := range picks {
			if strings.EqualFold(p.Name, t.Name) && p.sure() && len(p.Labels) == 1 && contains(t.Labels, p.Labels[0]) {
				p.Name = t.Name
				p.Via = "context"
				return &p, t.Name + ": settled by Gemma-4 12B reading the words (" + p.Evidence + ")"
			}
		}
		tried = append(tried, "the words (12B): not sure")
	}

	// 3. ask Jordan (or read his earlier answer)
	if !opts.ask {
		return nil, t.Name + ": not settled - " + strings.Join(tried, "; ")
	}
	top := t.Labels[0]
	video, _ := filepath.Abs(input)
	a, answered, waiting, err := whois.LookupSpeaker(video, t.Name, top)
	switch {
	case err != nil:
	case answered && strings.EqualFold(a.Answer, t.Name):
		return &contextPick{Name: t.Name, Labels: []string{top}, Confidence: "high", Via: "jordan",
			Evidence: "Jordan said so"}, t.Name + ": Jordan said " + top + " is them"
	case answered:
		return nil, fmt.Sprintf("%s: Jordan said %s is %s, not them", t.Name, top, a.Answer)
	case waiting:
		return nil, t.Name + ": waiting for Jordan's answer in the who-is-this window"
	default:
		sp := longest(t.Spans[top])
		at := math.Round((sp.Start+sp.End)/2*100) / 100
		frame := grabFrame(cfg.FFmpeg, input, at)
		kbDir, _ := filepath.Abs(kb.Dir)
		_, _, err = whois.Ask(whois.Question{Video: video, At: at, Guess: t.Name, Speaker: top, Frame: frame, KB: kbDir,
			People: whoPeople(kb), Checked: t.Why + "; " + strings.Join(tried, "; ")})
		if frame != "" {
			os.Remove(frame)
		}
		if err == nil {
			return nil, t.Name + ": asked Jordan who is talking (" + strings.Join(tried, "; ") + ")"
		}
	}
	return nil, t.Name + ": not settled - " + strings.Join(tried, "; ")
}

// refPhotos is up to 2 of the person's enrolled face pictures.
func refPhotos(kb Knowledge, key string) []string {
	for _, f := range kb.Faces {
		if f.Key == key && len(f.Faces) > 0 {
			if len(f.Faces) > 2 {
				return f.Faces[:2]
			}
			return f.Faces
		}
	}
	return nil
}

// lookAtSpeaker shows Gemma-4 E4B the reference photos and 2 frames from the middle of the
// speaker's longest stretches: "yes" | "no" | "unsure".
func lookAtSpeaker(cfg config.Config, input, name string, refs []string, spans []SpeakerSpan, verbose bool) (string, string) {
	s := append([]SpeakerSpan(nil), spans...)
	sort.SliceStable(s, func(i, j int) bool { return s[i].End-s[i].Start > s[j].End-s[j].Start })
	var frames []string
	for i := 0; i < len(s) && len(frames) < 2; i++ {
		if f := grabFrame(cfg.FFmpeg, input, (s[i].Start+s[i].End)/2); f != "" {
			frames = append(frames, f)
			defer os.Remove(f)
		}
	}
	if len(frames) == 0 {
		return "unsure", ""
	}
	// LR-ASD (becky-speaking) finds the face whose lips move with the sound: Gemma then
	// compares THAT face, instead of guessing who is talking in a frame with several people.
	talking := ""
	if len(s) > 0 {
		crop, why := talkingface.Crop(cfg.FFmpeg, input, s[0].Start, s[0].End)
		beckyio.Logf(verbose, "voice check: talking face: %s%s", crop, why)
		if crop != "" {
			talking = crop
			defer os.Remove(crop)
			// the face model compares the talking face with the enrolled photos first;
			// Gemma is only asked when that is unclear
			ans, sim := talkingface.Compare(cfg, []string{crop}, refs)
			beckyio.Logf(verbose, "voice check: talking face vs enrolled photos: %.2f -> %s", sim, ans)
			if ans != "unsure" {
				return ans, fmt.Sprintf("the talking face matched the photos %.2f", sim)
			}
		}
	}
	prompt := fmt.Sprintf(`The first %d picture(s) show %s. The last %d picture(s) were taken from a video while one speaker was talking.
In the last pictures, find the person who is talking (mouth open mid-word, or the only person shown). Is the person TALKING the same person as in the first pictures? Compare the face first, then hair, tattoos, piercings and build. If nobody is clearly talking, or you cannot tell, answer "unsure".
Reply with JSON only: {"same": "yes" | "no" | "unsure", "why": "<short>"}`, len(refs), name, len(frames))
	if talking != "" {
		frames = append([]string{talking}, frames[:1]...)
		prompt = fmt.Sprintf(`The first %d picture(s) show %s. The next picture is a close-up of the face that was TALKING in a video (found by matching lip movement to the sound); the last picture is the whole frame around it.
Is the talking face the same person as in the first pictures? Compare the face first, then hair, tattoos, piercings and build. If you cannot tell, answer "unsure".
Reply with JSON only: {"same": "yes" | "no" | "unsure", "why": "<short>"}`, len(refs), name)
	}
	r := avlm.New(cfg.GemmaModel, cfg.GemmaMMProj, cfg.LlamaServer, "", cfg.FFmpeg, cfg.FFprobe, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	res, err := r.AnalyzeImages(ctx, append(append([]string(nil), refs...), frames...),
		avlm.ImageOptions{Prompt: prompt, MaxTokens: 200, Temperature: 0.01})
	if err != nil {
		beckyio.Logf(verbose, "voice check: Gemma could not look: %v", err)
		return "unsure", ""
	}
	return parseSame(res.Text), "Gemma-4 E4B looked"
}

func parseSame(raw string) string {
	i, j := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
	if i < 0 || j <= i {
		return "unsure"
	}
	var v struct {
		Same string `json:"same"`
	}
	if json.Unmarshal([]byte(raw[i:j+1]), &v) != nil {
		return "unsure"
	}
	if s := strings.ToLower(strings.TrimSpace(v.Same)); s == "yes" || s == "no" {
		return s
	}
	return "unsure"
}

// grabFrame saves one frame at t seconds as a temp JPEG ("" when it fails).
func grabFrame(ffmpeg, input string, t float64) string {
	f, err := os.CreateTemp("", "becky_voicelook_*.jpg")
	if err != nil {
		return ""
	}
	p := f.Name()
	f.Close()
	cmd := exec.Command(ffmpeg, "-ss", fmt.Sprintf("%.2f", t), "-i", input, "-frames:v", "1",
		"-vf", "scale=640:-2", "-q:v", "3", "-y", "-loglevel", "error", p)
	if cmd.Run() != nil {
		os.Remove(p)
		return ""
	}
	if st, err := os.Stat(p); err != nil || st.Size() == 0 {
		os.Remove(p)
		return ""
	}
	return p
}

func longest(spans []SpeakerSpan) SpeakerSpan {
	var best SpeakerSpan
	for _, s := range spans {
		if s.End-s.Start > best.End-best.Start {
			best = s
		}
	}
	return best
}
