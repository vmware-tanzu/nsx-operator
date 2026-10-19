package vpc

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"syscall"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func newLBProviderTestService(t *testing.T, objects ...client.Object) *VPCService {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	return &VPCService{
		Service: common.Service{
			Client: fake.NewClientBuilder().WithScheme(scheme).
				WithStatusSubresource(&v1alpha1.VPCNetworkConfiguration{}).WithObjects(objects...).Build(),
			NSXConfig: &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{UseAVILoadBalancer: true}},
			NSXClient: &nsx.Client{Cluster: &nsx.Cluster{}},
		},
		LbsStore: &LBSStore{ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(keyFunc, cache.Indexers{}),
		}},
	}
}

func systemNetworkConfig(annotations map[string]string) *v1alpha1.VPCNetworkConfiguration {
	return &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{
		Name: common.SystemVPCNetworkConfigurationName, Annotations: annotations,
	}}
}

func TestGetLBProviderFromAnnotation(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  LBProvider
	}{
		{value: string(AVILB), want: AVILB},
		{value: string(NSXLB), want: NSXLB},
		{value: "", want: NoneLB},
		{value: string(NoneLB), want: NoneLB},
		{value: "unknown", want: NoneLB},
	} {
		t.Run(tc.value, func(t *testing.T) {
			s := newLBProviderTestService(t, systemNetworkConfig(map[string]string{common.AnnotationLBProvider: tc.value}))
			// No NSX/config/store dependency is available: a persisted decision must bypass all discovery.
			s.NSXClient, s.NSXConfig, s.LbsStore = nil, nil, nil
			for range 2 {
				provider, err := s.GetLBProvider()
				if tc.want == NoneLB {
					require.ErrorContains(t, err, "invalid persisted LB provider")
				} else {
					require.NoError(t, err)
				}
				assert.Equal(t, tc.want, provider)
			}
		})
	}
}

func TestGetLBProviderPersistsDecision(t *testing.T) {
	for _, tc := range []struct {
		name     string
		useAVI   bool
		edge     bool
		probeErr error
		want     LBProvider
	}{
		{name: "AVI", useAVI: true, edge: true, want: AVILB},
		{name: "NSX LB with AVI disabled", edge: true, want: NSXLB},
		{name: "NSX LB without ALB endpoint", useAVI: true, edge: true, probeErr: nsxutil.HttpNotFoundError, want: NSXLB},
		{name: "AVI without service gateway", useAVI: true, want: AVILB},
		{name: "no provider yet", want: NoneLB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nc := systemNetworkConfig(map[string]string{"other": "keep"})
			nc.Labels = map[string]string{"label": "keep"}
			nc.Spec.NSXProject = "/orgs/default/projects/project"
			nc.Status.VPCs = []v1alpha1.VPCInfo{{Name: "existing", AVISESubnetPath: "/existing/subnet"}}
			s := newLBProviderTestService(t, nc)
			s.NSXConfig.UseAVILoadBalancer = tc.useAVI
			patches := gomonkey.ApplyMethod(reflect.TypeOf(s), "GetVpcConnectivityProfileWithRetry",
				func(_ *VPCService, _ *v1alpha1.VPCNetworkConfiguration) (*model.VpcConnectivityProfile, error) {
					return &model.VpcConnectivityProfile{ServiceGateway: &model.VpcServiceGatewayConfig{Enable: &tc.edge}}, nil
				})
			defer patches.Reset()
			patches.ApplyMethod(reflect.TypeOf(s.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, _ string) (map[string]interface{}, error) {
				return nil, tc.probeErr
			})
			patches.ApplyMethod(reflect.TypeOf(s.NSXClient.Cluster), "Health", func(_ *nsx.Cluster) nsx.ClusterHealth { return nsx.GREEN })

			provider, err := s.GetLBProvider()
			require.NoError(t, err)
			assert.Equal(t, tc.want, provider)
			stored := &v1alpha1.VPCNetworkConfiguration{}
			require.NoError(t, s.Client.Get(context.Background(), client.ObjectKeyFromObject(nc), stored))
			assert.Equal(t, "keep", stored.Annotations["other"])
			assert.Equal(t, nc.Labels, stored.Labels)
			assert.Equal(t, nc.Spec, stored.Spec)
			assert.Equal(t, nc.Status, stored.Status)
			if tc.want == NoneLB {
				assert.NotContains(t, stored.Annotations, common.AnnotationLBProvider)
				return
			}
			assert.Equal(t, string(tc.want), stored.Annotations[common.AnnotationLBProvider])

			// A new service (operator restart) must retain the decision without reaching NSX.
			restarted := &VPCService{Service: common.Service{Client: s.Client}}
			provider, err = restarted.GetLBProvider()
			require.NoError(t, err)
			assert.Equal(t, tc.want, provider)

			// Authorized changes must also be seen by the original process, without a restart.
			otherProvider := AVILB
			if tc.want == AVILB {
				otherProvider = NSXLB
			}
			stored.Annotations[common.AnnotationLBProvider] = string(otherProvider)
			require.NoError(t, s.Client.Update(context.Background(), stored))
			provider, err = s.GetLBProvider()
			require.NoError(t, err)
			assert.Equal(t, otherProvider, provider)
		})
	}
}

