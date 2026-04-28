/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package server

import (
	"context"
	"fmt"
	"os"
	"strconv"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
)

// GVR for APIService resources (apiregistration.k8s.io/v1).
var apiserviceGVR = schema.GroupVersionResource{Group: "apiregistration.k8s.io", Version: "v1", Resource: "apiservices"}

const (
	envEASRegisterAPIService         = "EAS_REGISTER_APISERVICE"
	envEASAPIServiceServiceName      = "EAS_APISERVICE_SERVICE_NAME"
	envEASAPIServiceServiceNamespace = "EAS_APISERVICE_SERVICE_NAMESPACE"
	envEASAPIServiceInsecureTLS      = "EAS_APISERVICE_INSECURE_SKIP_TLS"
	envEASAPIServiceGroupPriority    = "EAS_APISERVICE_GROUP_PRIORITY_MINIMUM"
	envEASAPIServiceVersionPriority  = "EAS_APISERVICE_VERSION_PRIORITY"
)

const defaultAPIServiceName = "v1alpha1.eas.nsx.vmware.com"

// registerExtensionAPIService creates or updates the APIService so kube-apiserver aggregates
// requests for eas.nsx.vmware.com/v1alpha1 to the backing Service.
func (s *EASServer) registerExtensionAPIService(ctx context.Context) error {
	if s.restConfig == nil {
		return nil
	}
	if !envBool(envEASRegisterAPIService, true) {
		logger.Log.Info("Skipping Kubernetes APIService registration", "env", envEASRegisterAPIService, "value", os.Getenv(envEASRegisterAPIService))
		return nil
	}

	dc, err := dynamic.NewForConfig(s.restConfig)
	if err != nil {
		return fmt.Errorf("dynamic client for APIService registration: %w", err)
	}

	desired, err := desiredAPIServiceUnstructured()
	if err != nil {
		return err
	}
	ri := dc.Resource(apiserviceGVR)
	name := defaultAPIServiceName

	existing, getErr := ri.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(getErr) {
		if _, err = ri.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create APIService %s: %w", name, err)
		}
		logger.Log.Info("Registered eas APIService with Kubernetes", "name", name)
		return nil
	}
	if getErr != nil {
		return fmt.Errorf("get APIService %s: %w", name, getErr)
	}

	mergeAPIServiceSpec(existing, desired)
	if _, err = ri.Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update APIService %s: %w", name, err)
	}
	logger.Log.Info("Updated eas APIService with Kubernetes", "name", name)
	return nil
}

func mergeAPIServiceSpec(into, from *unstructured.Unstructured) {
	spec, found, _ := unstructured.NestedMap(from.Object, "spec")
	if !found || spec == nil {
		return
	}
	_ = unstructured.SetNestedMap(into.Object, spec, "spec")
}

func desiredAPIServiceUnstructured() (*unstructured.Unstructured, error) {
	svcName := os.Getenv(envEASAPIServiceServiceName)
	if svcName == "" {
		svcName = "nsx-eas"
	}
	svcNS := os.Getenv(envEASAPIServiceServiceNamespace)
	if svcNS == "" {
		svcNS = "vmware-system-nsx"
	}
	insecure := envBool(envEASAPIServiceInsecureTLS, true)
	gp := envInt32(envEASAPIServiceGroupPriority, 1000)
	vp := envInt32(envEASAPIServiceVersionPriority, 15)

	u := &unstructured.Unstructured{Object: map[string]interface{}{}}
	u.SetAPIVersion(apiserviceGVR.Group + "/" + apiserviceGVR.Version)
	u.SetKind("APIService")
	u.SetName(defaultAPIServiceName)

	if err := unstructured.SetNestedMap(u.Object, map[string]interface{}{
		"name":      svcName,
		"namespace": svcNS,
	}, "spec", "service"); err != nil {
		return nil, err
	}
	_ = unstructured.SetNestedField(u.Object, easv1alpha1.GroupVersion.Group, "spec", "group")
	_ = unstructured.SetNestedField(u.Object, easv1alpha1.GroupVersion.Version, "spec", "version")
	_ = unstructured.SetNestedField(u.Object, int64(gp), "spec", "groupPriorityMinimum")
	_ = unstructured.SetNestedField(u.Object, int64(vp), "spec", "versionPriority")
	_ = unstructured.SetNestedField(u.Object, insecure, "spec", "insecureSkipTLSVerify")
	return u, nil
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt32(key string, def int32) int32 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return def
	}
	return int32(n)
}
