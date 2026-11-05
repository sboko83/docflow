package journal

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const headerPrefix = "# ledger-v2 next="

// Record is a journal entry with its stable ID.
type Record struct {
	ID int64
	Entry
}

// Journal is the decoded journal. Version is 1 for a base journal without a
// header and 2 otherwise; Next is the ID the next added record gets.
type Journal struct {
	Version int
	Next    int64
	Records []Record
}

// ErrLocked reports that another process held the lock past the timeout.
var ErrLocked = errors.New("journal is locked by another process")

// WriteOptions control locking and crash simulation of a write.
type WriteOptions struct {
	LockTimeout time.Duration
	LockStale   time.Duration
	// BeforeRename runs after the temporary file is synced and closed and
	// before it replaces the journal. Tests use it to simulate a crash.
	BeforeRename func()
}

// Store reads and rewrites one journal file.
type Store struct {
	Path string
}

// Load reads the journal without locking. An absent journal is an empty
// version 2 journal whose next ID is 1.
func (store Store) Load() (Journal, error) {
	if strings.TrimSpace(store.Path) == "" {
		return Journal{}, errors.New("journal path is empty")
	}
	data, err := os.ReadFile(store.Path)
	if errors.Is(err, os.ErrNotExist) {
		return Journal{Version: 2, Next: 1, Records: []Record{}}, nil
	}
	if err != nil {
		return Journal{}, fmt.Errorf("read journal: %w", err)
	}
	return Parse(data)
}

// ReadAll returns the journal's entries in file order.
func (store Store) ReadAll() ([]Entry, error) {
	journal, err := store.Load()
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(journal.Records))
	for _, record := range journal.Records {
		entries = append(entries, record.Entry)
	}
	return entries, nil
}

// Read decodes a journal of either version from a reader.
func Read(reader io.Reader) ([]Entry, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read journal: %w", err)
	}
	journal, err := Parse(data)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(journal.Records))
	for _, record := range journal.Records {
		entries = append(entries, record.Entry)
	}
	return entries, nil
}

// Parse decodes version 1 and version 2 journals.
func Parse(data []byte) (Journal, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	journal := Journal{Version: 1, Next: 1, Records: []Record{}}
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if lineNumber == 1 && strings.HasPrefix(line, headerPrefix) {
			next, err := parseID(strings.TrimPrefix(line, headerPrefix))
			if err != nil {
				return Journal{}, fmt.Errorf("journal line 1: invalid next ID: %w", err)
			}
			journal.Version, journal.Next = 2, next
			continue
		}
		if strings.HasPrefix(line, "#") {
			return Journal{}, fmt.Errorf("journal line %d: unexpected header %q", lineNumber, line)
		}
		var record Record
		var err error
		if journal.Version == 2 {
			record, err = parseRecord(line)
		} else {
			record.Entry, err = parseLegacy(line)
			record.ID = int64(len(journal.Records) + 1)
		}
		if err != nil {
			return Journal{}, fmt.Errorf("journal line %d: %w", lineNumber, err)
		}
		if n := len(journal.Records); n > 0 && record.ID <= journal.Records[n-1].ID {
			return Journal{}, fmt.Errorf("journal line %d: ID %d does not increase", lineNumber, record.ID)
		}
		journal.Records = append(journal.Records, record)
	}
	if err := scanner.Err(); err != nil {
		return Journal{}, fmt.Errorf("read journal: %w", err)
	}
	if journal.Version == 1 {
		journal.Next = int64(len(journal.Records) + 1)
		return journal, nil
	}
	for _, record := range journal.Records {
		if record.ID >= journal.Next {
			return Journal{}, fmt.Errorf("journal record ID %d is not below next %d", record.ID, journal.Next)
		}
	}
	return journal, nil
}

// Encode writes the journal in version 2 format.
func (journal Journal) Encode() []byte {
	var out bytes.Buffer
	fmt.Fprintf(&out, "%s%d\n", headerPrefix, journal.Next)
	for _, record := range journal.Records {
		fmt.Fprintf(&out, "%d\t%s\t%d\t%s\n", record.ID, record.Date, record.Amount, record.Description)
	}
	return out.Bytes()
}

// Add appends an entry with the next ID and returns that ID.
func (journal *Journal) Add(entry Entry) (int64, error) {
	if err := entry.Validate(); err != nil {
		return 0, err
	}
	id := journal.Next
	journal.Records = append(journal.Records, Record{ID: id, Entry: entry})
	journal.Next++
	return id, nil
}

// Delete removes the record with the given ID; next stays unchanged.
func (journal *Journal) Delete(id int64) error {
	for i, record := range journal.Records {
		if record.ID == id {
			journal.Records = append(journal.Records[:i], journal.Records[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("no entry with ID %d", id)
}

// ParseID accepts a positive decimal integer.
func ParseID(value string) (int64, error) {
	id, err := parseID(value)
	if err != nil {
		return 0, fmt.Errorf("invalid ID %q: use a positive integer", value)
	}
	return id, nil
}

func parseID(value string) (int64, error) {
	if value == "" {
		return 0, errors.New("empty")
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, errors.New("not a decimal integer")
		}
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("not a positive integer")
	}
	return id, nil
}

func parseRecord(line string) (Record, error) {
	fields := strings.SplitN(line, "\t", 2)
	if len(fields) != 2 {
		return Record{}, errors.New("expected ID, date, amount, and description separated by tabs")
	}
	id, err := parseID(fields[0])
	if err != nil {
		return Record{}, fmt.Errorf("invalid ID: %w", err)
	}
	entry, err := parseLegacy(fields[1])
	if err != nil {
		return Record{}, err
	}
	return Record{ID: id, Entry: entry}, nil
}

func parseLegacy(line string) (Entry, error) {
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

// Update locks the journal, applies change to its current contents and
// replaces the file atomically. The journal is unchanged when change fails.
func (store Store) Update(options WriteOptions, change func(*Journal) error) error {
	if strings.TrimSpace(store.Path) == "" {
		return errors.New("journal path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(store.Path), 0o755); err != nil {
		return fmt.Errorf("create journal directory: %w", err)
	}
	lock := store.Path + ".lock"
	if err := acquire(lock, options.LockTimeout, options.LockStale); err != nil {
		return err
	}
	defer os.Remove(lock)
	journal, err := store.Load()
	if err != nil {
		return err
	}
	if err := change(&journal); err != nil {
		return err
	}
	return store.replace(journal.Encode(), options.BeforeRename)
}

func (store Store) replace(data []byte, beforeRename func()) error {
	temporary := store.Path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary journal: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		os.Remove(temporary)
		return fmt.Errorf("write temporary journal: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(temporary)
		return fmt.Errorf("sync temporary journal: %w", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(temporary)
		return fmt.Errorf("close temporary journal: %w", err)
	}
	if beforeRename != nil {
		beforeRename()
	}
	if err := os.Rename(temporary, store.Path); err != nil {
		os.Remove(temporary)
		return fmt.Errorf("replace journal: %w", err)
	}
	return nil
}

func acquire(lock string, timeout, stale time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		file, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(file, "%d\n", os.Getpid())
			return file.Close()
		}
		// Windows reports a lock that is being removed as access denied.
		if !errors.Is(err, os.ErrExist) && !errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("create lock: %w", err)
		}
		if info, statErr := os.Stat(lock); statErr == nil && time.Since(info.ModTime()) > stale {
			os.Remove(lock)
			continue
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%w: %s", ErrLocked, lock)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
