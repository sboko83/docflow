// Package stats reports recorded context proxies beside measured agent usage.
package stats

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/cycle"
)

var (
	transcriptUsage = []string{"input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"}
	commandPattern  = regexp.MustCompile(`\bdocflow\s+prompt(?:\s+(P[0-9]+\.[0-9]+\.[0-9]+))?`)
	endPattern      = regexp.MustCompile(`\bdocflow\s+(?:finish|commit)\s+(P[0-9]+\.[0-9]+\.[0-9]+)`)
	resultIDPattern = regexp.MustCompile(`^#\s+(P[0-9]+\.[0-9]+\.[0-9]+)`)
	pathPartPattern = regexp.MustCompile(`[^A-Za-z0-9]`)
	digitPattern    = regexp.MustCompile(`[0-9]+`)
)

// Row is one recorded unit's proxy and transcript usage, when available.
type Row struct {
	Unit     string
	Proxy    any
	Requests *int
	Sum      *big.Int
	BaseCtx  *big.Int
	Peak     *big.Int
	Source   string
}

// Result is the stdout, stderr, and exit code of docflow stats.
type Result struct {
	Code   int
	Stdout string
	Stderr string
}

type toolCommand struct {
	toolID  any
	command any
}

type request struct {
	ctx  *big.Int
	cmds []toolCommand
}

type transcript struct {
	requests []request
	results  map[any]string
}

type record struct {
	unit string
	data map[string]any
}

// ClaudeProjectsDir returns DOCFLOW_CLAUDE_PROJECTS or Claude's project
// transcript directory for root.
func ClaudeProjectsDir(root string) string {
	if configured := os.Getenv("DOCFLOW_CLAUDE_PROJECTS"); configured != "" {
		return expandUser(configured)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects", pathPartPattern.ReplaceAllString(root, "-"))
}

func expandUser(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	separator := strings.IndexAny(path[1:], "/\\")
	userName, rest := path[1:], ""
	if separator >= 0 {
		userName, rest = path[1:separator+1], path[separator+1:]
	}
	var home string
	if userName == "" {
		home, _ = os.UserHomeDir()
	} else if found, err := user.Lookup(userName); err == nil {
		home = found.HomeDir
	}
	if home == "" {
		return path
	}
	if rest != "" {
		rest = strings.TrimLeft(rest, "/\\")
		return filepath.Join(home, rest)
	}
	return home
}

func resultText(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	blocks, ok := content.([]any)
	if !ok {
		return ""
	}
	var values []string
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := block["text"].(string); ok {
			values = append(values, text)
		}
	}
	return strings.Join(values, "\n")
}

func readTranscript(path string) transcript {
	out := transcript{results: make(map[any]string)}
	content, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	indexes := make(map[any]int)
	for lineNumber, line := range bytes.Split(content, []byte{'\n'}) {
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()
		var raw any
		if err := decoder.Decode(&raw); err != nil {
			continue
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			continue
		}
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		message, ok := entry["message"].(map[string]any)
		if !ok {
			continue
		}
		blocks, _ := message["content"].([]any)
		switch entry["type"] {
		case "user":
			for _, rawBlock := range blocks {
				block, ok := rawBlock.(map[string]any)
				if !ok || block["type"] != "tool_result" {
					continue
				}
				toolID, exists := block["tool_use_id"]
				if !exists || !comparable(toolID) {
					break
				}
				text := strings.TrimSpace(resultText(block["content"]))
				out.results[toolID] = firstLine(text)
			}
			continue
		case "assistant":
		default:
			continue
		}

		key, exists := message["id"]
		if !exists || !truthy(key) {
			key = fmt.Sprintf("line%d", lineNumber)
		}
		if !comparable(key) {
			continue
		}
		index, exists := indexes[key]
		if !exists {
			usage, ok := message["usage"].(map[string]any)
			if !ok {
				usage = nil
			}
			ctx := new(big.Int)
			for _, name := range transcriptUsage {
				if value, ok := integerValue(usage[name]); ok {
					ctx.Add(ctx, value)
				}
			}
			index = len(out.requests)
			indexes[key] = index
			out.requests = append(out.requests, request{ctx: ctx})
		}
		for _, rawBlock := range blocks {
			block, ok := rawBlock.(map[string]any)
			if !ok || block["type"] != "tool_use" || block["name"] != "Bash" {
				continue
			}
			input, ok := block["input"].(map[string]any)
			if !ok {
				break
			}
			command, ok := input["command"]
			if !ok {
				break
			}
			out.requests[index].cmds = append(out.requests[index].cmds, toolCommand{
				toolID: block["id"], command: command,
			})
		}
	}
	return out
}

