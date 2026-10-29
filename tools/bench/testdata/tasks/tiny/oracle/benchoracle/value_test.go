package benchoracle

import (
	"testing"
	"tinybase"
)

func TestValue(t *testing.T) {
	if tinybase.Value() != 2 { t.Fatalf("Value() = %d, want 2", tinybase.Value()) }
}
