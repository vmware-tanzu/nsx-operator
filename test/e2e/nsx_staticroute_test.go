package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type StaticRouteSuite struct {
	suite.Suite
	TestNamespace   string
	StaticRouteName string
}

func (s *StaticRouteSuite) SetupSuite() {
	// Initialize TestNamespace and StaticRouteName
	s.TestNamespace = fmt.Sprintf("staticroute-%s", getRandomString())
	s.StaticRouteName = "guestcluster-staticroute-2"
	// Call setupTest with s.T() and TestNamespace
	setupTest(s.T(), s.TestNamespace)
}

func (s *StaticRouteSuite) TearDownSuite() {
	// Call teardownTest with s.T(), TestNamespace, and defaultTimeout
	teardownTest(s.T(), s.TestNamespace, defaultTimeout)
}

func (s *StaticRouteSuite) TestCreateStaticRoute() {
	// Mark as parallel to allow suite to run concurrently with other suites
	s.T().Parallel()

	nextHop := v1alpha1.NextHop{IPAddress: "192.168.0.1"}
	staticRoute := &v1alpha1.StaticRoute{
		Spec: v1alpha1.StaticRouteSpec{
			Network:  "45.1.2.0/24",
			NextHops: []v1alpha1.NextHop{nextHop},
		},
	}
	staticRoute.Name = s.StaticRouteName

	_, err := testData.crdClientset.CrdV1alpha1().StaticRoutes(s.TestNamespace).Create(context.TODO(), staticRoute, v1.CreateOptions{})
	if err != nil && errors.IsAlreadyExists(err) {
		err = nil
	}
	s.NoError(err)

	s.waitForStaticRouteCRReady(s.TestNamespace, staticRoute.Name)

	err = testData.waitForResourceExistOrNot(s.TestNamespace, common.ResourceTypeStaticRoutes, staticRoute.Name, true)
	s.NoError(err)
}

func (s *StaticRouteSuite) TestDeleteStaticRoute() {
	// Runs sequentially after TestCreateStaticRoute
	err := testData.crdClientset.CrdV1alpha1().StaticRoutes(s.TestNamespace).Delete(context.TODO(), s.StaticRouteName, v1.DeleteOptions{})
	s.NoError(err)

	err = testData.waitForResourceExistOrNot(s.TestNamespace, common.ResourceTypeStaticRoutes, s.StaticRouteName, false)
	s.NoError(err)
}

func (s *StaticRouteSuite) waitForStaticRouteCRReady(ns, staticRouteName string) *v1alpha1.StaticRoute {
	s.T().Logf("Waiting for StaticRoute CR to be ready, ns: %s, staticRouteName: %s", ns, staticRouteName)

	var res *v1alpha1.StaticRoute
	err := wait.PollUntilContextTimeout(context.TODO(), 3*time.Second, 60*time.Second, true, func(ctx context.Context) (done bool, err error) {
		res, err = testData.crdClientset.CrdV1alpha1().StaticRoutes(ns).Get(context.TODO(), staticRouteName, v1.GetOptions{})
		if err != nil {
			s.T().Logf("Error fetching StaticRoute, namespace: %s, name: %s, err: %v", ns, staticRouteName, err)
			return false, nil
		}

		s.T().Logf("StaticRoute status: %v", res.Status)
		for _, con := range res.Status.Conditions {
			s.T().Logf("Checking condition, type: %s, status: %s", con.Type, con.Status)
			if con.Type == v1alpha1.Ready && con.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})

	s.NoError(err)
	return res
}

func TestStaticRouteSuite(t *testing.T) {
	suite.Run(t, new(StaticRouteSuite))
}
