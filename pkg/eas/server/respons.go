/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
)

func writeJSON(w http.ResponseWriter, status int, obj interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(obj); err != nil {
		log := logger.Log
		log.Error(err, "Failed to encode response")
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	log := logger.Log
	log.Debug("Returning error response", "status", status, "message", message)
	writeAPIError(w, newStatusError(status, message))
}

func writeAPIError(w http.ResponseWriter, err error) {
	var apiStatus apierrors.APIStatus
	if errors.As(err, &apiStatus) {
		status := apiStatus.Status()
		if status.Kind == "" {
			status.Kind = "Status"
		}
		if status.APIVersion == "" {
			status.APIVersion = metav1.Unversioned.String()
		}
		writeJSON(w, int(status.Code), &status)
		return
	}

	status := apierrors.NewInternalError(err).Status()
	status.Message = err.Error()
	status.Kind = "Status"
	status.APIVersion = metav1.Unversioned.String()
	writeJSON(w, int(status.Code), &status)
}

func newStatusError(status int, message string) *apierrors.StatusError {
	if status == http.StatusInternalServerError {
		err := apierrors.NewInternalError(errors.New(message))
		err.ErrStatus.Message = message
		return err
	}

	err := apierrors.NewGenericServerResponse(status, "", schema.GroupResource{}, "", message, 0, false)
	err.ErrStatus.Message = message
	err.ErrStatus.Details = nil
	return err
}

// wantsTable returns true if the client requested a Table format via the Accept header.
func wantsTable(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "as=Table")
}

// writeResponse writes a Table format if requested by the client, otherwise writes raw JSON.
func writeResponse(w http.ResponseWriter, r *http.Request, obj interface{}, columns []metav1.TableColumnDefinition, rows []metav1.TableRow) {
	if wantsTable(r) {
		writeJSON(w, http.StatusOK, &metav1.Table{
			TypeMeta:          metav1.TypeMeta{APIVersion: metav1.SchemeGroupVersion.String(), Kind: "Table"},
			ColumnDefinitions: columns,
			Rows:              rows,
		})
		return
	}
	writeJSON(w, http.StatusOK, obj)
}

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
