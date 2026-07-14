// Tests for state persistence: round-trips must preserve template identity
// exactly, and loads must reject anything that is not a valid logloom
// state file — silent misparses would corrupt every later run.
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JaydenCJ/logloom/internal/drain"
)

func buildTree(t *testing.T) *drain.Tree {
	t.Helper()
	tr := drain.New(drain.DefaultConfig())
	lines := []string{
		"INFO cache hit key=a ttl=b",
		"INFO cache hit key=c ttl=d",
		"ERROR upstream timeout host=x after=y",
	}
	for _, l := range lines {
		tr.Feed(l, strings.Fields(l))
	}
	return tr
}

func tmpState(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state.json")
}

func TestSaveLoadRoundTripPreservesEverything(t *testing.T) {
	src := buildTree(t)
	path := tmpState(t)
	if err := Save(path, src); err != nil {
		t.Fatalf("save: %v", err)
	}
	dst, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if dst.Lines() != src.Lines() {
		t.Fatalf("lines: %d != %d", dst.Lines(), src.Lines())
	}
	if dst.Config() != src.Config() {
		t.Fatalf("config: %+v != %+v", dst.Config(), src.Config())
	}
	a, b := src.Clusters(), dst.Clusters()
	if len(a) != len(b) {
		t.Fatalf("cluster count: %d != %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Count != b[i].Count ||
			a[i].Template() != b[i].Template() || a[i].Example != b[i].Example ||
			a[i].FirstLine != b[i].FirstLine || a[i].LastLine != b[i].LastLine {
			t.Fatalf("cluster %d differs:\n%+v\n%+v", i, a[i], b[i])
		}
	}
}

func TestLoadedTreeContinuesClusteringIntoSameTemplates(t *testing.T) {
	path := tmpState(t)
	if err := Save(path, buildTree(t)); err != nil {
		t.Fatalf("save: %v", err)
	}
	tr, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	tr.MarkBaseline()
	line := "INFO cache hit key=zz ttl=qq"
	c, created := tr.Feed(line, strings.Fields(line))
	if created {
		t.Fatal("known pattern must join its restored cluster, not fork")
	}
	if c.Count != 3 {
		t.Fatalf("restored count should continue: got %d, want 3", c.Count)
	}
	if tr.NovelCount() != 0 {
		t.Fatalf("nothing novel was fed, NovelCount = %d", tr.NovelCount())
	}
}

func TestSaveIsAtomicOverwriteAndLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := Save(path, buildTree(t)); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := Save(path, buildTree(t)); err != nil {
		t.Fatalf("overwrite save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Fatalf("directory should hold exactly state.json, got %v", entries)
	}
}

func TestStateFileIsStableJSONWithClustersInFirstSeenOrder(t *testing.T) {
	path := tmpState(t)
	if err := Save(path, buildTree(t)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		SchemaVersion int    `json:"schema_version"`
		Tool          string `json:"tool"`
		Clusters      []struct {
			FirstLine int64 `json:"first_line"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("state file is not valid JSON: %v", err)
	}
	if f.SchemaVersion != SchemaVersion || f.Tool != "logloom" {
		t.Fatalf("envelope wrong: %+v", f)
	}
	for i := 1; i < len(f.Clusters); i++ {
		if f.Clusters[i].FirstLine < f.Clusters[i-1].FirstLine {
			t.Fatal("clusters must be stored in first-seen order")
		}
	}
}

func TestLoadMissingFileFails(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("missing file must be an error")
	}
}

func TestLoadCorruptJSONFails(t *testing.T) {
	path := tmpState(t)
	os.WriteFile(path, []byte("{not json"), 0o644)
	if _, err := Load(path); err == nil {
		t.Fatal("corrupt JSON must be an error")
	}
}

func TestLoadForeignToolMarkerFails(t *testing.T) {
	path := tmpState(t)
	os.WriteFile(path, []byte(`{"schema_version":1,"tool":"other","config":{"depth":3,"sim_threshold":0.5,"max_children":64},"clusters":[]}`), 0o644)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "not a logloom state file") {
		t.Fatalf("foreign tool marker must be rejected, got %v", err)
	}
}

func TestLoadUnsupportedSchemaVersionFails(t *testing.T) {
	path := tmpState(t)
	os.WriteFile(path, []byte(`{"schema_version":99,"tool":"logloom","config":{"depth":3,"sim_threshold":0.5,"max_children":64},"clusters":[]}`), 0o644)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("future schema must be rejected, got %v", err)
	}
}

func TestLoadBadConfigFails(t *testing.T) {
	path := tmpState(t)
	os.WriteFile(path, []byte(`{"schema_version":1,"tool":"logloom","config":{"depth":0,"sim_threshold":0.5,"max_children":64},"clusters":[]}`), 0o644)
	if _, err := Load(path); err == nil {
		t.Fatal("invalid config in state must be rejected")
	}
}

func TestLoadRejectsClusterMissingIDOrTemplate(t *testing.T) {
	path := tmpState(t)
	os.WriteFile(path, []byte(`{"schema_version":1,"tool":"logloom","config":{"depth":3,"sim_threshold":0.5,"max_children":64},"clusters":[{"id":"","template":"a b","count":1}]}`), 0o644)
	if _, err := Load(path); err == nil {
		t.Fatal("cluster without id must be rejected")
	}
}
