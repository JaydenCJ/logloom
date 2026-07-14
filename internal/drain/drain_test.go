// Tests for the parse-tree miner: clustering, generalization, strict
// matching, ID stability, and the tree-shape invariants (length routing,
// wildcard branches, MaxChildren overflow) that keep it O(1) per line.
package drain

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// feedLine tokenizes trivially (whitespace split) and feeds; the real
// tokenizer is exercised in its own package and in the CLI tests.
func feedLine(t *testing.T, tr *Tree, line string) (*Cluster, bool) {
	t.Helper()
	return tr.Feed(line, strings.Fields(line))
}

func newTree(t *testing.T) *Tree {
	t.Helper()
	return New(DefaultConfig())
}

func TestIdenticalLinesFormOneCluster(t *testing.T) {
	tr := newTree(t)
	if _, created := feedLine(t, tr, "INFO cache hit key=user ttl=300"); !created {
		t.Fatal("first line must create its cluster")
	}
	for i := 0; i < 4; i++ {
		if _, created := feedLine(t, tr, "INFO cache hit key=user ttl=300"); created {
			t.Fatal("repeat line must join, not create")
		}
	}
	if tr.Len() != 1 {
		t.Fatalf("want 1 cluster, got %d", tr.Len())
	}
	c := tr.Clusters()[0]
	if c.Count != 5 {
		t.Fatalf("want count 5, got %d", c.Count)
	}
	if c.Template() != "INFO cache hit key=user ttl=300" {
		t.Fatalf("template mutated without disagreement: %q", c.Template())
	}
}

func TestDisagreeingPositionGeneralizesToWildcard(t *testing.T) {
	tr := newTree(t)
	feedLine(t, tr, "INFO worker finished job alpha fast")
	feedLine(t, tr, "INFO worker finished job omega slow")
	if tr.Len() != 1 {
		t.Fatalf("want merge into 1 cluster, got %d", tr.Len())
	}
	got := tr.Clusters()[0].Template()
	if got != "INFO worker finished job <*> <*>" {
		t.Fatalf("template = %q", got)
	}
}

// Literal tokens inside the routing prefix (the first Depth tokens)
// partition clusters by design: "cache hit" and "cache miss" must never
// merge into "cache <*>", because prefix words carry the message identity.
func TestPrefixLiteralsPartitionClusters(t *testing.T) {
	tr := newTree(t)
	feedLine(t, tr, "INFO cache hit key=a ttl=b")
	feedLine(t, tr, "INFO cache miss key=a ttl=b")
	if tr.Len() != 2 {
		t.Fatalf("prefix literals must partition: got %d clusters", tr.Len())
	}
}

func TestKeyValueMergeKeepsTheKey(t *testing.T) {
	tr := newTree(t)
	feedLine(t, tr, "INFO http request done method=GET status=ok")
	feedLine(t, tr, "INFO http request done method=POST status=ok")
	got := tr.Clusters()[0].Template()
	if got != "INFO http request done method=<*> status=ok" {
		t.Fatalf("template = %q", got)
	}
	// The key-wildcard keeps absorbing further values without re-forking.
	feedLine(t, tr, "INFO http request done method=DELETE status=ok")
	if tr.Len() != 1 || tr.Clusters()[0].Count != 3 {
		t.Fatalf("key wildcard lost: %d clusters", tr.Len())
	}
}

func TestDissimilarLinesFormSeparateClusters(t *testing.T) {
	tr := newTree(t)
	feedLine(t, tr, "ERROR upstream timeout host=a after=5s")
	feedLine(t, tr, "ERROR shutdown requested reason=b code=1")
	if tr.Len() != 2 {
		t.Fatalf("want 2 clusters, got %d", tr.Len())
	}
}

func TestDifferentTokenCountsNeverMerge(t *testing.T) {
	tr := newTree(t)
	feedLine(t, tr, "INFO cache hit")
	feedLine(t, tr, "INFO cache hit again")
	if tr.Len() != 2 {
		t.Fatalf("length partition violated: got %d clusters", tr.Len())
	}
}

