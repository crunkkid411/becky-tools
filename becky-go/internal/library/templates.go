package library

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/dawmodel"
)

// templates.go: save/recall named arrangements. Each template body lives at
// templates/<slug>.json; an index (templates/index.json) carries the metadata so
// listing is cheap and stable.

// Slugify turns a template name into a filesystem-safe slug.
func Slugify(name string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func (l *Library) tmplDir() string   { return filepath.Join(l.dir, "templates") }
func (l *Library) indexPath() string { return filepath.Join(l.tmplDir(), "index.json") }

func (l *Library) loadIndex() ([]TemplateMeta, error) {
	data, err := os.ReadFile(l.indexPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("library: read template index: %w", err)
	}
	var idx []TemplateMeta
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("library: parse template index: %w", err)
	}
	return idx, nil
}

func (l *Library) saveIndex(idx []TemplateMeta) error {
	if _, err := l.ensureDir("templates"); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(idx, "", "  ")
	if err := os.WriteFile(l.indexPath(), data, 0o644); err != nil {
		return fmt.Errorf("library: write template index: %w", err)
	}
	return nil
}

// SaveTemplate stores an arrangement under a name (overwriting a same-slug template),
// recording metadata derived from the arrangement. Genre is an optional tag.
func (l *Library) SaveTemplate(name, genre string, arr *dawmodel.Arrangement) (TemplateMeta, error) {
	if strings.TrimSpace(name) == "" {
		return TemplateMeta{}, fmt.Errorf("library: template name is required")
	}
	if arr == nil {
		return TemplateMeta{}, fmt.Errorf("library: cannot save a nil arrangement")
	}
	slug := Slugify(name)
	if slug == "" {
		return TemplateMeta{}, fmt.Errorf("library: name %q has no usable characters", name)
	}
	if _, err := l.ensureDir("templates"); err != nil {
		return TemplateMeta{}, err
	}
	body, err := json.MarshalIndent(arr, "", "  ")
	if err != nil {
		return TemplateMeta{}, fmt.Errorf("library: encode arrangement: %w", err)
	}
	if err := os.WriteFile(filepath.Join(l.tmplDir(), slug+".json"), body, 0o644); err != nil {
		return TemplateMeta{}, fmt.Errorf("library: write template: %w", err)
	}
	meta := TemplateMeta{
		Name: name, Slug: slug, Genre: strings.TrimSpace(genre),
		Tracks: len(arr.Tracks), Notes: arr.NoteCount(), BPM: arr.BPM, CreatedAt: l.now(),
	}
	idx, err := l.loadIndex()
	if err != nil {
		return TemplateMeta{}, err
	}
	replaced := false
	for i := range idx {
		if idx[i].Slug == slug {
			idx[i] = meta
			replaced = true
			break
		}
	}
	if !replaced {
		idx = append(idx, meta)
	}
	if err := l.saveIndex(idx); err != nil {
		return TemplateMeta{}, err
	}
	return meta, nil
}

// LoadTemplate recalls an arrangement by name or slug.
func (l *Library) LoadTemplate(nameOrSlug string) (*dawmodel.Arrangement, TemplateMeta, error) {
	slug := Slugify(nameOrSlug)
	idx, err := l.loadIndex()
	if err != nil {
		return nil, TemplateMeta{}, err
	}
	var meta TemplateMeta
	found := false
	for _, m := range idx {
		if m.Slug == slug {
			meta, found = m, true
			break
		}
	}
	if !found {
		return nil, TemplateMeta{}, fmt.Errorf("library: no template named %q", nameOrSlug)
	}
	data, err := os.ReadFile(filepath.Join(l.tmplDir(), slug+".json"))
	if err != nil {
		return nil, TemplateMeta{}, fmt.Errorf("library: read template %q: %w", slug, err)
	}
	var arr dawmodel.Arrangement
	if err := json.Unmarshal(data, &arr); err != nil {
		return nil, TemplateMeta{}, fmt.Errorf("library: parse template %q: %w", slug, err)
	}
	return &arr, meta, nil
}

// ListTemplates returns the saved templates, newest first.
func (l *Library) ListTemplates() ([]TemplateMeta, error) {
	idx, err := l.loadIndex()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(idx, func(i, j int) bool { return idx[i].CreatedAt.After(idx[j].CreatedAt) })
	return idx, nil
}

// RemoveTemplate deletes a template (body + index entry). Missing is not an error.
func (l *Library) RemoveTemplate(nameOrSlug string) error {
	slug := Slugify(nameOrSlug)
	idx, err := l.loadIndex()
	if err != nil {
		return err
	}
	out := idx[:0]
	for _, m := range idx {
		if m.Slug != slug {
			out = append(out, m)
		}
	}
	_ = os.Remove(filepath.Join(l.tmplDir(), slug+".json"))
	return l.saveIndex(out)
}
