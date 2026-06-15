// ingest.go — step A (PREPARE): read becky's existing evidence outputs into a
// flat, neutral observation list. Each Observation is one becky datapoint tied to
// an entity (person/place/device/event) with full provenance (source_file +
// sha256 + timestamp). No model, no network — pure Go.
//
// The readers are deliberately tolerant: a missing or garbled file is recorded as
// a note and skipped, never fatal (degrade-never-crash). The unit of co-occurrence
// downstream is the source_file (a "clip"), so every Observation carries one.
package palantir

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"becky-go/internal/pathx"
)

// Observation is one flat becky datapoint: an entity seen/heard in a clip, with
// provenance. This is the neutral intermediate the graph builders consume and the
// shape written to the OpenPlanter workspace's evidence.jsonl (SPEC §8a).
type Observation struct {
	RowID        string   `json:"row_id"`
	Kind         string   `json:"kind"` // person | place | device | event
	NodeID       string   `json:"node_id"`
	Label        string   `json:"label"`
	Status       string   `json:"status"` // documented | candidate (entity-level)
	Aliases      []string `json:"aliases,omitempty"`
	SourceFile   string   `json:"source_file"`
	SourceSHA256 string   `json:"source_sha256,omitempty"`
	Timestamp    float64  `json:"timestamp,omitempty"`
	Signal       string   `json:"signal"`
	Confidence   float64  `json:"confidence,omitempty"`
	From         string   `json:"from"`
	GpsLat       float64  `json:"gps_lat,omitempty"`
	GpsLon       float64  `json:"gps_lon,omitempty"`
	Device       string   `json:"device,omitempty"`
	Text         string   `json:"text,omitempty"`
}

// Ingest is the result of harvesting a corpus: the flat observations plus the
// files seen and any per-file degrade notes.
type Ingest struct {
	Observations  []Observation
	FilesIngested int
	Notes         []string
}

// --- becky-identify JSON ---------------------------------------------------

type identifyDoc struct {
	File            string `json:"file"`
	Identifications []struct {
		Type       string  `json:"type"`
		Name       string  `json:"name"`
		SpeakerID  string  `json:"speaker_id"`
		Confidence float64 `json:"confidence"`
	} `json:"identifications"`
}

// --- becky-events JSON -----------------------------------------------------

type eventsDoc struct {
	File   string `json:"file"`
	Events []struct {
		Type        string  `json:"type"`
		Timestamp   float64 `json:"timestamp"`
		Start       float64 `json:"start"`
		Confidence  float64 `json:"confidence"`
		Description string  `json:"description"`
	} `json:"events"`
}

// --- osint sidecar JSON (one per frame) ------------------------------------

type osintDoc struct {
	SourceFile   string  `json:"source_file"`
	SourceSHA256 string  `json:"source_sha256"`
	Timestamp    float64 `json:"timestamp"`
	GpsLat       float64 `json:"gps_lat"`
	GpsLon       float64 `json:"gps_lon"`
	Make         string  `json:"make"`
	Model        string  `json:"model"`
	Place        string  `json:"place"`
}

// --- becky-cluster JSON ----------------------------------------------------

type clusterDoc struct {
	Clusters []struct {
		ID      string `json:"id"`
		Label   string `json:"label"`
		Members []struct {
			SourceFile   string  `json:"source_file"`
			SourceSHA256 string  `json:"source_sha256"`
			Timestamp    float64 `json:"timestamp"`
			Confidence   float64 `json:"confidence"`
		} `json:"members"`
	} `json:"clusters"`
}

// readJSON reads and unmarshals a JSON file into v, wrapping errors with context.
func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", pathx.Base(path), err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("parse %s: %w", pathx.Base(path), err)
	}
	return nil
}

// AddIdentify folds a becky-identify JSON file into the ingest. Named persons
// become person observations (entity status documented); unnamed are not tracked.
func (in *Ingest) AddIdentify(path string) {
	var doc identifyDoc
	if err := readJSON(path, &doc); err != nil {
		in.Notes = append(in.Notes, err.Error())
		return
	}
	src := firstNonEmpty(doc.File, pathx.Base(path))
	in.FilesIngested++
	for i, id := range doc.Identifications {
		name := strings.TrimSpace(id.Name)
		if name == "" {
			continue
		}
		in.Observations = append(in.Observations, Observation{
			RowID:      fmt.Sprintf("identify:%s:%d", src, i),
			Kind:       KindPerson,
			NodeID:     personNodeID(name),
			Label:      name,
			Status:     StatusDocumented,
			SourceFile: src,
			Signal:     id.Type, // voice | face | location | corroborated
			Confidence: id.Confidence,
			From:       "identify.json",
		})
	}
}

