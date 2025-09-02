/* Copyright © 2025 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"crypto/sha1" // #nosec G505: not used for security
	"fmt"
	"math/big"
)

func truncateLabelHash(data string) string {
	return Sha1(data)[:HashLength]
}

func Sha1(data string) string {
	sum := getSha1Bytes(data)
	return fmt.Sprintf("%x", sum)
}

func getSha1Bytes(data string) []byte {
	h := sha1.New() // #nosec G401: not used for security
	h.Write([]byte(data))
	sum := h.Sum(nil)
	return sum
}

// Sha1WithCustomizedCharset uses the chars in `HashCharset` to present the hash result on the input data. We now use Sha1 as
// the hash algorithm.
func Sha1WithCustomizedCharset(data string) string {
	sum := getSha1Bytes(data)
	value := new(big.Int).SetBytes(sum[:])
	base := big.NewInt(int64(len(HashCharset)))
	var result []byte
	for value.Cmp(big.NewInt(0)) > 0 {
		mod := new(big.Int).Mod(value, base)
		result = append(result, HashCharset[mod.Int64()])
		value.Div(value, base)
	}

	// Reverse the result because the encoding process generates characters in reverse order
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return string(result)
}

func truncateNameOrIDHash(data string) string {
	return Sha1WithCustomizedCharset(data)[:Base62HashLength]
}

func TruncateUIDHash(uid string) string {
	return Sha1WithCustomizedCharset(uid)[:UUIDHashLength]
}