func firstLine(text string) string {
	for index, r := range text {
		switch r {
		case '\n', '\r', '\v', '\f', '\u0085', '\u2028', '\u2029':
			return text[:index]
		}
	}
	return text
}

func interactiveWindows(requests []request, results map[any]string) map[string][]request {
	starts := make(map[string]int)
	windows := make(map[string][]request)
	for index, req := range requests {
		for _, tool := range req.cmds {
			command, ok := tool.command.(string)
			if !ok {
				continue
			}
			for _, match := range commandPattern.FindAllStringSubmatch(command, -1) {
				unit := match[1]
				if unit == "" {
					if result, exists := results[tool.toolID]; exists {
						found := resultIDPattern.FindStringSubmatch(result)
						if len(found) > 1 {
							unit = found[1]
						}
					}
				}
				if unit != "" {
					if _, exists := starts[unit]; !exists {
						starts[unit] = index
					}
				}
			}
			for _, match := range endPattern.FindAllStringSubmatch(command, -1) {
				unit := match[1]
				start, started := starts[unit]
				if started {
					if _, exists := windows[unit]; !exists {
						windows[unit] = append([]request(nil), requests[start:index+1]...)
					}
				}
			}
		}
	}
	for unit, start := range starts {
		if _, exists := windows[unit]; !exists {
			windows[unit] = append([]request(nil), requests[start:]...)
		}
	}
	return windows
}

