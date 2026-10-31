/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package versioned_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	migration "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/client/clientset/versioned"
	"github.com/vmware-tanzu/nsx-operator/pkg/client/clientset/versioned/fake"
	informers "github.com/vmware-tanzu/nsx-operator/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

func TestMigrationClientUsesClusterScopedGroupEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/migration.network.vmware.com/v1alpha1/networkresourcetransitions/request" {
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"migration.network.vmware.com/v1alpha1","kind":"NetworkResourceTransition","metadata":{"name":"request"}}`))
	}))
	defer server.Close()
	client, err := versioned.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	object, err := client.MigrationV1alpha1().NetworkResourceTransitions().Get(context.Background(), "request", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if object.Name != "request" {
		t.Fatalf("unexpected response: %#v", object)
	}
}

func TestMigrationFakeAndGenericInformerRegistration(t *testing.T) {
	client := fake.NewSimpleClientset(
		&migration.NetworkResourceTransition{ObjectMeta: metav1.ObjectMeta{Name: "request"}},
	)
	if _, err := client.MigrationV1alpha1().NetworkResourceTransitions().Get(context.Background(), "request", metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
	factory := informers.NewSharedInformerFactory(client, time.Minute)
	for _, resource := range []string{"networkresourcetransitions"} {
		if _, err := factory.ForResource(migration.GroupVersion.WithResource(resource)); err != nil {
			t.Fatal(err)
		}
	}
	for _, group := range []string{"crd.nsx.vmware.com", "migration.vmware.com", "migration.nsx.vmware.com"} {
		oldResource := schema.GroupVersionResource{Group: group, Version: "v1alpha1", Resource: "networkresourcetransitions"}
		if _, err := factory.ForResource(oldResource); err == nil {
			t.Fatalf("old API group %s must not expose NetworkResourceTransition", group)
		}
	}
}