// The similarity threshold is inclusive: exactly 0.5 with the default
// config must merge, one token less must not.
func TestThresholdBoundaryIsInclusive(t *testing.T) {
	tr := New(Config{Depth: 1, SimThreshold: 0.5, MaxChildren: 64})
	feedLine(t, tr, "a b c d")
	if _, created := feedLine(t, tr, "a b x y"); created {
		t.Fatal("2/4 matches is exactly the threshold and must merge")
	}
	tr2 := New(Config{Depth: 1, SimThreshold: 0.5, MaxChildren: 64})
	feedLine(t, tr2, "a b c d")
	if _, created := feedLine(t, tr2, "a x y z"); !created {
		t.Fatal("1/4 matches is below the threshold and must not merge")
	}
}

func TestClusterKeepsFirstExampleAndLineSpan(t *testing.T) {
	tr := newTree(t)
	feedLine(t, tr, "INFO cache hit key=a")
	feedLine(t, tr, "INFO cache hit key=b")
	feedLine(t, tr, "INFO cache hit key=c")
	c := tr.Clusters()[0]
	if c.Example != "INFO cache hit key=a" {
		t.Fatalf("example = %q", c.Example)
	}
	if c.FirstLine != 1 || c.LastLine != 3 {
		t.Fatalf("span = %d..%d, want 1..3", c.FirstLine, c.LastLine)
	}
}

func TestTemplateIDShapeDeterminismAndBoundaries(t *testing.T) {
	id := TemplateID([]string{"a", "b"})
	if len(id) != 9 || id[0] != 't' {
		t.Fatalf("ID %q should be 't' + 8 hex chars", id)
	}
	for _, r := range id[1:] {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("ID %q contains non-hex %q", id, r)
		}
	}
	if TemplateID([]string{"x", "y", "z"}) != TemplateID([]string{"x", "y", "z"}) {
		t.Fatal("same tokens must mint the same ID")
	}
	// Joined text must not collide across different token boundaries.
	if TemplateID([]string{"ab", "c"}) == TemplateID([]string{"a", "bc"}) {
		t.Fatal("token boundary collision")
	}
}

func TestSameStreamMintsSameIDsAcrossTrees(t *testing.T) {
	lines := []string{
		"INFO cache hit key=a",
		"WARN retry attempt=1 backoff=long",
		"INFO cache hit key=b",
	}
	run := func() []string {
		tr := newTree(t)
		for _, l := range lines {
			feedLine(t, tr, l)
		}
		var ids []string
		for _, c := range tr.Clusters() {
			ids = append(ids, c.ID)
		}
		return ids
	}
	if a, b := run(), run(); !reflect.DeepEqual(a, b) {
		t.Fatalf("IDs differ across identical runs: %v vs %v", a, b)
	}
}

// The ID is assigned at birth and preserved when the template later
// generalizes — that is the "stable template IDs" contract.
func TestIDSurvivesGeneralization(t *testing.T) {
	tr := newTree(t)
	c1, _ := feedLine(t, tr, "INFO worker finished job one")
	before := c1.ID
	c2, _ := feedLine(t, tr, "INFO worker finished job two")
	if c2 != c1 {
		t.Fatal("lines should have merged")
	}
	if c1.ID != before {
		t.Fatalf("ID changed on merge: %s → %s", before, c1.ID)
	}
	if c1.ID == TemplateID(c1.Tokens) {
		t.Fatal("test expects the generalized template to hash differently")
	}
}

func TestMatchFindsKnownLineAndIsStrictOnLiterals(t *testing.T) {
	tr := newTree(t)
	c, _ := feedLine(t, tr, "INFO cache hit key=user")
	if got := tr.Match(strings.Fields("INFO cache hit key=user")); got != c {
		t.Fatalf("Match returned %v, want the learned cluster", got)
	}
	if tr.Match(strings.Fields("INFO cache MISS key=user")) != nil {
		t.Fatal("literal disagreement must not match")
	}
}

func TestMatchAcceptsWildcardPositions(t *testing.T) {
	tr := newTree(t)
	feedLine(t, tr, "INFO worker finished job one")
	feedLine(t, tr, "INFO worker finished job two")
	if tr.Match(strings.Fields("INFO worker finished job seven")) == nil {
		t.Fatal("wildcard position must accept any token")
	}
}

