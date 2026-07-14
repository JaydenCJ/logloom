// Package state persists a drain.Tree to a versioned JSON file and loads it
// back, so template IDs and counts survive across runs. Writes are atomic
// (temp file + rename) and loads are strict: an unknown schema version or a
// foreign tool marker is an error, never a silent misparse.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JaydenCJ/logloom/internal/drain"
)

// SchemaVersion is bumped whenever the file layout changes incompatibly.
const SchemaVersion = 1

// toolMarker guards against feeding logloom a state file written by some
// other JSON-emitting tool.
const toolMarker = "logloom"

type clusterRecord struct {
	ID        string `json:"id"`
	Template  string `json:"template"`
	Count     int64  `json:"count"`
	Example   string `json:"example"`
	FirstLine int64  `json:"first_line"`
	LastLine  int64  `json:"last_line"`
}

type file struct {
	SchemaVersion int             `json:"schema_version"`
	Tool          string          `json:"tool"`
	Config        drain.Config    `json:"config"`
	Lines         int64           `json:"lines"`
	Clusters      []clusterRecord `json:"clusters"`
}

// Save writes the tree to path atomically. Clusters are stored in
// first-seen order so a Load replays the original insertion order and the
// rebuilt tree routes lines identically.
func Save(path string, t *drain.Tree) error {
	clusters := t.Clusters()
	sort.SliceStable(clusters, func(i, j int) bool {
		return clusters[i].FirstLine < clusters[j].FirstLine
	})
	f := file{
		SchemaVersion: SchemaVersion,
		Tool:          toolMarker,
		Config:        t.Config(),
		Lines:         t.Lines(),
		Clusters:      make([]clusterRecord, 0, len(clusters)),
	}
	for _, c := range clusters {
		f.Clusters = append(f.Clusters, clusterRecord{
			ID:        c.ID,
			Template:  c.Template(),
			Count:     c.Count,
			Example:   c.Example,
			FirstLine: c.FirstLine,
			LastLine:  c.LastLine,
		})
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".logloom-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Load reads a state file and rebuilds the tree, preserving cluster IDs,
// counts, and examples. The tree's tuning comes from the file — the run
// that wrote the state decided how its templates were mined.
func Load(path string) (*drain.Tree, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Tool != toolMarker {
		return nil, fmt.Errorf("%s: not a logloom state file (tool=%q)", path, f.Tool)
	}
	if f.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%s: unsupported schema_version %d (this build reads %d)",
			path, f.SchemaVersion, SchemaVersion)
	}
	if err := f.Config.Validate(); err != nil {
		return nil, fmt.Errorf("%s: bad config: %w", path, err)
	}
	t := drain.New(f.Config)
	for _, rec := range f.Clusters {
		if rec.ID == "" || rec.Template == "" {
			return nil, fmt.Errorf("%s: cluster record missing id or template", path)
		}
		t.Restore(drain.Cluster{
			ID:        rec.ID,
			Tokens:    strings.Split(rec.Template, " "),
			Count:     rec.Count,
			Example:   rec.Example,
			FirstLine: rec.FirstLine,
			LastLine:  rec.LastLine,
		})
	}
	t.SetLines(f.Lines)
	return t, nil
}
