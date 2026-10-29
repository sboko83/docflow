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

// Entry is one expense record. Amount is expressed in the account's smallest
// unit and is deliberately kept as an integer to avoid rounding surprises.
type Entry struct {
	Date        string
	Amount      int64
	Description string
}

// NewEntry validates command input and joins description words with spaces.
func NewEntry(date, amount string, words []string) (Entry, error) {
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
	return Entry{Date: parsedDate, Amount: parsedAmount, Description: description}, nil
}

// ParseDate accepts only a real calendar date in the documented layout.
func ParseDate(value string) (string, error) {
	date, err := time.Parse(dateLayout, value)
	if err != nil || date.Year() < 1 || date.Format(dateLayout) != value {
		return "", fmt.Errorf("invalid date %q: expected YYYY-MM-DD", value)
	}
	return value, nil
}

// ParseAmount accepts positive base-10 integers that fit in int64.
func ParseAmount(value string) (int64, error) {
	if value == "" {
		return 0, errors.New("amount must be a positive integer")
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, fmt.Errorf("invalid amount %q: use a positive integer", value)
		}
	}
	amount, err := strconv.ParseInt(value, 10, 64)
	if err != nil || amount <= 0 {
		return 0, fmt.Errorf("invalid amount %q: use a positive integer", value)
	}
	return amount, nil
}

// NormalizeDescription joins CLI words while preserving their visible text.
func NormalizeDescription(words []string) (string, error) {
	description := strings.TrimSpace(strings.Join(words, " "))
	if description == "" {
		return "", errors.New("description is required")
	}
	if strings.ContainsAny(description, "\r\n\t") {
		return "", errors.New("description must fit on one journal line")
	}
	return description, nil
}

// Validate checks invariants required by the on-disk representation.
func (entry Entry) Validate() error {
	if _, err := ParseDate(entry.Date); err != nil {
		return err
	}
	if entry.Amount <= 0 {
		return errors.New("amount must be a positive integer")
	}
	if entry.Description == "" || strings.TrimSpace(entry.Description) != entry.Description {
		return errors.New("description must be non-empty and trimmed")
	}
	if strings.ContainsAny(entry.Description, "\r\n\t") {
		return errors.New("description must fit on one journal line")
	}
	return nil
}

// Total adds amounts and reports overflow instead of wrapping the balance.
func Total(entries []Entry) (int64, error) {
	var total int64
	for _, entry := range entries {
		if entry.Amount <= 0 {
			return 0, errors.New("journal contains a non-positive amount")
		}
		if total > math.MaxInt64-entry.Amount {
			return 0, errors.New("balance exceeds the supported integer range")
		}
		total += entry.Amount
	}
	return total, nil
}
