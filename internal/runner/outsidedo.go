package runner

import (
	"path"
	"regexp"
	"strings"

	"bsv-docflow/internal/format"
)

// outsideDoLabel prefixes the run line that lists the files a unit's commit
// touched although neither Read, Do, Traps nor Verify of the unit names them.
const outsideDoLabel = "outside Do: "

// pathLike matches a token that looks like a file path or a file name with an
// extension, with an optional :Lstart-end or #anchor suffix.
var pathLike = regexp.MustCompile("[A-Za-z0-9_./\\\\@+-]+\\.[A-Za-z0-9]+|[A-Za-z0-9_@+-]+(?:/[A-Za-z0-9_.@+-]+)+")

// mentionedPaths collects the path-like tokens of the unit's Read, Do, Traps
// and Verify fields, slash-normalised and without a leading "./".
func mentionedPaths(unit *format.Unit) []string {
	var tokens []string
	for _, key := range []string{"Read", "Do", "Traps", "Verify"} {
		field := unit.Field(key)
		if field == nil {
			continue
		}
		text := field.Value
		for _, item := range field.Items {
			text += "\n" + item.Text
		}
		for _, token := range pathLike.FindAllString(text, -1) {
			token = strings.TrimPrefix(strings.ReplaceAll(token, "\\", "/"), "./")
			tokens = append(tokens, strings.Trim(token, "./"))
		}
	}
	return tokens
}

// committedFiles reads the file list out of the stdout of `docflow commit`:
// the lines after the "<sha> <subject>" line, up to the first line that is not
// a bare path (a nested repository header or a test change item).
func committedFiles(stdout string) []string {
	var files []string
	lines := strings.Split(stdout, "\n")
	for _, line := range lines[min(1, len(lines)):] {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.Contains(line, " ") || strings.HasPrefix(line, testChangeLabel) {
			break
		}
		files = append(files, line)
	}
	return files
}

// outsideDo returns the committed files that the unit's text does not name,
// neither by path nor by file name nor through a directory it mentions. The
// plan file is the unit's own bookkeeping and never counts.
func outsideDo(unit *format.Unit, planPath, stdout string) []string {
	mentioned := mentionedPaths(unit)
	var outside []string
	for _, file := range committedFiles(stdout) {
		if file == planPath || named(mentioned, file) {
			continue
		}
		outside = append(outside, file)
	}
	return outside
}

func named(mentioned []string, file string) bool {
	base := path.Base(file)
	for _, token := range mentioned {
		if token == "" {
			continue
		}
		if token == file || token == base || strings.HasSuffix(file, "/"+token) ||
			strings.HasPrefix(file, token+"/") || path.Dir(file) == token {
			return true
		}
	}
	return false
}
