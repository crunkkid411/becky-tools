// Package library is becky's persistent store of FAVORITES and TEMPLATES — the
// "give me my crunkcore starter / my favorite kit" backbone. It is deliberately
// small, deterministic, and UI-agnostic: the canvas, the CLIs, and the agent box all
// sit on the same store so a favorite starred anywhere shows up everywhere.
//
//   - Templates: named, saved arrangements (a full dawmodel.Arrangement) Jordan can
//     recall in one click — "my crunkcore starter", "four-on-the-floor house".
//   - Favorites: starred items by category (kit, sound, sample, genre, progression),
//     so the things he reaches for are one lookup away instead of buried in a drive.
//
// Stored as plain JSON under ~/.becky/library (override with BECKY_LIBRARY_DIR), so
// it survives across sessions — the whole point.
package library

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Favorite categories.
const (
	CatKit         = "kit"
	CatSound       = "sound"
	CatSample      = "sample"
	CatGenre       = "genre"
	CatProgression = "progression"
)

// ValidCategory reports whether c is a known favorite category.
func ValidCategory(c string) bool {
	switch c {
	case CatKit, CatSound, CatSample, CatGenre, CatProgression:
		return true
	}
	return false
}

// Favorite is one starred item.
type Favorite struct {
	Category string    `json:"category"`
	Value    string    `json:"value"`           // the thing (a path, a genre id, a progression)
	Label    string    `json:"label,omitempty"` // optional friendly name
	AddedAt  time.Time `json:"added_at"`
}

// TemplateMeta is the index entry for a saved arrangement.
type TemplateMeta struct {
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Genre     string    `json:"genre,omitempty"`
	Tracks    int       `json:"tracks"`
	Notes     int       `json:"notes"`
	BPM       int       `json:"bpm,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Library is a handle to the on-disk store.
type Library struct {
	dir string
	now func() time.Time // injectable for deterministic tests
}

// Open returns the default library (~/.becky/library, or $BECKY_LIBRARY_DIR).
func Open() (*Library, error) {
	dir := strings.TrimSpace(os.Getenv("BECKY_LIBRARY_DIR"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("library: locate home: %w", err)
		}
		dir = filepath.Join(home, ".becky", "library")
	}
	return OpenDir(dir), nil
}

// OpenDir returns a library rooted at dir (created lazily on first write).
func OpenDir(dir string) *Library {
	return &Library{dir: dir, now: time.Now}
}

func (l *Library) ensureDir(sub string) (string, error) {
	p := filepath.Join(l.dir, sub)
	if err := os.MkdirAll(p, 0o755); err != nil {
		return "", fmt.Errorf("library: mkdir %s: %w", p, err)
	}
	return p, nil
}

// ---- Favorites ----

func (l *Library) favPath() string { return filepath.Join(l.dir, "favorites.json") }

func (l *Library) loadFavorites() ([]Favorite, error) {
	data, err := os.ReadFile(l.favPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("library: read favorites: %w", err)
	}
	var favs []Favorite
	if err := json.Unmarshal(data, &favs); err != nil {
		return nil, fmt.Errorf("library: parse favorites: %w", err)
	}
	return favs, nil
}

func (l *Library) saveFavorites(favs []Favorite) error {
	if _, err := l.ensureDir("."); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(favs, "", "  ")
	if err := os.WriteFile(l.favPath(), data, 0o644); err != nil {
		return fmt.Errorf("library: write favorites: %w", err)
	}
	return nil
}

// Star adds (or updates the label of) a favorite. Idempotent on (category,value).
func (l *Library) Star(category, value, label string) error {
	category = strings.ToLower(strings.TrimSpace(category))
	value = strings.TrimSpace(value)
	if !ValidCategory(category) {
		return fmt.Errorf("library: unknown category %q (kit/sound/sample/genre/progression)", category)
	}
	if value == "" {
		return fmt.Errorf("library: cannot star an empty value")
	}
	favs, err := l.loadFavorites()
	if err != nil {
		return err
	}
	for i := range favs {
		if favs[i].Category == category && favs[i].Value == value {
			if label != "" {
				favs[i].Label = label
			}
			return l.saveFavorites(favs)
		}
	}
	favs = append(favs, Favorite{Category: category, Value: value, Label: label, AddedAt: l.now()})
	return l.saveFavorites(favs)
}

// Unstar removes a favorite by (category,value). Missing is not an error.
func (l *Library) Unstar(category, value string) error {
	category = strings.ToLower(strings.TrimSpace(category))
	value = strings.TrimSpace(value)
	favs, err := l.loadFavorites()
	if err != nil {
		return err
	}
	out := favs[:0]
	for _, f := range favs {
		if f.Category == category && f.Value == value {
			continue
		}
		out = append(out, f)
	}
	return l.saveFavorites(out)
}

// Favorites returns starred items in a category ("" = all), newest first.
func (l *Library) Favorites(category string) ([]Favorite, error) {
	category = strings.ToLower(strings.TrimSpace(category))
	favs, err := l.loadFavorites()
	if err != nil {
		return nil, err
	}
	var out []Favorite
	for _, f := range favs {
		if category == "" || f.Category == category {
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].AddedAt.After(out[j].AddedAt) })
	return out, nil
}
