/* Copyright © 2025 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestSha1(t *testing.T) {
	assert.Equal(t, Sha1("name"), "6ae999552a0d2dca14d62e2bc8b764d377b1dd6c")
}

func TestNewSha1(t *testing.T) {
	assert.Equal(t, "chl6tk4k3f8cb0c1lfpdlfjtsyfuess", Sha1WithCustomizedCharset("name"))
	assert.Equal(t, "eqbb380p8jcm2zjaxwy0dmvb4hyevkw", Sha1WithCustomizedCharset("namee"))

	allowedChars := sets.New[rune]()
	for _, c := range HashCharset {
		allowedChars.Insert(c)
	}
	randUID, err := uuid.NewRandom()
	require.NoError(t, err)
	hashString := Sha1WithCustomizedCharset(randUID.String())
	// Verify all chars in the hash string are contained in base62Chars.
	for _, c := range hashString {
		assert.True(t, allowedChars.Has(c))
	}
}

func TestCollisionWithHashCharset(t *testing.T) {
	hashLength := 5
	newUUID, err := uuid.NewRandom()
	require.NoError(t, err)

	hashStr := Sha1WithCustomizedCharset(newUUID.String())[:hashLength]
	timestamp := time.Now().UnixMilli()
	hashStrWithTime := Sha1WithCustomizedCharset(fmt.Sprintf("%s-%d", newUUID.String(), timestamp))[:hashLength]
	require.NotEqual(t, hashStr, hashStrWithTime)
}
