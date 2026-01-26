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
	StaticRoute             = "StaticRoute"
	StaticRouteName         = "guestcluster-staticroute-2"
	IpAddressAllocationName = "staticroute-ipalloc"
)

var TestNamespace = fmt.Sprintf("staticroute-%s", getRandomString())

// TestStaticRouteBasic verifies that it could successfully realize StaticRoute.
func TestStaticRouteBasic(t *testing.T) {
	TrackTest(t)
	StartParallel(t)
	testNamespace := fmt.Sprintf("staticroute-%s", getRandomString())
	setupTest(t, testNamespace)
	defer teardownTest(t, testNamespace, defaultTimeout)
	ips := createIpAddressAllocation(t, testNamespace, IpAddressAllocationName)
	defer deleteIpAddressAllocation(t, testNamespace, IpAddressAllocationName)

	// SequentialTests: Create and Delete must run in sequence (Delete depends on Create)
	RunSubtest(t, "SequentialTests", func(t *testing.T) {
		RunSubtest(t, "case=CreateStaticRoute", func(t *testing.T) {
			CreateStaticRoute(t, testNamespace, ips)
		})
		RunSubtest(t, "case=DeleteStaticRoute", func(t *testing.T) {
			DeleteStaticRoute(t, testNamespace)
		})
	})
}

func waitForStaticRouteCRReady(t *testing.T, ns, staticRouteName string) (res *v1alpha1.StaticRoute) {
	log.Info("Waiting for StaticRoute CR to be ready", "ns", ns, "staticRouteName", staticRouteName)
	err := wait.PollUntilContextTimeout(context.TODO(), 3*time.Second, 60*time.Second, true, func(ctx context.Context) (done bool, err error) {
		res, err = testData.crdClientset.CrdV1alpha1().StaticRoutes(ns).Get(context.TODO(), staticRouteName, v1.GetOptions{})
		if err != nil {
			log.Error(err, "Error fetching StaticRoute", "namespace", ns, "name", staticRouteName)
			return false, nil
		}
		log.Info("StaticRoute status", "status", res.Status)
		for _, con := range res.Status.Conditions {
			log.Info("Checking condition", "type", con.Type, "status", con.Status)
			if con.Type == v1alpha1.Ready && con.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
	require.NoError(t, err)
	return
}

func createIpAddressAllocation(t *testing.T, ns, ipAllocName string) string {
	ipAlloc := &v1alpha1.IPAddressAllocation{
		Spec: v1alpha1.IPAddressAllocationSpec{
			AllocationSize:           32,
			IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityPrivateTGW,
		}}
	ipAlloc.Name = ipAllocName
	_, err := testData.crdClientset.CrdV1alpha1().IPAddressAllocations(ns).Create(context.TODO(), ipAlloc, v1.CreateOptions{})
	if err != nil && errors.IsAlreadyExists(err) {
		err = nil
	}
	require.NoError(t, err)
	ips := assureIPAddressAllocationReady(t, ns, ipAllocName)
	log.Info("Created IPAddressAllocation", "Namespace", ns, "Name", ipAllocName, "IPs", ips)
	require.NoError(t, err)
	return ips
}

func deleteIpAddressAllocation(t *testing.T, ns, ipAllocName string) {
	log.Info("Deleting IPAddressAllocation", "Namespace", ns, "Name", ipAllocName)
	err := testData.crdClientset.CrdV1alpha1().IPAddressAllocations(ns).Delete(context.TODO(), ipAllocName, v1.DeleteOptions{})
	require.NoError(t, err)
}

func CreateStaticRoute(t *testing.T, ns string, ips string) {
	nextHop := v1alpha1.NextHop{IPAddress: "192.168.0.1"}
	staticRoute := &v1alpha1.StaticRoute{
		Spec: v1alpha1.StaticRouteSpec{
			Network:  ips,
			NextHops: []v1alpha1.NextHop{nextHop},
		}}
	staticRoute.Name = StaticRouteName
	_, err := testData.crdClientset.CrdV1alpha1().StaticRoutes(ns).Create(context.TODO(), staticRoute, v1.CreateOptions{})
	if err != nil && errors.IsAlreadyExists(err) {
		err = nil
	}
	require.NoError(t, err)
	waitForStaticRouteCRReady(t, ns, staticRoute.Name)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeStaticRoutes, staticRoute.Name, true)
	require.NoError(t, err)
}

func DeleteStaticRoute(t *testing.T, ns string) {
	err := testData.crdClientset.CrdV1alpha1().StaticRoutes(ns).Delete(context.TODO(), StaticRouteName, v1.DeleteOptions{})
	require.NoError(t, err)

	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeStaticRoutes, StaticRouteName, false)
	require.NoError(t, err)
}
