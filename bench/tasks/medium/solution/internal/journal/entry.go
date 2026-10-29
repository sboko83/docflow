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
const LegacyCategory = "Uncategorized"

type Entry struct {
	Date        string
	Amount      int64
	Category    string
	Description string
}

func NewEntry(date, amount string, words []string, categories ...string) (Entry, error) {
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
	category := LegacyCategory
	if len(categories) > 0 {
		category = categories[0]
	}
	entry := Entry{Date: parsedDate, Amount: parsedAmount, Category: category, Description: description}
	if err := entry.Validate(); err != nil {
		return Entry{}, err
	}
	return entry, nil
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

func ValidateCategory(category string) error {
	if strings.TrimSpace(category) == "" || strings.ContainsAny(category, "\t\r\n") {
		return errors.New("category must be non-empty and contain no tabs or newlines")
	}
	return nil
}

func (entry Entry) Validate() error {
	if _, err := ParseDate(entry.Date); err != nil {
		return err
	}
	if entry.Amount <= 0 {
		return errors.New("amount must be a positive integer")
	}
	if err := ValidateCategory(entry.Category); err != nil {
		return err
	}
	if entry.Description == "" || strings.TrimSpace(entry.Description) != entry.Description {
		return errors.New("description must be non-empty and trimmed")
	}
	if strings.ContainsAny(entry.Description, "\r\n\t") {
		return errors.New("description must fit on one journal line")
	}
	return nil
}

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
