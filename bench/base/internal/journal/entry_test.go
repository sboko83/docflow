package journal

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestParseDateAcceptsCalendarDates(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "ordinary date", input: "2026-10-29"},
		{name: "leap day", input: "2024-02-29"},
		{name: "year boundary", input: "2000-01-01"},
		{name: "last day", input: "2025-12-31"},
		{name: "single digit month", input: "2025-03-07"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseDate(test.input)
			if err != nil {
				t.Fatalf("ParseDate(%q) returned error: %v", test.input, err)
			}
			if got != test.input {
				t.Fatalf("ParseDate(%q) = %q", test.input, got)
			}
		})
	}
}

func TestParseDateRejectsMalformedAndImpossibleDates(t *testing.T) {
	inputs := []string{
		"", "2025", "2025-1-01", "2025-01-1", "25-01-01",
		"2025/01/01", "2025-00-01", "2025-13-01", "2025-01-00",
		"2025-01-32", "2025-02-29", "2025-04-31", " 2025-01-01",
		"2025-01-01 ", "2025-01-01T00:00:00Z", "0000-01-01",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			if got, err := ParseDate(input); err == nil {
				t.Fatalf("ParseDate(%q) = %q, want error", input, got)
			}
		})
	}
}

func TestParseAmountAcceptsPositiveIntegers(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{input: "1", want: 1},
		{input: "42", want: 42},
		{input: "0007", want: 7},
		{input: "9007199254740993", want: 9007199254740993},
		{input: "9223372036854775807", want: math.MaxInt64},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := ParseAmount(test.input)
			if err != nil {
				t.Fatalf("ParseAmount(%q) returned error: %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("ParseAmount(%q) = %d, want %d", test.input, got, test.want)
			}
		})
	}
}

func TestParseAmountRejectsInvalidIntegers(t *testing.T) {
	inputs := []string{
		"", "0", "-1", "+1", "1.0", "1,00", "1e3", " 2", "2 ",
		"two", "１２", "9223372036854775808",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			if got, err := ParseAmount(input); err == nil {
				t.Fatalf("ParseAmount(%q) = %d, want error", input, got)
			}
		})
	}
}

func TestNormalizeDescriptionJoinsWords(t *testing.T) {
	tests := []struct {
		name  string
		words []string
		want  string
	}{
		{name: "one word", words: []string{"coffee"}, want: "coffee"},
		{name: "several words", words: []string{"coffee", "and", "cake"}, want: "coffee and cake"},
		{name: "preserves punctuation", words: []string{"lunch:", "soup,", "bread!"}, want: "lunch: soup, bread!"},
		{name: "trims outer whitespace", words: []string{"  taxi  ", "  home  "}, want: "taxi     home"},
		{name: "unicode", words: []string{"кофе", "東京"}, want: "кофе 東京"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeDescription(test.words)
			if err != nil {
				t.Fatalf("NormalizeDescription returned error: %v", err)
			}
			if got != test.want {
				t.Fatalf("NormalizeDescription(%q) = %q, want %q", test.words, got, test.want)
			}
		})
	}
}

func TestNormalizeDescriptionRejectsEmptyAndLineBreaks(t *testing.T) {
	tests := [][]string{
		nil,
		{},
		{""},
		{" ", "\t"},
		{"one\ntwo"},
		{"one\rtwo"},
		{"first", "second\nthird"},
	}
	for index, words := range tests {
		t.Run(strings.Join(words, "_"), func(t *testing.T) {
			if got, err := NormalizeDescription(words); err == nil {
				t.Fatalf("NormalizeDescription(%q) = %q, want error", words, got)
			}
		})
		_ = index
	}
}

func TestNewEntryCombinesValidation(t *testing.T) {
	got, err := NewEntry("2025-06-03", "1250", []string{"train", "ticket"})
	if err != nil {
		t.Fatalf("NewEntry returned error: %v", err)
	}
	want := Entry{Date: "2025-06-03", Amount: 1250, Description: "train ticket"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NewEntry = %#v, want %#v", got, want)
	}
}

func TestNewEntryReturnsFieldErrors(t *testing.T) {
	tests := []struct {
		name   string
		date   string
		amount string
		words  []string
	}{
		{name: "date", date: "tomorrow", amount: "1", words: []string{"bus"}},
		{name: "amount", date: "2025-01-01", amount: "-1", words: []string{"bus"}},
		{name: "description", date: "2025-01-01", amount: "1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewEntry(test.date, test.amount, test.words); err == nil {
				t.Fatal("NewEntry returned no error")
			}
		})
	}
}

func TestEntryValidateChecksStoredInvariants(t *testing.T) {
	valid := Entry{Date: "2025-01-02", Amount: 10, Description: "coffee"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid entry rejected: %v", err)
	}
	tests := []struct {
		name  string
		entry Entry
	}{
		{name: "invalid date", entry: Entry{Date: "2025-02-30", Amount: 1, Description: "x"}},
		{name: "zero amount", entry: Entry{Date: "2025-01-01", Amount: 0, Description: "x"}},
		{name: "negative amount", entry: Entry{Date: "2025-01-01", Amount: -2, Description: "x"}},
		{name: "empty description", entry: Entry{Date: "2025-01-01", Amount: 1}},
		{name: "leading space", entry: Entry{Date: "2025-01-01", Amount: 1, Description: " x"}},
		{name: "trailing space", entry: Entry{Date: "2025-01-01", Amount: 1, Description: "x "}},
		{name: "tab", entry: Entry{Date: "2025-01-01", Amount: 1, Description: "x\ty"}},
		{name: "newline", entry: Entry{Date: "2025-01-01", Amount: 1, Description: "x\ny"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.entry.Validate(); err == nil {
				t.Fatal("invalid entry accepted")
			}
		})
	}
}

func TestTotalSumsEntries(t *testing.T) {
	tests := []struct {
		name    string
		entries []Entry
		want    int64
	}{
		{name: "empty", entries: nil, want: 0},
		{name: "one", entries: []Entry{{Amount: 4}}, want: 4},
		{name: "many", entries: []Entry{{Amount: 4}, {Amount: 6}, {Amount: 11}}, want: 21},
		{name: "maximum safe", entries: []Entry{{Amount: math.MaxInt64}}, want: math.MaxInt64},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Total(test.entries)
			if err != nil {
				t.Fatalf("Total returned error: %v", err)
			}
			if got != test.want {
				t.Fatalf("Total = %d, want %d", got, test.want)
			}
		})
	}
}

func TestTotalRejectsInvalidAmountAndOverflow(t *testing.T) {
	tests := []struct {
		name    string
		entries []Entry
	}{
		{name: "zero", entries: []Entry{{Amount: 0}}},
		{name: "negative", entries: []Entry{{Amount: -1}}},
		{name: "overflow", entries: []Entry{{Amount: math.MaxInt64}, {Amount: 1}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got, err := Total(test.entries); err == nil {
				t.Fatalf("Total = %d, want error", got)
			}
		})
	}
}
