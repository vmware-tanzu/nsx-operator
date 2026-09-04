package unittests

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
)

// Metadata defines the semantic labeling for a unit test.
type Metadata struct {
	Feature     string `json:"feature"`     // Feature group name, also used as the JSON snapshot file name (e.g. "tepless")
	Component   string `json:"component"`   // Core component, e.g. "network-controller"
	Scenario    string `json:"scenario"`    // Specific business scenario, e.g. "default_subnetset_update"
	Description string `json:"description"` // Brief description of this scenario
}

type TestGuard struct {
	t        *testing.T
	metadata Metadata
}

// NewGuard initializes a Labeled Test Guard.
func NewGuard(t *testing.T, meta Metadata) *TestGuard {
	return &TestGuard{
		t:        t,
		metadata: meta,
	}
}

// findProjectRoot walks up from the current working directory to locate the project root containing go.mod
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}

// AssertOutput serializes and validates the actual output against the aggregated feature golden snapshot file.
func (g *TestGuard) AssertOutput(actual interface{}) {
	g.t.Helper()

	if g.metadata.Feature == "" {
		g.t.Fatalf("metadata.Feature is required to determine the snapshot group")
	}

	root, err := findProjectRoot()
	if err != nil {
		g.t.Fatalf("failed to locate project root: %v", err)
	}

	snapshotsDir := filepath.Join(root, "test", "unittests", "snapshots")
	if err := os.MkdirAll(snapshotsDir, 0755); err != nil {
		g.t.Fatalf("failed to create snapshots directory: %v", err)
	}

	// Use sanitized Feature name as the snapshot filename
	featureFileName := sanitizeFileName(g.metadata.Feature)

	// Normalize and sanitize dynamic fields of the actual object
	sanitizedActual := g.sanitize(actual)

	// Structured case payload
	casePayload := map[string]interface{}{
		"metadata": map[string]string{
			"component":   g.metadata.Component,
			"scenario":    g.metadata.Scenario,
			"description": g.metadata.Description,
		},
		"payload": sanitizedActual,
	}

	// Match using go-snaps (UPDATE_SNAPS env var and -snaps.update flag are handled automatically)
	snaps.WithConfig(
		snaps.Dir(snapshotsDir),
		snaps.Filename(featureFileName),
	).MatchJSON(g.t, casePayload)
}

// sanitize masks non-deterministic fields such as ResourceVersion, UIDs, and timestamps.
func (g *TestGuard) sanitize(input interface{}) interface{} {
	bytes, err := json.Marshal(input)
	if err != nil {
		return input
	}

	var data interface{}
	if err := json.Unmarshal(bytes, &data); err != nil {
		return input
	}

	return maskFields(data)
}

// maskFields recursively walks JSON-like map/slice objects and masks dynamic values.
func maskFields(val interface{}) interface{} {
	switch m := val.(type) {
	case map[string]interface{}:
		for k, v := range m {
			// Identify dynamic fields to mask
			switch strings.ToLower(k) {
			case "uid":
				m[k] = "MASKED_UID"
			case "resourceversion":
				m[k] = "MASKED_RESOURCE_VERSION"
			case "creationtimestamp", "timestamp", "deletiontimestamp":
				if v != nil && v != "" {
					m[k] = "MASKED_TIMESTAMP"
				}
			default:
				m[k] = maskFields(v)
			}
		}
		return m
	case []interface{}:
		for i, v := range m {
			m[i] = maskFields(v)
		}
		return m
	default:
		return val
	}
}

// sanitizeFileName replaces invalid file path characters with underscores.
func sanitizeFileName(name string) string {
	reg := regexp.MustCompile(`[^a-zA-Z0-9_\-]`)
	return reg.ReplaceAllString(name, "_")
}
