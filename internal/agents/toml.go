package agents

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ParseTOML parses the small TOML subset used by docflow agent configuration.
// It supports tables, dotted keys, inline tables, arrays, basic strings,
// multiline basic strings, booleans, and decimal integers.
func ParseTOML(text string) (map[string]any, error) {
	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("invalid UTF-8")
	}
	p := &tomlParser{
		text:        text,
		line:        1,
		root:        make(map[string]any),
		current:     make(map[string]any),
		tables:      map[string]bool{},
		inlineTable: map[string]bool{},
	}
	p.current = p.root
	for {
		p.skipStatementSpace()
		if p.eof() {
			return p.root, nil
		}
		var err error
		if p.peek() == '[' {
			err = p.parseTable()
		} else {
			err = p.parseAssignment()
		}
		if err != nil {
			return nil, err
		}
		if err := p.finishStatement(); err != nil {
			return nil, err
		}
	}
}

type tomlParser struct {
	text        string
	pos         int
	line        int
	root        map[string]any
	current     map[string]any
	currentPath []string
	tables      map[string]bool
	inlineTable map[string]bool
}

func (p *tomlParser) eof() bool { return p.pos >= len(p.text) }

func (p *tomlParser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.text[p.pos]
}

func (p *tomlParser) fail(message string) error {
	return fmt.Errorf("line %d: %s", p.line, message)
}

func (p *tomlParser) syntaxError(message string) error {
	if p.eof() {
		return fmt.Errorf("%s (at end of document)", message)
	}
	start := strings.LastIndex(p.text[:p.pos], "\n") + 1
	column := utf8.RuneCountInString(p.text[start:p.pos]) + 1
	return fmt.Errorf("%s (at line %d, column %d)", message, p.line, column)
}

func (p *tomlParser) skipStatementSpace() {
	for !p.eof() {
		switch p.peek() {
		case ' ', '\t', '\r':
			p.pos++
		case '\n':
			p.pos++
			p.line++
		case '#':
			p.skipComment()
		default:
			return
		}
	}
}

func (p *tomlParser) skipValueSpace() {
	for !p.eof() {
		switch p.peek() {
		case ' ', '\t', '\r':
			p.pos++
		case '\n':
			p.pos++
			p.line++
		case '#':
			p.skipComment()
		default:
			return
		}
	}
}

func (p *tomlParser) skipComment() {
	for !p.eof() && p.peek() != '\n' {
		p.pos++
	}
}

func (p *tomlParser) finishStatement() error {
	for !p.eof() {
		switch p.peek() {
		case ' ', '\t', '\r':
			p.pos++
		case '#':
			p.skipComment()
		case '\n':
			p.pos++
			p.line++
			return nil
		default:
			return p.fail("unexpected character after value")
		}
	}
	return nil
}

func (p *tomlParser) parseTable() error {
	line := p.line
	p.pos++
	if !p.eof() && p.peek() == '[' {
		p.pos++
		_, err := p.parseKeyPath(']')
		if err != nil || !strings.HasPrefix(p.text[p.pos:], "]]") {
			return p.syntaxError("Expected ']]' at the end of an array declaration")
		}
		return p.fail("array of tables is not supported")
	}
	path, err := p.parseKeyPath(']')
	if err != nil {
		return err
	}
	if len(path) == 0 || p.eof() || p.peek() != ']' {
		return p.fail("invalid table header")
	}
	p.pos++
	current := p.root
	full := make([]string, 0, len(path))
	for _, key := range path {
		full = append(full, key)
		canonical := strings.Join(full, ".")
		if p.inlineTable[canonical] {
			return fmt.Errorf("line %d: cannot extend inline table %q", line, canonical)
		}
		value, exists := current[key]
		if !exists {
			table := make(map[string]any)
			current[key] = table
			current = table
			continue
		}
		table, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("line %d: %q is not a table", line, canonical)
		}
		current = table
	}
	canonical := strings.Join(path, ".")
	if p.tables[canonical] {
		return fmt.Errorf("line %d: table %q is already defined", line, canonical)
	}
	p.tables[canonical] = true
	p.current = current
	p.currentPath = append([]string(nil), path...)
	return nil
}

func (p *tomlParser) parseAssignment() error {
	path, err := p.parseKeyPath('=')
	if err != nil {
		return err
	}
	if len(path) == 0 || p.eof() || p.peek() != '=' {
		return p.fail("expected key = value")
	}
	p.pos++
	p.skipHorizontalSpace()
	fullPath := append(append([]string(nil), p.currentPath...), path...)
	value, err := p.parseValue(fullPath)
	if err != nil {
		return err
	}
	return p.setValue(p.current, path, value, p.currentPath)
}

func (p *tomlParser) skipHorizontalSpace() {
	for !p.eof() && (p.peek() == ' ' || p.peek() == '\t' || p.peek() == '\r') {
		p.pos++
	}
}