func TestGetLBProviderWithoutSystemConfig(t *testing.T) {
	s := newLBProviderTestService(t)
	provider, err := s.GetLBProvider()
	require.NoError(t, err)
	assert.Equal(t, NoneLB, provider)
}

func TestGetLBProviderReadAndWriteFailures(t *testing.T) {
	for _, op := range []string{"read", "write", "profile"} {
		t.Run(op, func(t *testing.T) {
			s := newLBProviderTestService(t, systemNetworkConfig(nil))
			injectedErr := errors.New("temporary failure")
			failing := true
			s.Client = interceptor.NewClient(s.Client.(client.WithWatch), interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if failing && op == "read" {
						return injectedErr
					}
					return c.Get(ctx, key, obj, opts...)
				},
				Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					if failing && op == "write" {
						return injectedErr
					}
					return c.Update(ctx, obj, opts...)
				},
			})
			patches := gomonkey.ApplyMethod(reflect.TypeOf(s), "GetVpcConnectivityProfileWithRetry",
				func(_ *VPCService, _ *v1alpha1.VPCNetworkConfiguration) (*model.VpcConnectivityProfile, error) {
					if failing && op == "profile" {
						return nil, injectedErr
					}
					return &model.VpcConnectivityProfile{}, nil
				})
			defer patches.Reset()
			patches.ApplyMethod(reflect.TypeOf(s.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, _ string) (map[string]interface{}, error) { return nil, nil })

			provider, err := s.GetLBProvider()
			require.ErrorIs(t, err, injectedErr)
			assert.Equal(t, NoneLB, provider)
			failing = false
			stored := &v1alpha1.VPCNetworkConfiguration{}
			require.NoError(t, s.Client.Get(context.Background(), client.ObjectKey{Name: "system"}, stored))
			assert.NotContains(t, stored.Annotations, common.AnnotationLBProvider)
			provider, err = s.GetLBProvider()
			require.NoError(t, err)
			assert.Equal(t, AVILB, provider)
			require.NoError(t, s.Client.Get(context.Background(), client.ObjectKey{Name: "system"}, stored))
			assert.Equal(t, string(AVILB), stored.Annotations[common.AnnotationLBProvider])
		})
	}
}

func TestGetLBProviderConcurrentDecision(t *testing.T) {
	s := newLBProviderTestService(t, systemNetworkConfig(nil))
	// Another leader chooses AVI between our discovery and our update. Our NSX LB
	// choice must fail, then the next reconcile must read the existing AVI choice.
	s.Client = interceptor.NewClient(s.Client.(client.WithWatch), interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			winner := &v1alpha1.VPCNetworkConfiguration{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(obj), winner); err != nil {
				return err
			}
			winner.Annotations = map[string]string{common.AnnotationLBProvider: string(AVILB)}
			if err := c.Update(ctx, winner); err != nil {
				return err
			}
			return c.Update(ctx, obj, opts...)
		},
	})
	patches := gomonkey.ApplyMethod(reflect.TypeOf(s), "GetVpcConnectivityProfileWithRetry",
		func(_ *VPCService, _ *v1alpha1.VPCNetworkConfiguration) (*model.VpcConnectivityProfile, error) {
			return &model.VpcConnectivityProfile{ServiceGateway: &model.VpcServiceGatewayConfig{Enable: common.Bool(true)}}, nil
		})
	defer patches.Reset()
	s.NSXConfig.UseAVILoadBalancer = false
	provider, err := s.GetLBProvider()
	require.True(t, apierrors.IsConflict(err), "expected conflict, got %v", err)
	assert.Equal(t, NoneLB, provider)
	s.NSXConfig, s.NSXClient, s.LbsStore = nil, nil, nil
	provider, err = s.GetLBProvider()
	require.NoError(t, err)
	assert.Equal(t, AVILB, provider)
}

