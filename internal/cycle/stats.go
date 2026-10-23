package cycle

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// StateDir returns the shared verification state directory.
func StateDir(root string) (string, error) { return stateDir(root) }

// ReadStats reads the unit record shared with the agent runner.
func ReadStats(root, unitID string) map[string]any { return readStats(root, unitID) }

// WriteStats persists a unit record without discarding cycle fields.
func WriteStats(root, unitID string, record map[string]any) error {
	return writeStats(root, unitID, record)
}

func statsDir(root string) (string, error) {
	verified, err := stateDir(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(verified), "stats"), nil
}

func readStats(root, unitID string) map[string]any {
	dir, err := statsDir(root)
	if err != nil {
		return map[string]any{}
	}
	content, err := os.ReadFile(filepath.Join(dir, unitID+".json"))
	if err != nil {
		return map[string]any{}
	}
	var record map[string]any
	if err := json.Unmarshal(content, &record); err != nil || record == nil {
		return map[string]any{}
	}
	return record
}

func writeStats(root, unitID string, record map[string]any) error {
	dir, err := statsDir(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	content, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	return os.WriteFile(filepath.Join(dir, unitID+".json"), content, 0o644)
}
