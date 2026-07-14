// Package drain implements online log template mining with a fixed-depth
// parse tree, in the spirit of the Drain algorithm (He et al., ICWS 2017).
//
// Lines are first partitioned by token count, then routed through up to
// Depth levels keyed on their leading tokens (digit-bearing tokens route
// through a wildcard branch so identifiers never explode the tree). Each
// leaf holds a small list of clusters; a line joins the most similar
// cluster above the threshold, generalizing differing positions to the
// wildcard "<*>", or founds a new cluster. Every operation is O(depth +
// clusters-per-leaf) per line, so the tree streams through millions of
// lines in constant memory relative to template count.
package drain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Wildcard is the untyped placeholder inserted into a template wherever
// merged lines disagree. When both sides of a merge are key=value tokens
// with the same key, only the value generalizes ("method=<*>"), keeping
// templates readable.
const Wildcard = "<*>"

// Config tunes the parse tree. NoMask is carried here so a state file
// remembers how its lines were tokenized; the tree itself never tokenizes.
type Config struct {
	// Depth is how many leading tokens route a line below the length level.
	Depth int `json:"depth"`
	// SimThreshold is the minimum similarity (0–1] for joining a cluster.
	SimThreshold float64 `json:"sim_threshold"`
	// MaxChildren caps the branches per tree node; overflow routes through
	// the wildcard branch instead of growing the tree without bound.
	MaxChildren int `json:"max_children"`
	// NoMask records that lines were clustered on verbatim tokens.
	NoMask bool `json:"no_mask"`
}

// DefaultConfig returns the tuning used by the CLI unless overridden.
func DefaultConfig() Config {
	return Config{Depth: 3, SimThreshold: 0.5, MaxChildren: 64}
}

// Validate rejects configurations the tree cannot run with.
func (c Config) Validate() error {
	if c.Depth < 1 {
		return fmt.Errorf("depth must be >= 1, got %d", c.Depth)
	}
	if c.SimThreshold <= 0 || c.SimThreshold > 1 {
		return fmt.Errorf("threshold must be in (0, 1], got %g", c.SimThreshold)
	}
	if c.MaxChildren < 2 {
		return fmt.Errorf("max-children must be >= 2, got %d", c.MaxChildren)
	}
	return nil
}

// Cluster is one mined template with its evidence.
type Cluster struct {
	// ID is a stable content hash assigned when the cluster is born (see
	// TemplateID) and preserved through later generalization and across
	// runs via the state file.
	ID string
	// Tokens is the current template; Wildcard marks generalized positions.
	Tokens []string
	// Count is how many lines this template has absorbed.
	Count int64
	// Example is the first raw line that founded the cluster.
	Example string
	// FirstLine and LastLine are 1-based input line ordinals.
	FirstLine int64
	LastLine  int64
	// New is true when the cluster was created after MarkBaseline.
	New bool
}

// Template renders the token sequence as a single spaced string.
func (c *Cluster) Template() string { return strings.Join(c.Tokens, " ") }

// TemplateID derives a cluster ID from a token sequence: "t" plus the first
// 8 hex chars of its SHA-256. Deterministic for identical input, so two
// runs over the same stream mint identical IDs without coordination.
func TemplateID(tokens []string) string {
	sum := sha256.Sum256([]byte(strings.Join(tokens, "\x1f")))
	return "t" + hex.EncodeToString(sum[:4])
}

type node struct {
	children map[string]*node
	clusters []*Cluster
}

func newNode() *node { return &node{children: map[string]*node{}} }

// Tree is the streaming template miner. Not safe for concurrent use.
type Tree struct {
	cfg     Config
	lengths map[int]*node
	all     []*Cluster // insertion order
	lines   int64
	sealed  bool // baseline marked: later clusters are flagged New
}

// New returns an empty tree; cfg must pass Validate.
func New(cfg Config) *Tree {
	return &Tree{cfg: cfg, lengths: map[int]*node{}}
}

// Config returns the tree's tuning.
func (t *Tree) Config() Config { return t.cfg }

