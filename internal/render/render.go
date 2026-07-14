// Package render formats a mined template report as aligned text for
// humans, stable JSON (schema_version 1) for machines, or a Markdown table
// for PRs and issues. All three orderings come from drain.Tree.Clusters and
// are byte-deterministic for identical input.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/JaydenCJ/logloom/internal/drain"
)

// Options selects and filters the report.
type Options struct {
	Format   string // "text", "json", or "markdown"
	Top      int    // keep only the N most frequent templates; 0 = all
	MinCount int64  // drop templates with fewer matches
}

// row is one template in the JSON report.
type row struct {
	ID        string  `json:"id"`
	Template  string  `json:"template"`
	Count     int64   `json:"count"`
	Share     float64 `json:"share"`
	FirstLine int64   `json:"first_line"`
	LastLine  int64   `json:"last_line"`
	Example   string  `json:"example"`
	New       bool    `json:"new"`
}

type report struct {
	Tool           string `json:"tool"`
	SchemaVersion  int    `json:"schema_version"`
	Lines          int64  `json:"lines"`
	Templates      int    `json:"templates"`
	NovelTemplates int    `json:"novel_templates"`
	Shown          int    `json:"shown"`
	Clusters       []row  `json:"clusters"`
}

// Report writes the template report for a tree.
func Report(w io.Writer, t *drain.Tree, opts Options) error {
	rep := build(t, opts)
	switch opts.Format {
	case "", "text":
		return text(w, rep)
	case "json":
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false) // templates are full of <masks>
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	case "markdown":
		return markdown(w, rep)
	default:
		return fmt.Errorf("unknown format %q (want text, json, or markdown)", opts.Format)
	}
}

func build(t *drain.Tree, opts Options) report {
	clusters := t.Clusters()
	rep := report{
		Tool:           "logloom",
		SchemaVersion:  1,
		Lines:          t.Lines(),
		Templates:      len(clusters),
		NovelTemplates: t.NovelCount(),
		Clusters:       []row{},
	}
	for _, c := range clusters {
		if c.Count < opts.MinCount {
			continue
		}
		if opts.Top > 0 && len(rep.Clusters) >= opts.Top {
			break
		}
		rep.Clusters = append(rep.Clusters, row{
			ID:        c.ID,
			Template:  c.Template(),
			Count:     c.Count,
			Share:     share(c.Count, t.Lines()),
			FirstLine: c.FirstLine,
			LastLine:  c.LastLine,
			Example:   c.Example,
			New:       c.New,
		})
	}
	rep.Shown = len(rep.Clusters)
	return rep
}

// share returns the percentage with one decimal, computed the same way for
// every output format.
func share(count, lines int64) float64 {
	if lines == 0 {
		return 0
	}
	return math.Round(float64(count)/float64(lines)*1000) / 10
}

func text(w io.Writer, rep report) error {
	head := fmt.Sprintf("logloom — %s → %s",
		countNoun(rep.Lines, "line"), countNoun(int64(rep.Templates), "template"))
	if rep.NovelTemplates > 0 {
		head += fmt.Sprintf(" (%d new since baseline)", rep.NovelTemplates)
	}
	if _, err := fmt.Fprintf(w, "%s\n\n", head); err != nil {
		return err
	}
	countW := len("count")
	for _, r := range rep.Clusters {
		if n := len(comma(r.Count)); n > countW {
			countW = n
		}
	}
	markNew := rep.NovelTemplates > 0
	newCol := ""
	if markNew {
		newCol = "  new"
	}
	if _, err := fmt.Fprintf(w, "%*s  %5s  %-9s%s  template\n",
		countW, "count", "%", "id", newCol); err != nil {
		return err
	}
	for _, r := range rep.Clusters {
		flag := ""
		if markNew {
			flag = "     "
			if r.New {
				flag = "  NEW"
			}
		}
		if _, err := fmt.Fprintf(w, "%*s  %5.1f  %-9s%s  %s\n",
			countW, comma(r.Count), r.Share, r.ID, flag, r.Template); err != nil {
			return err
		}
	}
	foot := fmt.Sprintf("\n%s · %s", countNoun(rep.Lines, "line"), countNoun(int64(rep.Templates), "template"))
	if rep.Shown < rep.Templates {
		foot += fmt.Sprintf(" · showing %d", rep.Shown)
	}
	_, err := fmt.Fprintln(w, foot)
	return err
}

func markdown(w io.Writer, rep report) error {
	if _, err := fmt.Fprintf(w, "**logloom** — %s → %s\n\n",
		countNoun(rep.Lines, "line"), countNoun(int64(rep.Templates), "template")); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| count | share | id | template |\n|---:|---:|---|---|"); err != nil {
		return err
	}
	for _, r := range rep.Clusters {
		tpl := strings.ReplaceAll(r.Template, "|", "\\|")
		if r.New {
			tpl += " **(new)**"
		}
		if _, err := fmt.Fprintf(w, "| %s | %.1f%% | `%s` | `%s` |\n",
			comma(r.Count), r.Share, r.ID, tpl); err != nil {
			return err
		}
	}
	return nil
}

// countNoun renders a count with its noun so the grammatical number always
// agrees: "1 line", "12,345 lines" — never "1 lines".
func countNoun(n int64, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return comma(n) + " " + noun + "s"
}

// comma renders n with thousands separators: 1234567 → "1,234,567".
func comma(n int64) string {
	s := fmt.Sprintf("%d", n)
	neg := false
	if strings.HasPrefix(s, "-") {
		neg, s = true, s[1:]
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead == 0 {
		lead = 3
	}
	b.WriteString(s[:lead])
	for i := lead; i < len(s); i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
