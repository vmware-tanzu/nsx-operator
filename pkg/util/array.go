/* Copyright © 2025 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"fmt"
	"strings"

	"github.com/gookit/goutil/arrutil"
	"github.com/gookit/goutil/strutil"
)

func RemoveDuplicateStr(strSlice []string) []string {
	return arrutil.Unique(strSlice)
}

func ToUpper(obj interface{}) string {
	str := fmt.Sprintf("%s", obj)
	return strutil.Upper(str)
}

func Contains(s []string, str string) bool {
	return arrutil.Contains(s, str)
}

func FilterOut(s []string, strToRemove string) []string {
	var result []string
	for _, element := range s {
		if element != strToRemove {
			result = append(result, element)
		}
	}
	return result
}

func If(condition bool, trueVal, falseVal interface{}) interface{} {
	if condition {
		return trueVal
	} else {
		return falseVal
	}
}

func Capitalize(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func connectStrings(sep string, parts ...string) string {
	strParts := make([]string, 0)
	for _, part := range parts {
		if len(part) > 0 {
			strParts = append(strParts, part)
		}
	}
	return strings.Join(strParts, sep)
}