// Lines returns how many lines the tree has absorbed (including restored
// state).
func (t *Tree) Lines() int64 { return t.lines }

// SetLines restores the line counter from a saved state.
func (t *Tree) SetLines(n int64) { t.lines = n }

// Len returns the number of clusters.
func (t *Tree) Len() int { return len(t.all) }

// MarkBaseline freezes the current clusters as "known": they are cleared of
// the New flag and every cluster created afterwards carries it. Used when a
// state file is loaded before more input streams in.
func (t *Tree) MarkBaseline() {
	for _, c := range t.all {
		c.New = false
	}
	t.sealed = true
}

// Feed absorbs one line. It returns the cluster the line landed in and
// whether that cluster was created by this call.
func (t *Tree) Feed(raw string, tokens []string) (*Cluster, bool) {
	t.lines++
	leaf := t.leaf(tokens)
	if best, score := bestSim(leaf.clusters, tokens); best != nil && score >= t.cfg.SimThreshold {
		merge(best, tokens)
		best.Count++
		best.LastLine = t.lines
		return best, false
	}
	c := &Cluster{
		ID:        TemplateID(tokens),
		Tokens:    append([]string(nil), tokens...),
		Count:     1,
		Example:   raw,
		FirstLine: t.lines,
		LastLine:  t.lines,
		New:       t.sealed,
	}
	leaf.clusters = append(leaf.clusters, c)
	t.all = append(t.all, c)
	return c, true
}

// Match finds a cluster whose template strictly matches the tokens — every
// non-wildcard position equal — without learning anything. It backtracks
// through wildcard branches, prefers the most specific template, and
// returns nil when the line is novel.
func (t *Tree) Match(tokens []string) *Cluster {
	if len(tokens) == 0 {
		return nil
	}
	depth := t.cfg.Depth
	if depth > len(tokens) {
		depth = len(tokens)
	}
	return matchAt(t.lengths[len(tokens)], tokens, 0, depth)
}

// Restore re-inserts a cluster loaded from a state file, preserving its
// original ID, counters, and example.
func (t *Tree) Restore(c Cluster) {
	leaf := t.leaf(c.Tokens)
	cp := c
	cp.Tokens = append([]string(nil), c.Tokens...)
	leaf.clusters = append(leaf.clusters, &cp)
	t.all = append(t.all, &cp)
}

// Clusters returns a sorted snapshot: count descending, then first-seen
// line, then ID — a total, deterministic order.
func (t *Tree) Clusters() []*Cluster {
	out := append([]*Cluster(nil), t.all...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if a.FirstLine != b.FirstLine {
			return a.FirstLine < b.FirstLine
		}
		return a.ID < b.ID
	})
	return out
}

// NovelCount returns how many clusters were created after MarkBaseline.
func (t *Tree) NovelCount() int {
	n := 0
	for _, c := range t.all {
		if c.New {
			n++
		}
	}
	return n
}

// leaf walks (growing branches as needed) to the leaf node for a token
// sequence: length level first, then up to Depth prefix-token levels.
// Read-only descent is matchAt's job — it needs wildcard backtracking that
// a single walk cannot provide.
func (t *Tree) leaf(tokens []string) *node {
	root, ok := t.lengths[len(tokens)]
	if !ok {
		root = newNode()
		t.lengths[len(tokens)] = root
	}
	depth := t.cfg.Depth
	if depth > len(tokens) {
		depth = len(tokens)
	}
	n := root
	for i := 0; i < depth; i++ {
		key := branchKey(tokens[i])
		child := n.children[key]
		if child == nil && key != Wildcard && len(n.children) >= t.cfg.MaxChildren {
			// Node is full: unseen literals share the wildcard branch so
			// high-cardinality tokens cannot balloon the tree.
			key = Wildcard
			child = n.children[key]
		}
		if child == nil {
			child = newNode()
			n.children[key] = child
		}
		n = child
	}
	return n
}