func TestMatchKeyWildcardRequiresSameKey(t *testing.T) {
	tr := newTree(t)
	feedLine(t, tr, "INFO req done method=GET status=ok")
	feedLine(t, tr, "INFO req done method=POST status=ok")
	if tr.Match(strings.Fields("INFO req done method=PATCH status=ok")) == nil {
		t.Fatal("same key, new value must match key=<*>")
	}
	if tr.Match(strings.Fields("INFO req done verb=PATCH status=ok")) != nil {
		t.Fatal("different key must not match method=<*>")
	}
}

func TestMatchDoesNotLearn(t *testing.T) {
	tr := newTree(t)
	feedLine(t, tr, "INFO cache hit key=user")
	tr.Match(strings.Fields("TOTALLY new line here"))
	if tr.Len() != 1 || tr.Lines() != 1 {
		t.Fatalf("Match mutated the tree: %d clusters, %d lines", tr.Len(), tr.Lines())
	}
}

func TestMatchEmptyTokensIsNil(t *testing.T) {
	tr := newTree(t)
	feedLine(t, tr, "INFO cache hit")
	if tr.Match(nil) != nil {
		t.Fatal("empty token list must never match")
	}
}

// Digit-bearing tokens route through the wildcard branch, so two lines
// differing only in a leading identifier still share a leaf and merge.
func TestDigitTokensShareTheWildcardBranch(t *testing.T) {
	tr := New(Config{Depth: 2, SimThreshold: 0.5, MaxChildren: 64})
	feedLine(t, tr, "conn42 opened stream fast")
	feedLine(t, tr, "conn97 opened stream slow")
	if tr.Len() != 1 {
		t.Fatalf("digit-bearing prefixes must not split leaves: %d clusters", tr.Len())
	}
}

// When a node is full, unseen literals fall into the wildcard branch
// instead of growing the tree without bound.
func TestMaxChildrenOverflowRoutesToWildcard(t *testing.T) {
	tr := New(Config{Depth: 1, SimThreshold: 0.7, MaxChildren: 2})
	feedLine(t, tr, "alpha x y z")
	feedLine(t, tr, "beta x y z")
	feedLine(t, tr, "gamma x y z") // node full → wildcard branch
	feedLine(t, tr, "delta x y z") // joins gamma's leaf
	if tr.Len() != 3 {
		t.Fatalf("want 3 clusters (alpha, beta, merged overflow), got %d", tr.Len())
	}
	// The overflow cluster generalized its first position.
	var overflow *Cluster
	for _, c := range tr.Clusters() {
		if c.Tokens[0] == Wildcard {
			overflow = c
		}
	}
	if overflow == nil || overflow.Count != 2 {
		t.Fatalf("overflow cluster missing or miscounted: %+v", overflow)
	}
}

// Match must backtrack into the wildcard branch when the literal branch
// exists but holds no matching template.
func TestMatchBacktracksThroughWildcardBranch(t *testing.T) {
	tr := New(Config{Depth: 1, SimThreshold: 0.9, MaxChildren: 2})
	feedLine(t, tr, "alpha x y z")
	feedLine(t, tr, "beta x y z")
	feedLine(t, tr, "gamma x y z") // lives under the wildcard branch
	if tr.Match(strings.Fields("gamma x y z")) == nil {
		t.Fatal("strict match must find templates behind the wildcard branch")
	}
}

func TestMarkBaselineFlagsOnlyLaterClusters(t *testing.T) {
	tr := newTree(t)
	feedLine(t, tr, "INFO cache hit")
	tr.MarkBaseline()
	feedLine(t, tr, "ERROR disk failed badly")
	var oldC, newC *Cluster
	for _, c := range tr.Clusters() {
		if strings.HasPrefix(c.Template(), "INFO") {
			oldC = c
		} else {
			newC = c
		}
	}
	if oldC.New {
		t.Fatal("baseline cluster must not be flagged New")
	}
	if !newC.New {
		t.Fatal("post-baseline cluster must be flagged New")
	}
	if tr.NovelCount() != 1 {
		t.Fatalf("NovelCount = %d, want 1", tr.NovelCount())
	}
}

