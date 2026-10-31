package roster

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"bsv-docflow/internal/clierrors"
)

// StatusRow is one access line of `roster status`: the channels that
// reach it, what the auth probes said, how many models the catalog holds
// and the scanners that supplied them. The condition state joins it in
// P055.2.2.
type StatusRow struct {
	Access string   `json:"access"`
	Agents []string `json:"agents,omitempty"`
	Kind   string   `json:"kind,omitempty"`
	Auth   string   `json:"auth,omitempty"`
	Status string   `json:"status,omitempty"`
	Models int      `json:"models"`
	Source []string `json:"source,omitempty"`
}

// RosterAccessNames reads the access names the policy at path ("" means
// DefaultPath) declares, in file order. A missing file is an empty list,
// not an error: a catalog can be listed before a roster exists. The
// policy is parsed, so a v1 file fails with the migration hint instead of
// its spend order passing as a list of accesses.
func RosterAccessNames(path string) ([]string, error) {
	path = PolicyPath(path)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster file: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	policy, err := LoadPolicy(path)
	if err != nil {
		return nil, err
	}
	return policy.AccessNames(), nil
}

// StatusRows lists every access of the catalog in catalog order with the
// number of its models and the scanners that supplied it.
func (c *Catalog) StatusRows() []StatusRow {
	rows := make([]StatusRow, 0, len(c.Accesses))
	for _, access := range c.Accesses {
		rows = append(rows, StatusRow{
			Access: access.Access,
			Agents: access.Agents,
			Kind:   access.Kind,
			Auth:   access.Auth,
			Status: access.Status,
			Models: c.modelCount(access.Access),
			Source: c.accessSources(access.Access),
		})
	}
	return rows
}

// modelCount counts the models of one access.
func (c *Catalog) modelCount(access string) int {
	count := 0
	for _, model := range c.Models {
		if model.Access == access {
			count++
		}
	}
	return count
}

// accessSources lists the scanners that supplied the access or any of its
// models, in catalog source order. After the auth probe every access
// carries Source "auth", which alone would hide where its models came
// from — the per-field provenance still knows.
func (c *Catalog) accessSources(access string) []string {
	seen := map[string]bool{}
	var sources []string
	collect := func(provenance map[string]string, fallback string) {
		added := false
		for _, source := range provenance {
			if source == "" || seen[source] {
				continue
			}
			seen[source] = true
			sources = append(sources, source)
			added = true
		}
		if !added && fallback != "" && !seen[fallback] {
			seen[fallback] = true
			sources = append(sources, fallback)
		}
	}
	for _, entry := range c.Accesses {
		if entry.Access == access {
			collect(entry.Provenance, entry.Source)
		}
	}
	for _, model := range c.Models {
		if model.Access == access {
			collect(model.Provenance, model.Source)
		}
	}
	order := make(map[string]int, len(c.Sources))
	for i, source := range c.Sources {
		order[source] = i
	}
	sort.SliceStable(sources, func(i, j int) bool {
		li, okI := order[sources[i]]
		lj, okJ := order[sources[j]]
		switch {
		case okI && okJ:
			return li < lj
		case okI != okJ:
			return okI
		}
		return sources[i] < sources[j]
	})
	return sources
}

// ModelRows returns the models of the given accesses in catalog order; a
// nil list means every model of the catalog.
func (c *Catalog) ModelRows(accesses []string) []ModelInfo {
	if accesses == nil {
		return append([]ModelInfo{}, c.Models...)
	}
	wanted := make(map[string]bool, len(accesses))
	for _, access := range accesses {
		wanted[access] = true
	}
	rows := make([]ModelInfo, 0, len(c.Models))
	for _, model := range c.Models {
		if wanted[model.Access] {
			rows = append(rows, model)
		}
	}
	return rows
}

// AccessTable renders the access summary: the access, its agent channels,
// how many models the catalog holds for it, what the auth probes said and
// the scanners that supplied it.
func (c *Catalog) AccessTable() string {
	rows := [][]string{{"access", "agent", "models", "auth", "source"}}
	for _, row := range c.StatusRows() {
		rows = append(rows, []string{
			row.Access,
			orDashText(strings.Join(row.Agents, ",")),
			strconv.Itoa(row.Models),
			orDashText(row.Auth),
			orDashText(strings.Join(row.Source, "+")),
		})
	}
	return tableText(rows)
}

// ModelsTable renders the model table of the given accesses (nil means
// the whole catalog). An unknown context, output limit or price prints as
// "unknown" instead of a zero that would read as a real value.
func (c *Catalog) ModelsTable(accesses []string) string {
	return ModelTable(c.ModelRows(accesses))
}

// ModelTable renders the given models, in their own order.
func ModelTable(models []ModelInfo) string {
	rows := [][]string{{"access", "model", "context", "max-out", "reasoning", "cost", "source"}}
	for _, model := range models {
		rows = append(rows, []string{
			model.Access,
			model.ID,
			tokenText(model.Context),
			tokenText(model.MaxOut),
			yesNo(model.Reasoning),
			costText(model.Cost),
			orDashText(model.Source),
		})
	}
	return tableText(rows)
}

// StatusHeader is the line above the status table: when the cache was
// written and which sources contributed to it.
func (c *Catalog) StatusHeader() string {
	if c.ScannedAt == "" {
		return "catalog: not scanned yet"
	}
	return fmt.Sprintf("catalog: %s · sources: %s", c.ScannedAt, orDashText(strings.Join(c.Sources, ", ")))
}

// tokenText prints a token count; zero means unknown, never zero tokens.
func tokenText(value int) string {
	if value <= 0 {
		return "unknown"
	}
	return strconv.Itoa(value)
}

// costText prints the input/output price per million tokens. A nil cost is
// an unknown price; a zero price is a free model and prints as 0.
func costText(cost *Cost) string {
	if cost == nil {
		return "unknown"
	}
	return priceText(cost.Input) + "/" + priceText(cost.Output)
}

func priceText(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

// orDashText prints an empty cell as "-", so a table column never
// collapses.
func orDashText(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

// tableText pads every column to its widest cell with two spaces between
// columns. The CLI has no table library and the listings must be stable
// between runs, so the widths come from the rows themselves.
func tableText(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	widths := make([]int, 0, len(rows[0]))
	for i := range rows[0] {
		width := 0
		for _, row := range rows {
			if i < len(row) {
				if n := len([]rune(row[i])); n > width {
					width = n
				}
			}
		}
		widths = append(widths, width)
	}
	var text strings.Builder
	for _, row := range rows {
		last := len(row) - 1
		for i, cell := range row {
			text.WriteString(cell)
			if i < last {
				text.WriteString(strings.Repeat(" ", widths[i]-len([]rune(cell))+2))
			}
		}
		text.WriteString("\n")
	}
	return text.String()
}

// ModelReport is the JSON of `roster models`: the models the filter
// selected, in catalog order.
type ModelReport struct {
	Models []ModelInfo `json:"models"`
}

// StatusReport is the JSON of `roster status`: when the cache was written,
// which sources built it, one row per access, and the active conditions of
// a v2 roster.
type StatusReport struct {
	ScannedAt  string      `json:"scanned_at,omitempty"`
	Sources    []string    `json:"sources"`
	Accesses   []StatusRow `json:"accesses"`
	Conditions []Condition `json:"conditions,omitempty"`
}

// Report collects the status JSON of the catalog.
func (c *Catalog) Report() StatusReport {
	return StatusReport{ScannedAt: c.ScannedAt, Sources: c.Sources, Accesses: c.StatusRows()}
}