// AddEvents folds a becky-events JSON file into the ingest. Each event becomes an
// event observation tied to its clip + timestamp.
func (in *Ingest) AddEvents(path string) {
	var doc eventsDoc
	if err := readJSON(path, &doc); err != nil {
		in.Notes = append(in.Notes, err.Error())
		return
	}
	src := firstNonEmpty(doc.File, pathx.Base(path))
	in.FilesIngested++
	for i, e := range doc.Events {
		ts := e.Timestamp
		if ts == 0 {
			ts = e.Start
		}
		label := firstNonEmpty(e.Description, e.Type)
		in.Observations = append(in.Observations, Observation{
			RowID:      fmt.Sprintf("events:%s:%d", src, i),
			Kind:       KindEvent,
			NodeID:     eventNodeID(src, e.Type, ts),
			Label:      label,
			Status:     StatusDocumented,
			SourceFile: src,
			Timestamp:  ts,
			Signal:     "events",
			Confidence: e.Confidence,
			From:       "events.json",
			Text:       e.Description,
		})
	}
}

// AddOSINT folds an osint sidecar JSON into the ingest, emitting a place node when
// GPS/place is present and a device node when EXIF make/model is present.
func (in *Ingest) AddOSINT(path string) {
	var doc osintDoc
	if err := readJSON(path, &doc); err != nil {
		in.Notes = append(in.Notes, err.Error())
		return
	}
	src := firstNonEmpty(doc.SourceFile, pathx.Base(path))
	in.FilesIngested++
	if doc.GpsLat != 0 || doc.GpsLon != 0 || doc.Place != "" {
		in.Observations = append(in.Observations, Observation{
			RowID:      fmt.Sprintf("osint-place:%s:%.3f", src, doc.Timestamp),
			Kind:       KindPlace,
			NodeID:     placeNodeID(doc.GpsLat, doc.GpsLon, doc.Place),
			Label:      placeLabel(doc.GpsLat, doc.GpsLon, doc.Place),
			Status:     StatusCandidate,
			SourceFile: src, SourceSHA256: doc.SourceSHA256, Timestamp: doc.Timestamp,
			Signal: "exif-gps", From: "osint", GpsLat: doc.GpsLat, GpsLon: doc.GpsLon,
		})
	}
	if doc.Make != "" || doc.Model != "" {
		dev := strings.TrimSpace(doc.Make + " " + doc.Model)
		in.Observations = append(in.Observations, Observation{
			RowID:      fmt.Sprintf("osint-device:%s:%.3f", src, doc.Timestamp),
			Kind:       KindDevice,
			NodeID:     deviceNodeID(dev),
			Label:      dev,
			Status:     StatusDocumented,
			SourceFile: src, SourceSHA256: doc.SourceSHA256, Timestamp: doc.Timestamp,
			Signal: "exif-make-model", From: "osint", Device: dev,
		})
	}
}

// AddCluster folds a becky-cluster JSON into the ingest. Each cluster is one
// recurring-unknown person (status candidate — no name yet); each member is an
// appearance tied to a clip.
func (in *Ingest) AddCluster(path string) {
	var doc clusterDoc
	if err := readJSON(path, &doc); err != nil {
		in.Notes = append(in.Notes, err.Error())
		return
	}
	in.FilesIngested++
	for _, c := range doc.Clusters {
		label := firstNonEmpty(c.Label, "Person "+c.ID+" (unnamed)")
		nid := "person:cluster-" + sanitizeID(c.ID)
		for i, m := range c.Members {
			src := firstNonEmpty(m.SourceFile, pathx.Base(path))
			in.Observations = append(in.Observations, Observation{
				RowID:      fmt.Sprintf("cluster:%s:%d", c.ID, i),
				Kind:       KindPerson,
				NodeID:     nid,
				Label:      label,
				Status:     StatusCandidate,
				SourceFile: src, SourceSHA256: m.SourceSHA256, Timestamp: m.Timestamp,
				Signal: "face-cluster", Confidence: m.Confidence, From: "cluster.json",
			})
		}
	}
}

// Sort orders observations deterministically (row_id) so the prepared dataset and
// its hash are reproducible regardless of file-discovery order.
func (in *Ingest) Sort() {
	sort.SliceStable(in.Observations, func(i, j int) bool {
		return in.Observations[i].RowID < in.Observations[j].RowID
	})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