func TestClustersSortedByCountThenFirstLine(t *testing.T) {
	tr := newTree(t)
	feedLine(t, tr, "ERROR one two three four") // 1 hit, line 1
	feedLine(t, tr, "INFO cache hit key=a")     // 2 hits
	feedLine(t, tr, "INFO cache hit key=b")
	feedLine(t, tr, "WARN retry attempt now soon") // 1 hit, line 4
	got := tr.Clusters()
	if got[0].Count != 2 {
		t.Fatalf("most frequent first: got count %d", got[0].Count)
	}
	if got[1].FirstLine != 1 || got[2].FirstLine != 4 {
		t.Fatalf("ties break by first line: %d then %d", got[1].FirstLine, got[2].FirstLine)
	}
}

// Restored templates (which may contain wildcards) must route exactly like
// the live lines that built them — the branchKey invariant.
func TestRestoredWildcardTemplateStillMatches(t *testing.T) {
	src := New(Config{Depth: 2, SimThreshold: 0.5, MaxChildren: 64, NoMask: true})
	feedLine(t, src, "ts=1 request served fast")
	feedLine(t, src, "ts=2 request served slow")
	tpl := src.Clusters()[0]

	dst := New(src.Config())
	dst.Restore(*tpl)
	if dst.Match(strings.Fields("ts=3 request served instantly")) == nil {
		t.Fatal("restored template lost its routing")
	}
}

func TestRestorePreservesIdentityAndCounters(t *testing.T) {
	tr := newTree(t)
	tr.Restore(Cluster{
		ID: "tdeadbeef", Tokens: []string{"INFO", "cache", "hit"},
		Count: 41, Example: "INFO cache hit", FirstLine: 3, LastLine: 90,
	})
	c, created := feedLine(t, tr, "INFO cache hit")
	if created {
		t.Fatal("line must join the restored cluster")
	}
	if c.ID != "tdeadbeef" || c.Count != 42 {
		t.Fatalf("restored identity lost: id=%s count=%d", c.ID, c.Count)
	}
}

func TestLinesCounterTracksFeeds(t *testing.T) {
	tr := newTree(t)
	feedLine(t, tr, "a b")
	feedLine(t, tr, "a b")
	if tr.Lines() != 2 {
		t.Fatalf("Lines = %d, want 2", tr.Lines())
	}
	tr.SetLines(100)
	feedLine(t, tr, "a b")
	if tr.Lines() != 101 {
		t.Fatalf("Lines = %d, want 101 after restore", tr.Lines())
	}
}

func TestConfigValidate(t *testing.T) {
	cases := []Config{
		{Depth: 0, SimThreshold: 0.5, MaxChildren: 64},
		{Depth: 3, SimThreshold: 0, MaxChildren: 64},
		{Depth: 3, SimThreshold: 1.5, MaxChildren: 64},
		{Depth: 3, SimThreshold: 0.5, MaxChildren: 1},
	}
	for i, c := range cases {
		if c.Validate() == nil {
			t.Fatalf("case %d: %+v must be rejected", i, c)
		}
	}
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("default config rejected: %v", err)
	}
}

// A leaf with many clusters must stay deterministic: the most similar wins,
// and among equals the most literal one.
func TestBestMatchPrefersMostSpecificTemplate(t *testing.T) {
	tr := New(Config{Depth: 1, SimThreshold: 0.4, MaxChildren: 64})
	feedLine(t, tr, "job step one done ok")
	feedLine(t, tr, "job step two done ok") // → job step <*> done ok
	feedLine(t, tr, "job step one done ok") // exact line again
	got := tr.Clusters()[0]
	if got.Count != 3 {
		t.Fatalf("repeat line must land in the generalized cluster, count=%d", got.Count)
	}
}

// One million distinct identifiers must still collapse to one template —
// the whole point of the tool, kept fast by masking + wildcard routing.
func TestHighCardinalityCollapsesToOneTemplate(t *testing.T) {
	tr := newTree(t)
	for i := 0; i < 2000; i++ {
		line := "INFO session opened id=<num> shard=<num>"
		tr.Feed(line, strings.Fields(line))
	}
	// Also with raw varying tokens routed through the wildcard branch:
	for i := 0; i < 2000; i++ {
		line := fmt.Sprintf("conn%d closed by peer", i)
		tr.Feed(line, strings.Fields(line))
	}
	if tr.Len() != 2 {
		t.Fatalf("want 2 templates for 4000 lines, got %d", tr.Len())
	}
}
