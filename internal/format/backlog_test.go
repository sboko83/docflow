package format

import (
	"strings"
	"testing"
)

func TestEditBacklogPreservesOtherBytes(t *testing.T) {
	const prefix = "---\ndocflow: 3\ncustom: keep  \n---\n## Queue\n- P001\n\n## Frozen\n- P002 — why\n\n## Backlog\n<!-- keep comment -->\n"
	const old = "- first  \n  second\n\tthird\n  \n  fourth\n"
	const suffix = "\n- untouched  \n\n## Other\nkeep this without a final newline"
	for _, nl := range []string{"\n", "\r\n"} {
		convert := func(s string) string { return strings.ReplaceAll(s, "\n", nl) }
		for _, tc := range []struct {
			name       string
			index      int
			text, want string
		}{
			{"replace", 0, "new\ncontinuation\n\nlast", prefix + "- new\n  continuation\n  \n  last\n" + suffix},
			{"delete", 0, "", prefix + suffix},
			{"append", -1, "added\nmore", prefix + old + "\n- untouched  \n- added\n  more\n\n## Other\nkeep this without a final newline"},
		} {
			t.Run(tc.name+strings.ReplaceAll(nl, "\n", "LF"), func(t *testing.T) {
				doc, err := ParseDocflow(convert(prefix+old+suffix), "docflow.md")
				if err != nil {
					t.Fatal(err)
				}
				text, err := doc.BacklogText(0)
				if err != nil || text != "first  \nsecond\nthird\n\nfourth" {
					t.Fatalf("editable text = %q, %v", text, err)
				}
				if err := doc.EditBacklog(tc.index, tc.text); err != nil {
					t.Fatal(err)
				}
				if got := doc.Text(); got != convert(tc.want) {
					t.Fatalf("bytes changed outside item:\ngot  %q\nwant %q", got, convert(tc.want))
				}
			})
		}
	}
}

func TestBacklogAtEOFAndEmpty(t *testing.T) {
	for _, initial := range []string{"---\ndocflow: 3\n---\n## Backlog\n- last", "---\ndocflow: 3\n---\n## Backlog\n"} {
		doc, err := ParseDocflow(initial, "docflow.md")
		if err != nil {
			t.Fatal(err)
		}
		if len(doc.Backlog) > 0 {
			if err := doc.EditBacklog(0, "changed\nmore"); err != nil {
				t.Fatal(err)
			}
			if doc.Text() != strings.TrimSuffix(initial, "- last")+"- changed\n  more" {
				t.Fatal(doc.Text())
			}
		}
		if err := doc.EditBacklog(-1, "added"); err != nil {
			t.Fatal(err)
		}
		if doc.Backlog[len(doc.Backlog)-1].Text != "added" {
			t.Fatal(doc.Backlog)
		}
	}
}
