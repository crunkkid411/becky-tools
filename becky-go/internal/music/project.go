package music

// Project is the arrangement/routing manifest emitted next to the MIDI. It loads
// straight into SPEC-BECKY-CANVAS's deterministic audio DAG (nodes = tracks/buses/
// FX/sends; edges = audio or control/sidechain) so the song opens with sane routing
// — the 808 isolated on its own bus, the kick ducking the music bus and the 808,
// each as a single declared edge (one declaration, not 100 clicks). It is also a
// plain human-readable load manifest for any other DAW.
type Project struct {
	SchemaVersion int         `json:"schemaVersion"`
	Tool          string      `json:"tool"`
	Seed          int64       `json:"seed"`
	Genre         string      `json:"genre"`
	Tempo         int         `json:"tempo"`
	TimeSignature []int       `json:"timeSignature"`
	Key           ProjKey     `json:"key"`
	PPQ           int         `json:"ppq"`
	Progression   []string    `json:"progression"`
	Tracks        []ProjTrack `json:"tracks"`
	Buses         []ProjBus   `json:"buses"`
	Routing       []ProjEdge  `json:"routing"`
	Render        ProjRender  `json:"render"`
}

type ProjKey struct {
	Root  string `json:"root"`
	Scale string `json:"scale"`
}

type ProjTrack struct {
	ID      string `json:"id"`
	Midi    string `json:"midi"`
	Channel int    `json:"channel"`
	Kind    string `json:"kind"`
	Program int    `json:"program,omitempty"`
	Node    string `json:"node"`
	Out     string `json:"out"`
	Glide   bool   `json:"glide,omitempty"`
}

type ProjBus struct {
	ID  string   `json:"id"`
	Out string   `json:"out"`
	FX  []ProjFX `json:"fx,omitempty"`
}

type ProjFX struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type ProjEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
	Note string `json:"note"`
}

type ProjRender struct {
	Deterministic bool `json:"deterministic"`
	TopoSort      bool `json:"topoSort"`
}

// busFor routes a track to its bus: bass -> bus.808 (isolated low end),
// drums -> bus.drums, sfx -> bus.fx, everything pitched -> bus.music.
func busFor(name string) string {
	switch name {
	case "bass":
		return "bus.808"
	case "drums":
		return "bus.drums"
	case "sfx":
		return "bus.fx"
	default:
		return "bus.music"
	}
}

// buildProject assembles the routing manifest from the generated song + profile.
func buildProject(p Profile, s *Song) Project {
	proj := Project{
		SchemaVersion: 1,
		Tool:          "becky-compose",
		Seed:          s.Seed,
		Genre:         s.Genre,
		Tempo:         s.BPM,
		TimeSignature: []int{4, 4},
		Key:           ProjKey{Root: s.Root, Scale: s.Scale},
		PPQ:           s.TPQ,
		Progression:   s.Prog,
		Render:        ProjRender{Deterministic: true, TopoSort: true},
	}
	for _, nt := range s.Tracks {
		kind := "instrument"
		if nt.Name == "drums" {
			kind = "percussion"
		}
		proj.Tracks = append(proj.Tracks, ProjTrack{
			ID: nt.Name, Midi: nt.Name + ".mid", Channel: nt.Channel, Kind: kind,
			Program: progOrZero(nt.Program), Node: "src." + nt.Name, Out: busFor(nt.Name),
			Glide: p.Tracks[nt.Name].Glide,
		})
	}
	proj.Buses = []ProjBus{
		{ID: "bus.808", Out: "bus.master"},
		{ID: "bus.drums", Out: "bus.master"},
		{ID: "bus.music", Out: "bus.master", FX: []ProjFX{{Type: "compressor", ID: "comp.music"}}},
		{ID: "bus.fx", Out: "bus.master"},
		{ID: "bus.master", Out: "out.main"},
	}
	proj.Routing = []ProjEdge{
		{From: "src.drums.kick", To: "comp.music.sidechain", Kind: "sidechain", Note: "duck the music bus off the kick"},
		{From: "src.drums.kick", To: "bus.808.compressor.sidechain", Kind: "sidechain", Note: "808 ducks under the kick (mono-low discipline)"},
	}
	return proj
}

func progOrZero(p int) int {
	if p < 0 {
		return 0
	}
	return p
}
