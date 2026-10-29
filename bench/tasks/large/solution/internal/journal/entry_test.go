package journal

import "testing"

func TestCurrencyAndAmountValidation(t *testing.T) {
	for _, value := range []string{"USD", "EUR", "ABC"} {
		if err := ValidateCurrency(value); err != nil {
			t.Errorf("ValidateCurrency(%q): %v", value, err)
		}
	}
	for _, value := range []string{"usd", "US", "US1", "éUR"} {
		if ValidateCurrency(value) == nil {
			t.Errorf("ValidateCurrency(%q) accepted", value)
		}
	}
	if _, err := NewEntry("2025-01-01", "0", []string{"tea"}, "USD"); err == nil {
		t.Fatal("accepted zero amount")
	}
	if _, err := NewEntry("2025-02-29", "1", []string{"tea"}, "USD"); err == nil {
		t.Fatal("accepted invalid date")
	}
}
