package journal

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const dateLayout = "2006-01-02"

// Entry is one expense record, represented in the smallest unit of its currency.
type Entry struct {
	Date        string
	Amount      int64
	Currency    string
	Description string
}

type Recurrence struct {
	Day         int
	Amount      int64
	Currency    string
	Description string
}

func NewEntry(date, amount string, words []string, currency string) (Entry, error) {
	parsedDate, err := ParseDate(date)
	if err != nil {
		return Entry{}, err
	}
	parsedAmount, err := ParseAmount(amount)
	if err != nil {
		return Entry{}, err
	}
	description, err := NormalizeDescription(words)
	if err != nil {
		return Entry{}, err
	}
	if err := ValidateCurrency(currency); err != nil {
		return Entry{}, err
	}
	return Entry{Date: parsedDate, Amount: parsedAmount, Currency: currency, Description: description}, nil
}

func ParseDate(value string) (string, error) {
	date, err := time.Parse(dateLayout, value)
	if err != nil || date.Year() < 1 || date.Format(dateLayout) != value {
		return "", fmt.Errorf("invalid date %q: expected YYYY-MM-DD", value)
	}
	return value, nil
}

func ParseAmount(value string) (int64, error) {
	if value == "" {
		return 0, errors.New("amount must be a positive integer")
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid amount %q: use a positive integer", value)
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid amount %q: use a positive integer", value)
	}
	return n, nil
}

func ValidateCurrency(value string) error {
	if len(value) != 3 {
		return fmt.Errorf("invalid currency %q: expected three uppercase ASCII letters", value)
	}
	for _, c := range value {
		if c < 'A' || c > 'Z' {
			return fmt.Errorf("invalid currency %q: expected three uppercase ASCII letters", value)
		}
	}
	return nil
}

func NormalizeDescription(words []string) (string, error) {
	d := strings.TrimSpace(strings.Join(words, " "))
	if d == "" {
		return "", errors.New("description is required")
	}
	if strings.ContainsAny(d, "\r\n\t") {
		return "", errors.New("description must fit on one journal line")
	}
	return d, nil
}

func (e Entry) Validate() error {
	if _, err := ParseDate(e.Date); err != nil {
		return err
	}
	if e.Amount <= 0 {
		return errors.New("amount must be a positive integer")
	}
	if err := ValidateCurrency(e.Currency); err != nil {
		return err
	}
	if e.Description == "" || strings.TrimSpace(e.Description) != e.Description || strings.ContainsAny(e.Description, "\r\n\t") {
		return errors.New("description must be non-empty, trimmed, and fit on one journal line")
	}
	return nil
}

func Total(entries []Entry) (int64, error) {
	var total int64
	for _, e := range entries {
		if err := e.Validate(); err != nil {
			return 0, err
		}
		if total > math.MaxInt64-e.Amount {
			return 0, errors.New("balance exceeds the supported integer range")
		}
		total += e.Amount
	}
	return total, nil
}
