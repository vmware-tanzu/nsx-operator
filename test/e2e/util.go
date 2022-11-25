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

func assert_nil(t *testing.T, object interface{}, msgAndArgs ...interface{}) bool {
	if assert.Nil(t, object, msgAndArgs...) {
		return true
	}
	panic("assertion failed")
}

func assert_notnil(t *testing.T, object interface{}, msgAndArgs ...interface{}) bool {
	if assert.NotNil(t, object, msgAndArgs...) {
		t.Logf("assert_notnil: %v", object)
		return true
	}
	panic("assertion failed")
}

func assert_true(t *testing.T, value bool, msgAndArgs ...interface{}) bool {
	if assert.True(t, value, msgAndArgs...) {
		return true
	}
	panic("assertion failed")
}

func assert_false(t *testing.T, value bool, msgAndArgs ...interface{}) bool {
	if assert.False(t, value, msgAndArgs...) {
		return true
	}
	panic("assertion failed")
}