func TestGetLBProviderRetriesALBProbeAndPersistsAVI(t *testing.T) {
	for _, tc := range []struct {
		name     string
		probeErr error
		health   nsx.ClusterHealth
	}{
		{name: "HTTP common error", probeErr: nsxutil.HttpCommonError, health: nsx.GREEN},
		{name: "TCP connection refused", probeErr: &url.Error{Op: "Get", URL: albEndpointPath, Err: syscall.ECONNREFUSED}, health: nsx.GREEN},
		{name: "connection timeout", probeErr: &url.Error{Op: "Get", URL: albEndpointPath, Err: context.DeadlineExceeded}, health: nsx.GREEN},
		{name: "TLS failure", probeErr: errors.New("TLS handshake failed"), health: nsx.GREEN},
		{name: "404 while cluster is down", probeErr: nsxutil.HttpNotFoundError, health: nsx.RED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newLBProviderTestService(t, systemNetworkConfig(nil))
			patches := gomonkey.ApplyMethod(reflect.TypeOf(s), "GetVpcConnectivityProfileWithRetry",
				func(_ *VPCService, _ *v1alpha1.VPCNetworkConfiguration) (*model.VpcConnectivityProfile, error) {
					return &model.VpcConnectivityProfile{ServiceGateway: &model.VpcServiceGatewayConfig{Enable: common.Bool(true)}}, nil
				})
			defer patches.Reset()
			patches.ApplyMethod(reflect.TypeOf(s.NSXClient.Cluster), "Health", func(_ *nsx.Cluster) nsx.ClusterHealth { return tc.health })
			probeErr := tc.probeErr
			probeCalls := 0
			patches.ApplyMethod(reflect.TypeOf(s.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, path string) (map[string]interface{}, error) {
				assert.Equal(t, albEndpointPath, path)
				probeCalls++
				return nil, probeErr
			})
			provider, err := s.GetLBProvider()
			require.Error(t, err)
			assert.Equal(t, NoneLB, provider)
			stored := &v1alpha1.VPCNetworkConfiguration{}
			require.NoError(t, s.Client.Get(context.Background(), client.ObjectKey{Name: "system"}, stored))
			assert.NotContains(t, stored.Annotations, common.AnnotationLBProvider)
			assert.Equal(t, 1, probeCalls)

			probeErr = nil
			provider, err = s.GetLBProvider()
			require.NoError(t, err)
			assert.Equal(t, AVILB, provider)
			assert.Equal(t, 2, probeCalls)
			require.NoError(t, s.Client.Get(context.Background(), client.ObjectKey{Name: "system"}, stored))
			assert.Equal(t, string(AVILB), stored.Annotations[common.AnnotationLBProvider])

			// All later failures, including a healthy-cluster 404, must not cause re-detection.
			for _, laterErr := range []error{tc.probeErr, nsxutil.HttpNotFoundError} {
				probeErr = laterErr
				provider, err = s.GetLBProvider()
				require.NoError(t, err)
				assert.Equal(t, AVILB, provider)
				assert.Equal(t, 2, probeCalls)
			}
		})
	}
}

func TestGetLBProviderUpdateErrorsRemainRetryable(t *testing.T) {
	// Both Kubernetes conflict and authorization failures must be returned as errors.
	for _, err := range []error{
		apierrors.NewConflict(schema.GroupResource{Group: "crd.nsx.vmware.com", Resource: "vpcnetworkconfigurations"}, "system", errors.New("conflict")),
		apierrors.NewForbidden(schema.GroupResource{Group: "crd.nsx.vmware.com", Resource: "vpcnetworkconfigurations"}, "system", errors.New("denied")),
	} {
		t.Run(err.Error(), func(t *testing.T) {
			s := newLBProviderTestService(t, systemNetworkConfig(nil))
			s.Client = interceptor.NewClient(s.Client.(client.WithWatch), interceptor.Funcs{
				Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error { return err },
			})
			patches := gomonkey.ApplyMethod(reflect.TypeOf(s), "GetVpcConnectivityProfileWithRetry",
				func(_ *VPCService, _ *v1alpha1.VPCNetworkConfiguration) (*model.VpcConnectivityProfile, error) {
					return &model.VpcConnectivityProfile{}, nil
				})
			defer patches.Reset()
			patches.ApplyMethod(reflect.TypeOf(s.NSXClient.Cluster), "HttpGet", func(_ *nsx.Cluster, _ string) (map[string]interface{}, error) { return nil, nil })
			provider, gotErr := s.GetLBProvider()
			require.ErrorIs(t, gotErr, err)
			assert.Equal(t, NoneLB, provider)
		})
	}
}
