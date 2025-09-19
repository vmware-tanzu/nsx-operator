/* Copyright © 2025 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package auth

// TokenProvider provides token.
type TokenProvider interface {
	// GetToken gets a JWT, parameter refreshToken indicates whether a new token value is to be retrieved.
	GetToken(refreshToken bool) (string, error)
	// GetHeaderValue gets token value from a JWT， the value format likes "Bearer %s".
	HeaderValue(token string) string
}
