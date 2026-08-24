package unittests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeFileName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"normalName", "normalName"},
		{"test/case/with/slashes", "test_case_with_slashes"},
		{"special@char#name$", "special_char_name_"},
		{"spaces in name", "spaces_in_name"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := sanitizeFileName(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestFindProjectRoot(t *testing.T) {
	root, err := findProjectRoot()
	require.NoError(t, err)
	assert.NotEmpty(t, root)

	// Verify go.mod exists in the found root
	_, err = os.Stat(filepath.Join(root, "go.mod"))
	assert.NoError(t, err)
}

func TestFindProjectRoot_Error(t *testing.T) {
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(oldWd)

	tempDir := t.TempDir()
	err = os.Chdir(tempDir)
	require.NoError(t, err)

	// Since tempDir has no go.mod and none of its parents do, findProjectRoot should fail
	root, err := findProjectRoot()
	assert.Error(t, err)
	assert.Empty(t, root)
}

func TestMaskFields(t *testing.T) {
	input := map[string]interface{}{
		"name":              "test-resource",
		"uid":               "1234-abcd-5678",
		"resourceVersion":   "99999",
		"creationTimestamp": "2026-08-24T14:06:00Z",
		"deletionTimestamp": "", // Should not mask if empty
		"nested": map[string]interface{}{
			"uid":        "nested-uid-123",
			"nonDynamic": "keep-me",
		},
		"list": []interface{}{
			map[string]interface{}{
				"uid": "list-uid",
			},
		},
	}

	expected := map[string]interface{}{
		"name":              "test-resource",
		"uid":               "MASKED_UID",
		"resourceVersion":   "MASKED_RESOURCE_VERSION",
		"creationTimestamp": "MASKED_TIMESTAMP",
		"deletionTimestamp": "",
		"nested": map[string]interface{}{
			"uid":        "MASKED_UID",
			"nonDynamic": "keep-me",
		},
		"list": []interface{}{
			map[string]interface{}{
				"uid": "MASKED_UID",
			},
		},
	}

	result := maskFields(input)
	assert.Equal(t, expected, result)
}

func TestTestGuard_Sanitize(t *testing.T) {
	guard := NewGuard(t, Metadata{
		Feature:     "TestFeature",
		Component:   "TestComponent",
		Scenario:    "TestScenario",
		Description: "TestDesc",
	})

	type Resource struct {
		Name            string                 `json:"name"`
		UID             string                 `json:"uid"`
		ResourceVersion string                 `json:"resourceVersion"`
		Metadata        map[string]interface{} `json:"metadata"`
	}

	res := Resource{
		Name:            "my-resource",
		UID:             "some-uid",
		ResourceVersion: "123",
		Metadata: map[string]interface{}{
			"uid": "inner-uid",
		},
	}

	sanitized := guard.sanitize(res)

	// Convert output to map for verification
	bytes, err := json.Marshal(sanitized)
	require.NoError(t, err)

	var resultMap map[string]interface{}
	err = json.Unmarshal(bytes, &resultMap)
	require.NoError(t, err)

	assert.Equal(t, "my-resource", resultMap["name"])
	assert.Equal(t, "MASKED_UID", resultMap["uid"])
	assert.Equal(t, "MASKED_RESOURCE_VERSION", resultMap["resourceVersion"])

	innerMeta := resultMap["metadata"].(map[string]interface{})
	assert.Equal(t, "MASKED_UID", innerMeta["uid"])
}

func TestTestGuard_Sanitize_Error(t *testing.T) {
	guard := NewGuard(t, Metadata{
		Feature: "TestFeature",
	})

	// Pass a channel, which cannot be marshaled to JSON
	ch := make(chan int)
	result := guard.sanitize(ch)
	assert.Equal(t, ch, result)
}

func TestAssertOutput_Success(t *testing.T) {
	// Save and restore UPDATE_SNAPS
	oldEnv := os.Getenv("UPDATE_SNAPS")
	defer os.Setenv("UPDATE_SNAPS", oldEnv)

	os.Setenv("UPDATE_SNAPS", "true")

	guard := NewGuard(t, Metadata{
		Feature:     "my_test_feature",
		Component:   "my-component",
		Scenario:    "my-scenario",
		Description: "my-description",
	})

	payload := map[string]interface{}{
		"name":            "test-resource",
		"uid":             "1234-abcd",
		"resourceVersion": "555",
	}

	// This will generate/update the snapshot file using go-snaps
	guard.AssertOutput(payload)
}
