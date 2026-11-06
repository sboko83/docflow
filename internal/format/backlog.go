package format

import (
	"fmt"
	"strings"
)

// BacklogText returns the editable text of an entire item, including its
// indented continuation lines. Item.Text is the flattened display label.
func (d *DocflowFile) BacklogText(index int) (string, error) {
	start, end, err := d.backlogRange(index)
	if err != nil {
		return "", err
	}
	lines := make([]string, end-start)
	for i, raw := range d.Raw[start:end] {
		line := strings.TrimRight(raw, "\r\n")
		if i == 0 {
			line = strings.TrimPrefix(line, "- ")
		} else {
			line = strings.TrimPrefix(strings.TrimPrefix(line, "  "), "\t")
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n"), nil
}

// EditBacklog appends at index -1, replaces an item, or deletes it when
// text is empty. All bytes outside the item are left untouched.
func (d *DocflowFile) EditBacklog(index int, text string) error {
	section := d.Section("Backlog")
	if section == nil {
		return fmt.Errorf("%s: no Backlog section", d.Path)
	}
	start, end := section.Line, section.Line
	if index >= 0 {
		var err error
		start, end, err = d.backlogRange(index)
		if err != nil {
			return err
		}
	} else {
		for line := section.Line; line < section.End; line++ {
			if strings.TrimSpace(d.Raw[line]) != "" {
				start = line + 1
			}
		}
		end = start
	}
	var replacement []string
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text != "" {
		for i, line := range strings.Split(text, "\n") {
			prefix := "  "
			if i == 0 {
				prefix = "- "
			}
			replacement = append(replacement, prefix+line+d.newline())
		}
		// Preserve a final item without a trailing newline.
		if end > start && !strings.HasSuffix(d.Raw[end-1], "\n") {
			replacement[len(replacement)-1] = strings.TrimSuffix(replacement[len(replacement)-1], d.newline())
		}
	}
	if start > 0 && start == end && len(replacement) > 0 && !strings.HasSuffix(d.Raw[start-1], "\n") {
		d.Raw[start-1] += d.newline()
	}
	d.Raw = append(d.Raw[:start], append(replacement, d.Raw[end:]...)...)
	return d.reparseDocflow()
}

func (d *DocflowFile) backlogRange(index int) (int, int, error) {
	if index < 0 || index >= len(d.Backlog) {
		return 0, 0, fmt.Errorf("Backlog item %d is out of range", index)
	}
	start := d.Backlog[index].Line - 1
	end := start + 1
	for next := end; next < len(d.Raw); next++ {
		line := strings.TrimRight(d.Raw[next], "\r\n")
		if continuationLine(line) {
			end = next + 1
		} else if strings.TrimSpace(line) != "" {
			break
		}
	}
	return start, end, nil
}
