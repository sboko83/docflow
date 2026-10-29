package journal

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const version2Header = "# ledger-v2"

type Store struct{ Path string }

func (store Store) Append(entry Entry) error {
	if strings.TrimSpace(store.Path) == "" {
		return errors.New("journal path is empty")
	}
	if entry.Category == "" {
		entry.Category = LegacyCategory
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(store.Path), 0o755); err != nil {
		return fmt.Errorf("create journal directory: %w", err)
	}
	data, err := os.ReadFile(store.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read journal: %w", err)
	}
	if err == nil && !strings.HasPrefix(string(data), version2Header+"\n") {
		entries, readErr := Read(strings.NewReader(string(data)))
		if readErr != nil {
			return readErr
		}
		entries = append(entries, entry)
		return store.writeVersion2(entries)
	}
	if errors.Is(err, os.ErrNotExist) {
		return store.writeVersion2([]Entry{entry})
	}
	file, err := os.OpenFile(store.Path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open journal: %w", err)
	}
	defer file.Close()
	if _, err := fmt.Fprintf(file, "%s\t%d\t%s\t%s\n", entry.Date, entry.Amount, entry.Category, entry.Description); err != nil {
		return fmt.Errorf("write journal: %w", err)
	}
	return file.Sync()
}

func (store Store) writeVersion2(entries []Entry) error {
	var b strings.Builder
	b.WriteString(version2Header + "\n")
	for _, entry := range entries {
		if entry.Category == "" {
			entry.Category = LegacyCategory
		}
		if err := entry.Validate(); err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s\t%d\t%s\t%s\n", entry.Date, entry.Amount, entry.Category, entry.Description)
	}
	tmp := store.Path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write journal: %w", err)
	}
	if err := os.Rename(tmp, store.Path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace journal: %w", err)
	}
	return nil
}

func (store Store) ReadAll() ([]Entry, error) {
	if strings.TrimSpace(store.Path) == "" {
		return nil, errors.New("journal path is empty")
	}
	file, err := os.Open(store.Path)
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open journal: %w", err)
	}
	defer file.Close()
	return Read(file)
}

func Read(reader io.Reader) ([]Entry, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	entries := make([]Entry, 0)
	lineNumber, version2 := 0, false
	for scanner.Scan() {
		line := scanner.Text()
		lineNumber++
		if lineNumber == 1 && line == version2Header {
			version2 = true
			continue
		}
		entry, err := parseLine(line, version2)
		if err != nil {
			return nil, fmt.Errorf("journal line %d: %w", lineNumber, err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read journal: %w", err)
	}
	return entries, nil
}

func parseLine(line string, version2 bool) (Entry, error) {
	fields := strings.SplitN(line, "\t", 4)
	if version2 {
		if len(fields) != 4 {
			return Entry{}, errors.New("expected date, amount, category, and description separated by tabs")
		}
	} else if len(fields) == 3 {
		fields = []string{fields[0], fields[1], LegacyCategory, fields[2]}
	} else {
		return Entry{}, errors.New("expected date, amount, and description separated by tabs")
	}
	amount, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return Entry{}, errors.New("amount is not a valid integer")
	}
	entry := Entry{Date: fields[0], Amount: amount, Category: fields[2], Description: fields[3]}
	if err := entry.Validate(); err != nil {
		return Entry{}, err
	}
	return entry, nil
}
