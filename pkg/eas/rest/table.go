/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package rest

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// maxColLen is the maximum byte length for any summary column value shown by kubectl.
// Values longer than this are truncated with a trailing "..." to keep output readable.
const maxColLen = 64

// truncateCol truncates s to maxColLen characters, appending "..." when truncation occurs.
func truncateCol(s string) string {
	if len(s) <= maxColLen {
		return s
	}
	return s[:maxColLen-3] + "..."
}

// tableRow builds a metav1.TableRow with the resource name as the first cell
// followed by any additional summary cells.  The Object field carries a
// PartialObjectMetadata so kubectl can display the namespace column correctly.
func tableRow(name, namespace string, extraCells ...interface{}) metav1.TableRow {
	cells := []interface{}{name}
	cells = append(cells, extraCells...)
	return metav1.TableRow{
		Cells: cells,
		Object: runtime.RawExtension{
			Object: &metav1.PartialObjectMetadata{
				TypeMeta:   metav1.TypeMeta{APIVersion: metav1.SchemeGroupVersion.String(), Kind: "PartialObjectMetadata"},
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			},
		},
	}
}