func (p *tomlParser) parseKeyPath(stop byte) ([]string, error) {
	keys := make([]string, 0, 2)
	for {
		p.skipHorizontalSpace()
		key, err := p.parseKey()
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
		p.skipHorizontalSpace()
		if p.eof() {
			return nil, p.fail("unterminated key")
		}
		if p.peek() == '.' {
			p.pos++
			continue
		}
		if p.peek() == stop {
			return keys, nil
		}
		return nil, p.fail("expected key separator")
	}
}

func (p *tomlParser) parseKey() (string, error) {
	if p.eof() {
		return "", p.fail("expected key")
	}
	if p.peek() == '"' {
		value, err := p.parseBasicString(false)
		if err != nil {
			return "", err
		}
		return value, nil
	}
	start := p.pos
	for !p.eof() {
		c := p.peek()
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' {
			p.pos++
			continue
		}
		break
	}
	if start == p.pos {
		return "", p.fail("invalid key")
	}
	return p.text[start:p.pos], nil
}

func (p *tomlParser) parseValue(path []string) (any, error) {
	if p.eof() {
		return nil, p.fail("missing value")
	}
	switch p.peek() {
	case '"':
		return p.parseBasicString(p.pos+2 < len(p.text) && p.text[p.pos:p.pos+3] == `"""`)
	case '[':
		return p.parseArray(path)
	case '{':
		return p.parseInlineTable(path)
	default:
		return p.parseScalar()
	}
}

func (p *tomlParser) parseArray(path []string) ([]any, error) {
	p.pos++
	values := make([]any, 0)
	p.skipValueSpace()
	if !p.eof() && p.peek() == ']' {
		p.pos++
		return values, nil
	}
	for {
		value, err := p.parseValue(path)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
		p.skipValueSpace()
		if p.eof() {
			return nil, p.fail("unterminated array")
		}
		switch p.peek() {
		case ']':
			p.pos++
			return values, nil
		case ',':
			p.pos++
			p.skipValueSpace()
			if !p.eof() && p.peek() == ']' {
				p.pos++
				return values, nil
			}
		default:
			return nil, p.fail("expected ',' or ']' in array")
		}
	}
}

func (p *tomlParser) parseInlineTable(path []string) (map[string]any, error) {
	p.pos++
	table := make(map[string]any)
	p.skipValueSpace()
	if !p.eof() && p.peek() == '}' {
		p.pos++
		p.inlineTable[strings.Join(path, ".")] = true
		return table, nil
	}
	for {
		keys, err := p.parseKeyPath('=')
		if err != nil {
			return nil, err
		}
		if p.eof() || p.peek() != '=' {
			return nil, p.fail("expected key = value in inline table")
		}
		p.pos++
		p.skipHorizontalSpace()
		fullPath := append(append([]string(nil), path...), keys...)
		value, err := p.parseValue(fullPath)
		if err != nil {
			return nil, err
		}
		if err := p.setValue(table, keys, value, path); err != nil {
			return nil, err
		}
		p.skipHorizontalSpace()
		if p.eof() {
			return nil, p.fail("unterminated inline table")
		}
		switch p.peek() {
		case '}':
			p.pos++
			p.inlineTable[strings.Join(path, ".")] = true
			return table, nil
		case ',':
			p.pos++
			p.skipHorizontalSpace()
			if !p.eof() && p.peek() == '}' {
				return nil, p.fail("trailing comma in inline table")
			}
		default:
			return nil, p.fail("expected ',' or '}' in inline table")
		}
	}
}

func (p *tomlParser) setValue(table map[string]any, path []string, value any, base []string) error {
	if len(path) == 0 {
		return p.fail("empty key")
	}
	current := table
	full := append([]string(nil), base...)
	for _, key := range path[:len(path)-1] {
		full = append(full, key)
		canonical := strings.Join(full, ".")
		if p.inlineTable[canonical] {
			return p.fail(fmt.Sprintf("cannot extend inline table %q", canonical))
		}
		child, exists := current[key]
		if !exists {
			nested := make(map[string]any)
			current[key] = nested
			current = nested
			continue
		}
		nested, ok := child.(map[string]any)
		if !ok {
			return p.fail(fmt.Sprintf("%q is not a table", canonical))
		}
		current = nested
	}
	key := path[len(path)-1]
	if _, exists := current[key]; exists {
		return p.fail(fmt.Sprintf("duplicate key %q", key))
	}
	current[key] = value
	return nil
}

