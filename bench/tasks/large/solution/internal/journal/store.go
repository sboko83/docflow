package journal

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const header = "# ledger-v3"
const legacyHeader = "# ledger-v2"

type Store struct{ Path string }

func (s Store) Append(e Entry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	entries, err := s.ReadAll()
	if err != nil {
		return err
	}
	entries = append(entries, e)
	return s.write(entries)
}
func (s Store) ReadAll() ([]Entry, error) {
	if strings.TrimSpace(s.Path) == "" {
		return nil, errors.New("journal path is empty")
	}
	f, err := os.Open(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open journal: %w", err)
	}
	defer f.Close()
	return Read(f)
}
func (s Store) write(entries []Entry) error {
	if strings.TrimSpace(s.Path) == "" {
		return errors.New("journal path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return fmt.Errorf("create journal directory: %w", err)
	}
	var b strings.Builder
	b.WriteString(header + "\n")
	for _, e := range entries {
		if err := e.Validate(); err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s\t%d\t%s\t%s\n", e.Date, e.Amount, e.Currency, e.Description)
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write journal: %w", err)
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace journal: %w", err)
	}
	return nil
}
func Read(r io.Reader) ([]Entry, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024), 1024*1024)
	out := []Entry{}
	version := 1
	line := 0
	for sc.Scan() {
		text := sc.Text()
		line++
		if line == 1 {
			if text == header {
				version = 3
				continue
			}
			if text == legacyHeader {
				version = 2
				continue
			}
		}
		fields := strings.SplitN(text, "\t", 4)
		var e Entry
		var err error
		if version == 1 || version == 2 {
			old := strings.SplitN(text, "\t", 4)
			if version == 2 && len(old) == 4 {
				e.Date, e.Currency, e.Description = old[0], "USD", old[3]
				e.Amount, err = strconv.ParseInt(old[1], 10, 64)
			} else if len(old) >= 3 {
				e.Date, e.Currency, e.Description = old[0], "USD", old[2]
				e.Amount, err = strconv.ParseInt(old[1], 10, 64)
			} else {
				err = errors.New("expected date, amount, and description separated by tabs")
			}
		} else {
			if len(fields) != 4 {
				err = errors.New("expected date, amount, currency, and description separated by tabs")
			} else {
				e.Date, e.Currency, e.Description = fields[0], fields[2], fields[3]
				e.Amount, err = strconv.ParseInt(fields[1], 10, 64)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("journal line %d: invalid amount or record: %w", line, err)
		}
		if err = e.Validate(); err != nil {
			return nil, fmt.Errorf("journal line %d: %w", line, err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read journal: %w", err)
	}
	return out, nil
}

func (s Store) DefaultCurrency() (string, error) {
	data, err := os.ReadFile(s.Path + ".settings")
	if errors.Is(err, os.ErrNotExist) {
		return "USD", nil
	}
	if err != nil {
		return "", fmt.Errorf("read settings: %w", err)
	}
	value := strings.TrimSuffix(string(data), "\n")
	if strings.Contains(value, "\n") || strings.Contains(value, "\r") || ValidateCurrency(value) != nil {
		return "", errors.New("malformed currency settings")
	}
	return value, nil
}
func (s Store) SetCurrency(value string) error {
	if err := ValidateCurrency(value); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(s.Path+".settings", []byte(value+"\n"), 0o600); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}
func (s Store) AddImported(entries []Entry) error {
	current, err := s.ReadAll()
	if err != nil {
		return err
	}
	seen := map[Entry]bool{}
	for _, e := range current {
		seen[e] = true
	}
	for _, e := range entries {
		if err := e.Validate(); err != nil {
			return err
		}
		if !seen[e] {
			current = append(current, e)
			seen[e] = true
		}
	}
	return s.write(current)
}
func ReadImport(format string, r io.Reader) ([]Entry, error) {
	var out []Entry
	switch format {
	case "csv":
		cr := csv.NewReader(r)
		cr.FieldsPerRecord = 4
		rows, err := cr.ReadAll()
		if err != nil {
			return nil, fmt.Errorf("read CSV: %w", err)
		}
		if len(rows) == 0 || strings.Join(rows[0], ",") != "date,amount,currency,description" {
			return nil, errors.New("CSV header must be date,amount,currency,description")
		}
		for i, row := range rows[1:] {
			a, err := ParseAmount(row[1])
			if err != nil {
				return nil, fmt.Errorf("CSV row %d: %w", i+2, err)
			}
			out = append(out, Entry{Date: row[0], Amount: a, Currency: row[2], Description: row[3]})
		}
	case "json":
		dec := json.NewDecoder(r)
		var rows []map[string]json.RawMessage
		if err := dec.Decode(&rows); err != nil {
			return nil, fmt.Errorf("read JSON: %w", err)
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			return nil, errors.New("JSON must contain one array")
		}
		for i, row := range rows {
			if len(row) != 4 || row["date"] == nil || row["amount"] == nil || row["currency"] == nil || row["description"] == nil {
				return nil, fmt.Errorf("JSON row %d must have exactly date, amount, currency, description", i+1)
			}
			var e Entry
			var amount int64
			if json.Unmarshal(row["date"], &e.Date) != nil || json.Unmarshal(row["amount"], &amount) != nil || json.Unmarshal(row["currency"], &e.Currency) != nil || json.Unmarshal(row["description"], &e.Description) != nil {
				return nil, fmt.Errorf("JSON row %d has invalid field types", i+1)
			}
			e.Amount = amount
			out = append(out, e)
		}
	default:
		return nil, fmt.Errorf("unsupported format %q", format)
	}
	for i := range out {
		if err := out[i].Validate(); err != nil {
			return nil, fmt.Errorf("import row %d: %w", i+1, err)
		}
	}
	return out, nil
}
func WriteExport(format string, w io.Writer, entries []Entry) error {
	switch format {
	case "csv":
		cw := csv.NewWriter(w)
		if err := cw.Write([]string{"date", "amount", "currency", "description"}); err != nil {
			return err
		}
		for _, e := range entries {
			if err := cw.Write([]string{e.Date, strconv.FormatInt(e.Amount, 10), e.Currency, e.Description}); err != nil {
				return err
			}
		}
		cw.Flush()
		return cw.Error()
	case "json":
		type row struct {
			Date        string `json:"date"`
			Amount      int64  `json:"amount"`
			Currency    string `json:"currency"`
			Description string `json:"description"`
		}
		rows := make([]row, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, row{e.Date, e.Amount, e.Currency, e.Description})
		}
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		return enc.Encode(rows)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}
func ReadRates(r io.Reader) (map[[2]string][2]int64, error) {
	rates := map[[2]string][2]int64{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		parts := strings.Split(sc.Text(), "\t")
		if len(parts) != 4 {
			return nil, fmt.Errorf("rate line %d: expected FROM, TO, NUMERATOR, DENOMINATOR", n)
		}
		if ValidateCurrency(parts[0]) != nil || ValidateCurrency(parts[1]) != nil {
			return nil, fmt.Errorf("rate line %d: invalid currency", n)
		}
		num, e1 := ParseAmount(parts[2])
		den, e2 := ParseAmount(parts[3])
		if e1 != nil || e2 != nil {
			return nil, fmt.Errorf("rate line %d: numerator and denominator must be positive integers", n)
		}
		key := [2]string{parts[0], parts[1]}
		if _, ok := rates[key]; ok {
			return nil, fmt.Errorf("rate line %d: duplicate conversion", n)
		}
		rates[key] = [2]int64{num, den}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return rates, nil
}
func ConvertTotal(entries []Entry, target string, rates map[[2]string][2]int64) (int64, error) {
	if err := ValidateCurrency(target); err != nil {
		return 0, err
	}
	var total int64
	for _, e := range entries {
		n := e.Amount
		if e.Currency != target {
			rate, ok := rates[[2]string{e.Currency, target}]
			if !ok {
				return 0, fmt.Errorf("missing rate %s to %s", e.Currency, target)
			}
			if n > math.MaxInt64/rate[0] {
				return 0, errors.New("conversion overflow")
			}
			n = n * rate[0] / rate[1]
		}
		if total > math.MaxInt64-n {
			return 0, errors.New("balance exceeds the supported integer range")
		}
		total += n
	}
	return total, nil
}