// matchAt is the strict-match descent with wildcard backtracking: at every
// level try the literal branch first, then the wildcard branch.
func matchAt(n *node, tokens []string, level, depth int) *Cluster {
	if n == nil {
		return nil
	}
	if level == depth {
		return bestStrict(n.clusters, tokens)
	}
	key := branchKey(tokens[level])
	if c := matchAt(n.children[key], tokens, level+1, depth); c != nil {
		return c
	}
	if key != Wildcard {
		if c := matchAt(n.children[Wildcard], tokens, level+1, depth); c != nil {
			return c
		}
	}
	return nil
}

// branchKey picks the routing key for one token. Digit-bearing tokens and
// tokens already containing a wildcard route through the wildcard branch —
// raw identifiers must not mint tree branches, and restored templates must
// route exactly like the live lines that built them. Typed masks (<num>,
// <ip>, …) are stable strings and branch as themselves.
func branchKey(tok string) string {
	if strings.Contains(tok, Wildcard) || strings.ContainsFunc(tok, unicode.IsDigit) {
		return Wildcard
	}
	return tok
}

// bestSim scores every cluster in a leaf against the tokens. Wildcard
// positions count as matches (the template already gave that position up);
// exact matches break ties so the most specific candidate wins.
func bestSim(clusters []*Cluster, tokens []string) (*Cluster, float64) {
	var best *Cluster
	bestScore, bestExact := -1.0, -1
	for _, c := range clusters {
		exact, wild := 0, 0
		for i, tok := range tokens {
			switch {
			case c.Tokens[i] == tok:
				exact++
			case tokenMatches(c.Tokens[i], tok):
				wild++
			}
		}
		score := float64(exact+wild) / float64(len(tokens))
		if score > bestScore || (score == bestScore && exact > bestExact) {
			best, bestScore, bestExact = c, score, exact
		}
	}
	return best, bestScore
}

// bestStrict returns the strictly matching cluster with the most literal
// (non-wildcard) positions; insertion order breaks ties deterministically.
func bestStrict(clusters []*Cluster, tokens []string) *Cluster {
	var best *Cluster
	bestExact := -1
	for _, c := range clusters {
		if !strictMatch(c.Tokens, tokens) {
			continue
		}
		exact := 0
		for _, t := range c.Tokens {
			if t != Wildcard {
				exact++
			}
		}
		if exact > bestExact {
			best, bestExact = c, exact
		}
	}
	return best
}

// strictMatch reports whether a template accepts the tokens: wildcard
// positions match anything, key=<*> positions match any token with the same
// key, every other position must be equal.
func strictMatch(tpl, tokens []string) bool {
	for i, t := range tpl {
		if t != tokens[i] && !tokenMatches(t, tokens[i]) {
			return false
		}
	}
	return true
}

// tokenMatches reports whether a generalized template token accepts tok:
// the bare wildcard accepts anything, and "key=<*>" accepts any token
// carrying the same key.
func tokenMatches(tpl, tok string) bool {
	if tpl == Wildcard {
		return true
	}
	if strings.HasSuffix(tpl, "="+Wildcard) {
		return strings.HasPrefix(tok, tpl[:len(tpl)-len(Wildcard)])
	}
	return false
}

// merge generalizes a template in place: positions that disagree with the
// incoming tokens become the wildcard, except that key=value pairs sharing
// a key keep it and generalize only the value.
func merge(c *Cluster, tokens []string) {
	for i, tok := range tokens {
		if c.Tokens[i] == tok || tokenMatches(c.Tokens[i], tok) {
			continue
		}
		c.Tokens[i] = mergeToken(c.Tokens[i], tok)
	}
}

// mergeToken generalizes two disagreeing tokens. "status=200" + "status=404"
// → "status=<*>"; anything else → "<*>".
func mergeToken(a, b string) string {
	i := strings.IndexByte(a, '=')
	if i <= 0 || i >= len(a)-1 {
		return Wildcard
	}
	j := strings.IndexByte(b, '=')
	if j != i || a[:i] != b[:j] {
		return Wildcard
	}
	return a[:i+1] + Wildcard
}
