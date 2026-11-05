package runner

import (
	"reflect"
	"testing"

	"bsv-docflow/internal/format"
)

func TestOutsideDo(t *testing.T) {
	unit := &format.Unit{Fields: []format.Field{
		{Key: "Read", Items: []format.Item{{Text: "internal/cycle/commit.go:L1-80"}, {Text: "docs/design/cli.md#commit"}}},
		{Key: "Do", Items: []format.Item{{Text: "Edit `internal/runner/run.go` and add outsidedo.go"}, {Text: "Tests under internal/cycle/testdata"}}},
		{Key: "Verify", Value: "`go test ./internal/runner`"},
	}}
	stdout := "abc1234 P001.1.1: subject\n" +
		"docs/plans/P001-x.md\n" +
		"internal/runner/run.go\n" +
		"internal/runner/outsidedo.go\n" +
		"internal/cycle/testdata/a.txt\n" +
		"internal/other/stray.go\n" +
		"go.sum\n" +
		"test change: a_test.go: -1 lines\n"
	got := outsideDo(unit, "docs/plans/P001-x.md", stdout)
	want := []string{"internal/other/stray.go", "go.sum"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("outsideDo = %v, want %v", got, want)
	}
}

func TestCommittedFilesStopsAtNestedRepo(t *testing.T) {
	got := committedFiles("abc subject\na.go\nb.go\ndeps/module def5678\ndeps/module/c.go\n")
	if !reflect.DeepEqual(got, []string{"a.go", "b.go"}) {
		t.Fatalf("committedFiles = %v", got)
	}
}