func median(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 != 0 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

// UnitRows returns one row for each recorded unit, optionally limited to a plan.
func UnitRows(root, planID string) ([]Row, error) {
	verified, err := cycle.StateDir(root)
	if err != nil {
		return nil, err
	}
	statsFolder := filepath.Join(filepath.Dir(verified), "stats")
	paths, err := filepath.Glob(filepath.Join(statsFolder, "P*.json"))
	if err != nil {
		return nil, err
	}
	records := make([]record, 0, len(paths))
	for _, path := range paths {
		unit := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if planID != "" && !strings.HasPrefix(unit, planID+".") {
			continue
		}
		records = append(records, record{unit: unit, data: readStats(path)})
	}
	sort.SliceStable(records, func(i, j int) bool {
		return compareNumericParts(records[i].unit, records[j].unit) < 0
	})

	folder := ClaudeProjectsDir(root)
	transcripts := make(map[string]transcript)
	var transcriptOrder []string
	if info, err := os.Stat(folder); err == nil && info.IsDir() {
		files, _ := filepath.Glob(filepath.Join(folder, "*.jsonl"))
		for _, path := range files {
			name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			transcripts[name] = readTranscript(path)
			transcriptOrder = append(transcriptOrder, name)
		}
	}
	runnerSessions := make(map[string]struct{})
	for _, item := range records {
		for _, session := range sessionIDs(item.data["sessions"]) {
			runnerSessions[session] = struct{}{}
		}
	}
	windows := make(map[string][]request)
	for _, name := range transcriptOrder {
		transcript := transcripts[name]
		if _, isRunner := runnerSessions[name]; isRunner {
			continue
		}
		for unit, requests := range interactiveWindows(transcript.requests, transcript.results) {
			windows[unit] = append(windows[unit], requests...)
		}
	}

	rows := make([]Row, 0, len(records))
	for _, item := range records {
		requests := append([]request(nil), windows[item.unit]...)
		for _, session := range sessionIDs(item.data["sessions"]) {
			if recorded, exists := transcripts[session]; exists {
				requests = append(requests, recorded.requests...)
			}
		}
		row := Row{Unit: item.unit, Proxy: item.data["proxy_tokens"], Source: "-"}
		if len(requests) > 0 {
			total := new(big.Int)
			base := new(big.Int).Set(requests[0].ctx)
			peak := new(big.Int).Set(requests[0].ctx)
			for _, req := range requests {
				total.Add(total, req.ctx)
				if req.ctx.Cmp(peak) > 0 {
					peak.Set(req.ctx)
				}
			}
			count := len(requests)
			row.Requests, row.Sum, row.BaseCtx, row.Peak, row.Source = &count, total, base, peak, "transcript"
		} else if tokens, ok := integerValue(item.data["tokens"]); ok {
			row.Sum, row.Source = tokens, "tokens"
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func readStats(path string) map[string]any {
	content, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{}
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return map[string]any{}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return map[string]any{}
	}
	if data, ok := raw.(map[string]any); ok && data != nil {
		return data
	}
	return map[string]any{}
}

func sessionIDs(raw any) []string {
	var values []any
	switch sessions := raw.(type) {
	case []any:
		values = sessions
	case string:
		for _, r := range sessions {
			values = append(values, string(r))
		}
	}
	var out []string
	for _, value := range values {
		if session, ok := value.(string); ok {
			out = append(out, session)
		}
	}
	return out
}

func compareNumericParts(left, right string) int {
	leftParts, rightParts := digitPattern.FindAllString(left, -1), digitPattern.FindAllString(right, -1)
	for index := 0; index < len(leftParts) && index < len(rightParts); index++ {
		leftNumber, _ := new(big.Int).SetString(leftParts[index], 10)
		rightNumber, _ := new(big.Int).SetString(rightParts[index], 10)
		if comparison := leftNumber.Cmp(rightNumber); comparison != 0 {
			return comparison
		}
	}
	if len(leftParts) < len(rightParts) {
		return -1
	}
	if len(leftParts) > len(rightParts) {
		return 1
	}
	return 0
}

func integerValue(value any) (*big.Int, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return nil, false
	}
	parsed, ok := new(big.Int).SetString(number.String(), 10)
	return parsed, ok
}

func comparable(value any) bool {
	return value == nil || reflect.TypeOf(value).Comparable()
}

func truthy(value any) bool {
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case string:
		return value != ""
	case json.Number:
		parsed, ok := new(big.Rat).SetString(value.String())
		return !ok || parsed.Sign() != 0
	case []any:
		return len(value) > 0
	case map[string]any:
		return len(value) > 0
	default:
		return true
	}
}

func pythonString(value any) string {
	switch value := value.(type) {
	case nil:
		return "-"
	case bool:
		if value {
			return "True"
		}
		return "False"
	case json.Number:
		if integer, ok := integerValue(value); ok {
			return integer.String()
		}
		return value.String()
	case string:
		return value
	default:
		content, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(content)
	}
}

func ratioValue(peak *big.Int, proxy any) (float64, bool) {
	if peak == nil || !truthy(proxy) {
		return 0, false
	}
	var denominator float64
	switch value := proxy.(type) {
	case json.Number:
		parsed, err := strconv.ParseFloat(value.String(), 64)
		if err != nil {
			return 0, false
		}
		denominator = parsed
	case bool:
		if value {
			denominator = 1
		}
	default:
		return 0, false
	}
	if denominator == 0 {
		return 0, false
	}
	peakFloat, _ := peak.Float64()
	return peakFloat / denominator, true
}

// Command runs the stats report and returns CLI-compatible output and status.
func Command(root, planID string) (Result, error) {
	rows, err := UnitRows(root, planID)
	if err != nil {
		return Result{}, err
	}
	if len(rows) == 0 {
		return Result{Code: clierrors.EXIT_NOTHING, Stderr: "no stats: run docflow prompt first\n"}, nil
	}
	var output strings.Builder
	output.WriteString("unit proxy reqs sum_ctx base_ctx peak_ctx peak/proxy grow/proxy source\n")
	var peakRatios, growRatios []float64
	for _, row := range rows {
		requests, total, base, peak, ratio, growth := "-", "-", "-", "-", "-", "-"
		if row.Requests != nil {
			requests = strconv.Itoa(*row.Requests)
		}
		if row.Sum != nil {
			total = row.Sum.String()
		}
		if row.BaseCtx != nil {
			base = row.BaseCtx.String()
		}
		if row.Peak != nil {
			peak = row.Peak.String()
		}
		if value, ok := ratioValue(row.Peak, row.Proxy); ok {
			ratio = fmt.Sprintf("%.2f", value)
			peakRatios = append(peakRatios, value)
		}
		if row.Peak != nil && row.BaseCtx != nil {
			increase := new(big.Int).Sub(row.Peak, row.BaseCtx)
			if value, ok := ratioValue(increase, row.Proxy); ok {
				growth = fmt.Sprintf("%.2f", value)
				growRatios = append(growRatios, value)
			}
		}
		fmt.Fprintf(&output, "%s %s %s %s %s %s %s %s %s\n", row.Unit,
			pythonString(row.Proxy), requests, total, base, peak, ratio, growth, row.Source)
	}
	if len(peakRatios) > 0 {
		fmt.Fprintf(&output, "median peak/proxy: %.2f\n", median(peakRatios))
	} else {
		output.WriteString("median peak/proxy: -\n")
	}
	if len(growRatios) > 0 {
		fmt.Fprintf(&output, "median grow/proxy: %.2f\n", median(growRatios))
	} else {
		output.WriteString("median grow/proxy: -\n")
	}
	return Result{Code: clierrors.EXIT_OK, Stdout: output.String()}, nil
}
