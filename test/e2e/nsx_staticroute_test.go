// This file is for e2e StaticRoute tests.

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const (
	StaticRoute     = "StaticRoute"
	TestNamespace   = "sc-a"
	StaticRouteName = "guestcluster-staticroute-2"
)

// TestStaticRouteBasic verifies that it could successfully realize StaticRoute.
func TestStaticRouteBasic(t *testing.T) {
	setupTest(t, TestNamespace)
	defer teardownTest(t, TestNamespace, defaultTimeout)
	t.Run("case=CreateStaticRoute", CreateStaticRoute)
	t.Run("case=DeleteStaticRoute", DeleteStaticRoute)
}

func waitForStaticRouteCRReady(t *testing.T, ns, staticRouteName string) (res *v1alpha1.StaticRoute) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), 2*defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, 2*defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		res, err = testData.crdClientset.CrdV1alpha1().StaticRoutes(TestNamespace).Get(context.Background(), staticRouteName, v1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			log.Error(err, "Error fetching StaticRoute", "StaticRoute", res, "namespace", ns, "name", staticRouteName)
			return false, fmt.Errorf("error when waiting for StaticRoute %s", staticRouteName)
		}
		log.V(1).Info("StaticRoute status", "status", res.Status)
		for _, con := range res.Status.Conditions {
			if con.Type == v1alpha1.Ready && con.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
	require.NoError(t, err)
	return
}
func CreateStaticRoute(t *testing.T) {
	nextHop := v1alpha1.NextHop{IPAddress: "192.168.0.1"}
	staticRoute := &v1alpha1.StaticRoute{
		Spec: v1alpha1.StaticRouteSpec{
			Network:  "45.1.2.0/24",
			NextHops: []v1alpha1.NextHop{nextHop},
		}}
	staticRoute.Name = StaticRouteName
	_, err := testData.crdClientset.CrdV1alpha1().StaticRoutes(TestNamespace).Create(context.TODO(), staticRoute, v1.CreateOptions{})
	if err != nil && errors.IsAlreadyExists(err) {
		err = nil
	}
	require.NoError(t, err)
	waitForStaticRouteCRReady(t, staticRoute.Name, subnetTestNamespace)
	err = testData.waitForResourceExistOrNot(TestNamespace, common.ResourceTypeStaticRoute, staticRoute.Name, true)
	require.NoError(t, err)
}

func DeleteStaticRoute(t *testing.T) {
	err := testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Delete(context.TODO(), StaticRouteName, v1.DeleteOptions{})
	require.NoError(t, err)

	waitForStaticRouteCRReady(t, StaticRouteName, subnetTestNamespace)
	err = testData.waitForResourceExistOrNot(TestNamespace, common.ResourceTypeStaticRoute, StaticRouteName, false)
	require.NoError(t, err)
}
