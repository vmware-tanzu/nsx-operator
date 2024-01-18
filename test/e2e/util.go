package e2e

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// IsDirEmpty checks whether a directory is empty or not.
func IsDirEmpty(name string) (bool, error) {
	f, err := os.Open(name)
	if err != nil {
		return false, err
	}
	defer f.Close()

	_, err = f.Readdirnames(1)
	if err == io.EOF {
		return true, nil
	}
	return false, err
}

func assertNil(t *testing.T, object interface{}, msgAndArgs ...interface{}) bool {
	if assert.Nil(t, object, msgAndArgs...) {
		return true
	}
	panic("assertion failed")
}

func assertNotNil(t *testing.T, object interface{}, msgAndArgs ...interface{}) bool {
	if assert.NotNil(t, object, msgAndArgs...) {
		t.Logf("assertNotNil: %v", object)
		return true
	}
	panic("assertion failed")
}

func assertTrue(t *testing.T, value bool, msgAndArgs ...interface{}) bool {
	if assert.True(t, value, msgAndArgs...) {
		return true
	}
	panic("assertion failed")
}

func assertFalse(t *testing.T, value bool, msgAndArgs ...interface{}) bool {
	if assert.False(t, value, msgAndArgs...) {
		return true
	}
	panic("assertion failed")
}
