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

// Store reads and appends entries in one journal file.
type Store struct {
	Path string
}

// Append adds one validated record, creating parent directories as needed.
func (store Store) Append(entry Entry) error {
	if strings.TrimSpace(store.Path) == "" {
		return errors.New("journal path is empty")
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(store.Path), 0o755); err != nil {
		return fmt.Errorf("create journal directory: %w", err)
	}
	file, err := os.OpenFile(store.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open journal: %w", err)
	}
	defer file.Close()
	if _, err := fmt.Fprintf(file, "%s\t%d\t%s\n", entry.Date, entry.Amount, entry.Description); err != nil {
		return fmt.Errorf("write journal: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync journal: %w", err)
	}
	return nil
}

// ReadAll loads records in insertion order. An absent journal is empty.
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
	entries, err := Read(file)
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// Read decodes the tab-separated format from any reader.
func Read(reader io.Reader) ([]Entry, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	entries := make([]Entry, 0)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		entry, err := parseLine(scanner.Text())
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

func parseLine(line string) (Entry, error) {
	fields := strings.SplitN(line, "\t", 3)
	if len(fields) != 3 {
		return Entry{}, errors.New("expected date, amount, and description separated by tabs")
	}
	amount, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return Entry{}, errors.New("amount is not a valid integer")
	}
	entry := Entry{Date: fields[0], Amount: amount, Description: fields[2]}
	if err := entry.Validate(); err != nil {
		return Entry{}, err
	}
	return entry, nil
}
