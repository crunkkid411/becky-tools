package main

import (
	"encoding/json"
	"fmt"

	"becky-go/internal/facetrack"
)

// runSelftest is the offline proof required by HANDOFF-TEMPLATE.md: it exercises
// the REAL functions this binary runs (frame-time math, the facetrack wiring,
// the asd.py request/response contract, the speaker decision) with no network,
// no model, and no media, and asserts VALUES rather than "it ran".
func runSelftest() int {
	pass, fail := 0, 0
	check := func(name string, ok bool, detail string) {
		if ok {
			pass++
			fmt.Printf("  PASS  %s\n", name)
			return
		}
		fail++
		fmt.Printf("  FAIL  %s — %s\n", name, detail)
	}

	fmt.Println("becky-speaking --selftest (offline; no model, no media, no network)")

	// 1. Frame timestamps are TRUE-fps math, not an assumed rate.
	times := frameTimes(10.0, 30.0, 5)
	want := []float64{10.0, 10.0333333, 10.0666666, 10.1, 10.1333333}
	ok := len(times) == 5
	for i := range times {
		if i < len(want) && abs(times[i]-want[i]) > 1e-4 {
			ok = false
		}
	}
	check("frameTimes computes absolute timestamps at the video's real fps",
		ok, fmt.Sprintf("got %v", times))

	// 2. Two people, two tracks: synthetic detections at two well-separated
	//    screen positions with distinct embeddings must NOT merge into one
	//    track. This is the exact wiring this task closes — face_embed's
	//    --all-faces output -> facetrack.Build -> asd.py — proven end to end
	//    minus the two model calls (which selftest cannot make: rule 5).
	dets := twoPersonDetections(60, 30.0)
	tracks := facetrack.Build(dets, facetrack.DefaultOptions())
	check("two visible people yield exactly two tracks",
		len(tracks) == 2, fmt.Sprintf("got %d track(s)", len(tracks)))

	if len(tracks) == 2 {
		check("each track carries every frame of its person",
			len(tracks[0].Detections) == 60 && len(tracks[1].Detections) == 60,
			fmt.Sprintf("track A=%d track B=%d", len(tracks[0].Detections), len(tracks[1].Detections)))
	}

	// 3. The Go->asd.py request contract round-trips through JSON with the exact
	//    field names asd.py's docstring specifies ("id", "detections", "t", "bbox").
	if len(tracks) == 2 {
		in := asdTracksFromFaceTracks(tracks)
		data, err := json.Marshal(in)
		check("tracks request marshals without error", err == nil, fmt.Sprintf("%v", err))
		var raw map[string]any
		_ = json.Unmarshal(data, &raw)
		tlist, _ := raw["tracks"].([]any)
		check("request has one entry per track", len(tlist) == 2, fmt.Sprintf("got %d", len(tlist)))
		if len(tlist) > 0 {
			first, _ := tlist[0].(map[string]any)
			_, hasID := first["id"]
			_, hasDets := first["detections"]
			check("each track has id + detections keys", hasID && hasDets, fmt.Sprintf("%v", first))
			dlist, _ := first["detections"].([]any)
			if len(dlist) > 0 {
				d0, _ := dlist[0].(map[string]any)
				_, hasT := d0["t"]
				_, hasBBox := d0["bbox"]
				check("each detection has t + bbox keys", hasT && hasBBox, fmt.Sprintf("%v", d0))
			}
		}
	}

	// 4. The speaker decision: a clear lead is a CONCLUSION, naming the track.
	fA, fB := 0.82, 0.06
	winnerTracks := []trackOut{
		{ID: 1, SpeakingFrac: &fA},
		{ID: 2, SpeakingFrac: &fB},
	}
	id, conf := decideSpeaker(winnerTracks)
	check("a clip where one track talks throughout scores materially higher",
		id != nil && *id == 1 && conf == "conclusion",
		fmt.Sprintf("id=%v conf=%s", id, conf))

	// 5. A close call is reported honestly as ambiguous, never forced.
	fC, fD := 0.55, 0.50
	closeTracks := []trackOut{
		{ID: 1, SpeakingFrac: &fC},
		{ID: 2, SpeakingFrac: &fD},
	}
	id2, conf2 := decideSpeaker(closeTracks)
	check("two tracks within the margin are held as a candidate, not resolved",
		id2 == nil && conf2 == "candidate",
		fmt.Sprintf("id=%v conf=%s", id2, conf2))

	// 6. A single track that never clears the speaking floor is not "the
	//    speaker" by default just because it is the only face on screen.
	fE := 0.20
	quietTrack := []trackOut{{ID: 7, SpeakingFrac: &fE}}
	id3, conf3 := decideSpeaker(quietTrack)
	check("the only visible face is not assumed to be speaking",
		id3 == nil && conf3 == "candidate",
		fmt.Sprintf("id=%v conf=%s", id3, conf3))

	// 7. No scored track at all degrades to NONE, not a crash or a guess.
	id4, conf4 := decideSpeaker(nil)
	check("no scored tracks degrades to 'none' rather than panicking",
		id4 == nil && conf4 == "none",
		fmt.Sprintf("id=%v conf=%s", id4, conf4))

	// 8. joinTracks never turns "LR-ASD had no opinion" into "confirmed silent":
	//    a track with no asd.py entry at all must keep a nil SpeakingFrac.
	unscored := []facetrack.Track{{ID: 9, Detections: []facetrack.Detection{
		{Frame: 0, Time: 0, BBox: [4]float64{0, 0, 10, 10}},
		{Frame: 1, Time: 0.1, BBox: [4]float64{0, 0, 10, 10}},
	}}}
	joined := joinTracks(unscored, nil)
	check("a track ASD never scored keeps a nil speaking_frac, not a false zero",
		len(joined) == 1 && joined[0].SpeakingFrac == nil,
		fmt.Sprintf("%+v", joined))

	fmt.Printf("\n%d/%d PASS\n", pass, pass+fail)
	if fail > 0 {
		return 1
	}
	return 0
}

// twoPersonDetections builds n frames' worth of synthetic detections for two
// people standing on opposite sides of the frame, each with a stable, distinct
// embedding — enough for facetrack's IoU+cosine association to keep them apart
// without needing a real detector.
func twoPersonDetections(n int, fps float64) []facetrack.Detection {
	vecA := make([]float64, 8)
	vecB := make([]float64, 8)
	for i := range vecA {
		vecA[i] = 0
		vecB[i] = 0
	}
	vecA[0] = 1.0
	vecB[1] = 1.0

	var dets []facetrack.Detection
	for i := 0; i < n; i++ {
		t := float64(i) / fps
		dets = append(dets,
			facetrack.Detection{Frame: i, Time: t, BBox: [4]float64{100, 100, 300, 400}, Vector: vecA, DetScore: 0.9},
			facetrack.Detection{Frame: i, Time: t, BBox: [4]float64{900, 100, 1100, 400}, Vector: vecB, DetScore: 0.9},
		)
	}
	return dets
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