func (p *tomlParser) parseBasicString(multiline bool) (string, error) {
	if multiline {
		p.pos += 3
		if !p.eof() && p.peek() == '\n' {
			p.pos++
			p.line++
		} else if p.pos+1 < len(p.text) && p.text[p.pos:p.pos+2] == "\r\n" {
			p.pos += 2
			p.line++
		}
	} else {
		p.pos++
	}
	var out strings.Builder
	for !p.eof() {
		if multiline && p.pos+2 < len(p.text) && p.text[p.pos:p.pos+3] == `"""` {
			p.pos += 3
			return out.String(), nil
		}
		c := p.peek()
		if !multiline && c == '"' {
			p.pos++
			return out.String(), nil
		}
		if c == '\\' {
			p.pos++
			if multiline {
				if p.consumeLineContinuation() {
					continue
				}
			}
			escaped, err := p.parseEscape()
			if err != nil {
				return "", err
			}
			out.WriteString(escaped)
			continue
		}
		if !multiline && (c == '\n' || c == '\r') {
			return "", p.fail("newline in basic string")
		}
		if c == '\r' {
			p.pos++
			if !p.eof() && p.peek() == '\n' {
				p.pos++
			}
			p.line++
			out.WriteByte('\n')
			continue
		}
		if c == '\n' {
			p.line++
		}
		out.WriteByte(c)
		p.pos++
	}
	return "", p.fail("unterminated basic string")
}

func (p *tomlParser) consumeLineContinuation() bool {
	if p.eof() {
		return false
	}
	if p.peek() == '\n' {
		p.pos++
		p.line++
	} else if p.pos+1 < len(p.text) && p.text[p.pos:p.pos+2] == "\r\n" {
		p.pos += 2
		p.line++
	} else {
		return false
	}
	for !p.eof() {
		if p.peek() == ' ' || p.peek() == '\t' {
			p.pos++
			continue
		}
		if p.peek() == '\n' {
			p.pos++
			p.line++
			continue
		}
		if p.pos+1 < len(p.text) && p.text[p.pos:p.pos+2] == "\r\n" {
			p.pos += 2
			p.line++
			continue
		}
		break
	}
	return true
}

func (p *tomlParser) parseEscape() (string, error) {
	if p.eof() {
		return "", p.fail("unterminated escape")
	}
	c := p.peek()
	p.pos++
	switch c {
	case '"':
		return `"`, nil
	case '\\':
		return `\`, nil
	case 'b':
		return "\b", nil
	case 't':
		return "\t", nil
	case 'n':
		return "\n", nil
	case 'f':
		return "\f", nil
	case 'r':
		return "\r", nil
	case 'u', 'U':
		digits := 4
		if c == 'U' {
			digits = 8
		}
		if p.pos+digits > len(p.text) {
			return "", p.fail("short Unicode escape")
		}
		value, err := strconv.ParseUint(p.text[p.pos:p.pos+digits], 16, 32)
		if err != nil || !utf8.ValidRune(rune(value)) {
			return "", p.fail("invalid Unicode escape")
		}
		p.pos += digits
		return string(rune(value)), nil
	default:
		return "", p.fail(fmt.Sprintf("invalid escape sequence \\%c", c))
	}
}

func (p *tomlParser) parseScalar() (any, error) {
	start := p.pos
	for !p.eof() {
		c := p.peek()
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == ',' || c == ']' || c == '}' || c == '#' {
			break
		}
		p.pos++
	}
	if start == p.pos {
		return nil, p.fail("missing value")
	}
	token := p.text[start:p.pos]
	switch token {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	if isTOMLInteger(token) {
		normalized := strings.ReplaceAll(token, "_", "")
		base := 10
		if strings.HasPrefix(normalized, "0x") || strings.HasPrefix(normalized, "0X") {
			base = 0
		} else if strings.HasPrefix(normalized, "0o") || strings.HasPrefix(normalized, "0O") {
			base = 0
		} else if strings.HasPrefix(normalized, "0b") || strings.HasPrefix(normalized, "0B") {
			base = 0
		}
		value, err := strconv.ParseInt(normalized, base, 64)
		if err != nil {
			return nil, p.fail("integer is out of range")
		}
		return value, nil
	}
	return nil, p.fail(fmt.Sprintf("unsupported value %q", token))
}

func isTOMLInteger(value string) bool {
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		return validDigits(value[2:], "0123456789abcdefABCDEF")
	}
	if strings.HasPrefix(value, "0o") || strings.HasPrefix(value, "0O") {
		return validDigits(value[2:], "01234567")
	}
	if strings.HasPrefix(value, "0b") || strings.HasPrefix(value, "0B") {
		return validDigits(value[2:], "01")
	}
	unsigned := strings.TrimPrefix(strings.TrimPrefix(value, "+"), "-")
	if unsigned == "0" {
		return true
	}
	if unsigned == "" || unsigned[0] < '1' || unsigned[0] > '9' {
		return false
	}
	return validDigits(unsigned, "0123456789")
}

func validDigits(value, alphabet string) bool {
	if value == "" || value[0] == '_' || value[len(value)-1] == '_' {
		return false
	}
	previousUnderscore := false
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '_' {
			if previousUnderscore {
				return false
			}
			previousUnderscore = true
			continue
		}
		if !strings.ContainsRune(alphabet, rune(c)) {
			return false
		}
		previousUnderscore = false
	}
	return true
}
