package pod

import (
	"context"
	"errors"
	"testing"

	"github.com/agiledragon/gomonkey"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/node"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
)

type fakeRecorder struct {
}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
}
func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}
func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}

func TestPodReconciler_Reconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	r := &PodReconciler{
		Client: k8sClient,
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: &config.NSXOperatorConfig{
					NsxConfig: &config.NsxConfig{
						EnforcementPoint: "vmc-enforcementpoint",
					},
				},
			},
		},
		SubnetService: &subnet.SubnetService{
			SubnetStore: &subnet.SubnetStore{},
		},
		Recorder: fakeRecorder{},
	}
	tests := []struct {
		name           string
		prepareFunc    func(*testing.T, *PodReconciler) *gomonkey.Patches
		expectedErr    string
		expectedResult ctrl.Result
	}{
		{
			name: "CRNotFoundAndDeletionFailed",
			prepareFunc: func(t *testing.T, r *PodReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(apierrors.NewNotFound(v1alpha1.Resource("pod"), ""))
				patchesDeleteSubnetPortByPodName := gomonkey.ApplyFunc((*PodReconciler).deleteSubnetPortByPodName,
					func(r *PodReconciler, ctx context.Context, ns string, name string) error {
						return errors.New("deletion failed")
					})
				return patchesDeleteSubnetPortByPodName
			},
			expectedErr:    "deletion failed",
			expectedResult: common.ResultRequeue,
		},
		{
			name: "CRNotFoundAndDeletionSuccess",
			prepareFunc: func(t *testing.T, r *PodReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(apierrors.NewNotFound(v1alpha1.Resource("pod"), ""))
				patchesDeleteSubnetPortByPodName := gomonkey.ApplyFunc((*PodReconciler).deleteSubnetPortByPodName,
					func(r *PodReconciler, ctx context.Context, ns string, name string) error {
						return nil
					})
				return patchesDeleteSubnetPortByPodName
			},
			expectedResult: common.ResultNormal,
		},
		{
			name: "GetSubnetPathFailure",
			prepareFunc: func(t *testing.T, r *PodReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					podCR := obj.(*v1.Pod)
					podCR.Spec.NodeName = "node-1"
					return nil
				})
				patchesGetSubnetPathForPod := gomonkey.ApplyFunc((*PodReconciler).GetSubnetPathForPod,
					func(r *PodReconciler, ctx context.Context, pod *v1.Pod) (string, error) {
						return "", errors.New("failed to get subnet path")
					})
				return patchesGetSubnetPathForPod
			},
			expectedErr:    "failed to get subnet path",
			expectedResult: common.ResultRequeue,
		},
		{
			name: "PodNotScheduled",
			prepareFunc: func(t *testing.T, r *PodReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				return nil
			},
			expectedResult: common.ResultNormal,
		},
		{
			name: "NodeNotCached",
			prepareFunc: func(t *testing.T, r *PodReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					podCR := obj.(*v1.Pod)
					podCR.Spec.NodeName = "node-1"
					return nil
				})
				patches := gomonkey.ApplyFunc((*PodReconciler).GetSubnetPathForPod,
					func(r *PodReconciler, ctx context.Context, pod *v1.Pod) (string, error) {
						return "subnet-path-1", nil
					})
				patches.ApplyFunc((*PodReconciler).GetNodeByName,
					func(r *PodReconciler, nodeName string) (*model.HostTransportNode, error) {
						return nil, errors.New("failed to get node")
					})
				return patches
			},
			expectedErr:    "failed to get node",
			expectedResult: common.ResultRequeue,
		},
		{
			name: "GetSubnetFailure",
			prepareFunc: func(t *testing.T, r *PodReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					podCR := obj.(*v1.Pod)
					podCR.Spec.NodeName = "node-1"
					return nil
				})
				patches := gomonkey.ApplyFunc((*PodReconciler).GetSubnetPathForPod,
					func(r *PodReconciler, ctx context.Context, pod *v1.Pod) (string, error) {
						return "subnet-path-1", nil
					})
				patches.ApplyFunc((*PodReconciler).GetNodeByName,
					func(r *PodReconciler, nodeName string) (*model.HostTransportNode, error) {
						return &model.HostTransportNode{UniqueId: servicecommon.String("node-1")}, nil
					})
				patches.ApplyFunc((*subnet.SubnetService).GetSubnetByPath,
					func(r *subnet.SubnetService, path string) (*model.VpcSubnet, error) {
						return nil, errors.New("failed to get subnet")
					})
				return patches
			},
			expectedErr:    "failed to get subnet",
			expectedResult: common.ResultRequeue,
		},
		{
			name: "CreateSubnetPortFailure",
			prepareFunc: func(t *testing.T, r *PodReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					podCR := obj.(*v1.Pod)
					podCR.Spec.NodeName = "node-1"
					return nil
				})
				patches := gomonkey.ApplyFunc((*PodReconciler).GetSubnetPathForPod,
					func(r *PodReconciler, ctx context.Context, pod *v1.Pod) (string, error) {
						return "subnet-path-1", nil
					})
				patches.ApplyFunc((*PodReconciler).GetNodeByName,
					func(r *PodReconciler, nodeName string) (*model.HostTransportNode, error) {
						return &model.HostTransportNode{UniqueId: servicecommon.String("node-1")}, nil
					})
				patches.ApplyFunc((*subnet.SubnetService).GetSubnetByPath,
					func(r *subnet.SubnetService, path string) (*model.VpcSubnet, error) {
						return &model.VpcSubnet{}, nil
					})
				patches.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
					func(r *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string) (*model.SegmentPortState, error) {
						return nil, errors.New("failed to create subnetport")
					})
				return patches
			},
			expectedErr:    "failed to create subnetport",
			expectedResult: common.ResultRequeue,
		},
		{
			name: "CreateSubnetPortSuccess",
			prepareFunc: func(t *testing.T, r *PodReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					podCR := obj.(*v1.Pod)
					podCR.Spec.NodeName = "node-1"
					return nil
				})
				patches := gomonkey.ApplyFunc((*PodReconciler).GetSubnetPathForPod,
					func(r *PodReconciler, ctx context.Context, pod *v1.Pod) (string, error) {
						return "subnet-path-1", nil
					})
				patches.ApplyFunc((*PodReconciler).GetNodeByName,
					func(r *PodReconciler, nodeName string) (*model.HostTransportNode, error) {
						return &model.HostTransportNode{UniqueId: servicecommon.String("node-1")}, nil
					})
				patches.ApplyFunc((*subnet.SubnetService).GetSubnetByPath,
					func(s *subnet.SubnetService, path string) (*model.VpcSubnet, error) {
						return &model.VpcSubnet{}, nil
					})
				patches.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
					func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string) (*model.SegmentPortState, error) {
						return &model.SegmentPortState{}, nil
					})
				return patches
			},
			expectedResult: common.ResultNormal,
		},
		{
			name: "PodDeletedSuccess",
			prepareFunc: func(t *testing.T, r *PodReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					podCR := obj.(*v1.Pod)
					podCR.Spec.NodeName = "node-1"
					podCR.ObjectMeta.Name = "pod-1"
					podCR.ObjectMeta.Namespace = "ns-1"
					podCR.Status.Phase = "Failed"
					return nil
				})
				patchesDeleteSubnetPortById := gomonkey.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPortById,
					func(s *subnetport.SubnetPortService, portID string) error {
						return nil
					})
				return patchesDeleteSubnetPortById
			},
			expectedResult: common.ResultNormal,
		},
		{
			name: "PodDeletedFailure",
			prepareFunc: func(t *testing.T, r *PodReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					podCR := obj.(*v1.Pod)
					podCR.Spec.NodeName = "node-1"
					podCR.ObjectMeta.Name = "pod-1"
					podCR.ObjectMeta.Namespace = "ns-1"
					podCR.Status.Phase = "Failed"
					return nil
				})
				patchesDeleteSubnetPortById := gomonkey.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPortById,
					func(s *subnetport.SubnetPortService, portID string) error {
						return errors.New("failed to delete subnetport")
					})
				return patchesDeleteSubnetPortById
			},
			expectedErr:    "failed to delete subnetport",
			expectedResult: common.ResultRequeue,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.prepareFunc(t, r)
			if patches != nil {
				defer patches.Reset()
			}
			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "pod-1", Namespace: "ns-1"}}
			result, err := r.Reconcile(context.TODO(), req)
			if tt.expectedErr != "" {
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				assert.Nil(t, err)
			}
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestPodReconciler_CollectGarbage(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	service := &subnetport.SubnetPortService{
		Service: servicecommon.Service{
			Client: k8sClient,
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
	}
	r := &PodReconciler{
		Client:            k8sClient,
		Scheme:            nil,
		SubnetPortService: service,
	}
	ListNSXSubnetPortIDForPod := gomonkey.ApplyFunc((*subnetport.SubnetPortService).ListNSXSubnetPortIDForPod,
		func(s *subnetport.SubnetPortService) sets.Set[string] {
			a := sets.New[string]()
			a.Insert("uuid-1")
			a.Insert("uuid-2")
			return a
		})
	defer ListNSXSubnetPortIDForPod.Reset()
	patchesDeleteSubnetPortById := gomonkey.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPortById,
		func(s *subnetport.SubnetPortService, uid string) error {
			return nil
		})
	defer patchesDeleteSubnetPortById.Reset()
	podList := &v1.PodList{}
	k8sClient.EXPECT().List(gomock.Any(), podList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1.PodList)
		a.Items = append(a.Items, v1.Pod{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "uuid-1"
		a.Items[0].Name = "pod-1"
		return nil
	})
	r.CollectGarbage(context.TODO())
}

func TestPodReconciler_GetNodeByName(t *testing.T) {
	r := &PodReconciler{
		NodeServiceReader: &node.NodeService{},
	}
	tests := []struct {
		name           string
		nodes          []*model.HostTransportNode
		expectedErr    string
		expectedResult *model.HostTransportNode
	}{
		{
			name:        "NoNode",
			nodes:       []*model.HostTransportNode{},
			expectedErr: "node node-1 not found",
		},
		{
			name: "MultipleNodes",
			nodes: []*model.HostTransportNode{
				{UniqueId: servicecommon.String("id-1")},
				{UniqueId: servicecommon.String("id-2")},
			},
			expectedErr: "multiple node IDs found for node node-1: [id-1 id-2]",
		},
		{
			name: "Success",
			nodes: []*model.HostTransportNode{
				{UniqueId: servicecommon.String("id-1")},
			},
			expectedResult: &model.HostTransportNode{UniqueId: servicecommon.String("id-1")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := gomonkey.ApplyFunc((*node.NodeService).GetNodeByName,
				func(s *node.NodeService, nodeName string) []*model.HostTransportNode {
					return tt.nodes
				})
			defer patches.Reset()
			node, err := r.GetNodeByName("node-1")
			if tt.expectedErr != "" {
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, tt.expectedResult, node)
			}
		})
	}
}

func TestSubnetPortReconciler_GetSubnetPathForPod(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	subnetPath := "subnet-path-1"
	r := &PodReconciler{
		Client:            k8sClient,
		SubnetPortService: &subnetport.SubnetPortService{},
		SubnetService:     &subnet.SubnetService{},
	}

	tests := []struct {
		name               string
		prepareFunc        func(*testing.T, *PodReconciler) *gomonkey.Patches
		expectedErr        string
		expectedSubnetPath string
	}{
		{
			name: "SubnetExisted",
			prepareFunc: func(t *testing.T, pr *PodReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetSubnetPathForSubnetPortFromStore,
					func(s *subnetport.SubnetPortService, nsxSubnetPortID string) string {
						return subnetPath
					})
				return patches
			},
			expectedSubnetPath: subnetPath,
		},
		{
			name: "NoGetDefaultSubnetSet",
			prepareFunc: func(t *testing.T, pr *PodReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetSubnetPathForSubnetPortFromStore,
					func(s *subnetport.SubnetPortService, nsxSubnetPortID string) string {
						return ""
					})
				patches.ApplyFunc(common.GetDefaultSubnetSet,
					func(client client.Client, ctx context.Context, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
						return nil, errors.New("failed to get default SubnetSet")
					})
				return patches
			},
			expectedErr: "failed to get default SubnetSet",
		},
		{
			name: "CreateSubnetFailure",
			prepareFunc: func(t *testing.T, pr *PodReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetSubnetPathForSubnetPortFromStore,
					func(s *subnetport.SubnetPortService, nsxSubnetPortID string) string {
						return ""
					})
				patches.ApplyFunc(common.GetDefaultSubnetSet,
					func(client client.Client, ctx context.Context, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
						return &v1alpha1.SubnetSet{
							ObjectMeta: metav1.ObjectMeta{
								Name: "subnetset-1",
								UID:  "uid-1",
							},
						}, nil
					})
				patches.ApplyFunc(common.AllocateSubnetFromSubnetSet,
					func(subnetSet *v1alpha1.SubnetSet, vpcService servicecommon.VPCServiceProvider, subnetService servicecommon.SubnetServiceProvider, subnetPortService servicecommon.SubnetPortServiceProvider) (string, error) {
						return "", errors.New("failed to create subnet")
					})
				return patches
			},
			expectedErr: "failed to create subnet",
		},
		{
			name: "CreateSubnetSuccess",
			prepareFunc: func(t *testing.T, pr *PodReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetSubnetPathForSubnetPortFromStore,
					func(s *subnetport.SubnetPortService, nsxSubnetPortID string) string {
						return ""
					})
				patches.ApplyFunc(common.GetDefaultSubnetSet,
					func(client client.Client, ctx context.Context, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
						return &v1alpha1.SubnetSet{
							ObjectMeta: metav1.ObjectMeta{
								Name: "subnetset-1",
								UID:  "uid-1",
							},
						}, nil
					})
				patches.ApplyFunc(common.AllocateSubnetFromSubnetSet,
					func(subnetSet *v1alpha1.SubnetSet, vpcService servicecommon.VPCServiceProvider, subnetService servicecommon.SubnetServiceProvider, subnetPortService servicecommon.SubnetPortServiceProvider) (string, error) {
						return subnetPath, nil
					})
				return patches
			},
			expectedSubnetPath: subnetPath,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.prepareFunc(t, r)
			defer patches.Reset()
			path, err := r.GetSubnetPathForPod(context.TODO(), &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "ns-1",
				},
			})
			if tt.expectedErr != "" {
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, subnetPath, path)
			}
		})
	}
}

func TestSubnetPortReconciler_deleteSubnetPortByPodName(t *testing.T) {
	subnetportId1 := "subnetport-1"
	subnetportId2 := "subnetport-2"
	podName := "pod-1"
	namespaceScope := "nsx-op/namespace"
	ns := "ns"
	nameScope := "nsx-op/pod_name"
	sp1 := &model.VpcSubnetPort{
		Id: &subnetportId1,
		Tags: []model.Tag{
			{
				Scope: &namespaceScope,
				Tag:   &ns,
			},
			{
				Scope: &nameScope,
				Tag:   &podName,
			},
		},
	}
	sp2 := &model.VpcSubnetPort{
		Id: &subnetportId2,
		Tags: []model.Tag{
			{
				Scope: &namespaceScope,
				Tag:   &ns,
			},
			{
				Scope: &nameScope,
				Tag:   &podName,
			},
		},
	}
	r := &PodReconciler{
		SubnetPortService: &subnetport.SubnetPortService{},
	}
	patchesListSubnetPortIDsFromCRs := gomonkey.ApplyFunc((*subnetport.SubnetPortService).ListSubnetPortIDsFromCRs,
		func(s *subnetport.SubnetPortService, _ context.Context) (sets.Set[string], error) {
			crSubnetPortIDsSet := sets.New[string]()
			crSubnetPortIDsSet.Insert("subnetport-1")
			return crSubnetPortIDsSet, nil
		})
	defer patchesListSubnetPortIDsFromCRs.Reset()
	patchesGetByIndex := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetByIndex,
		func(s *subnetport.SubnetPortStore, key string, value string) []*model.VpcSubnetPort {
			subnetPorts := make([]*model.VpcSubnetPort, 0)
			subnetPorts = append(subnetPorts, sp1, sp2)
			return subnetPorts
		})
	defer patchesGetByIndex.Reset()
	patchesDeleteSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPort,
		func(s *subnetport.SubnetPortService, sp *model.VpcSubnetPort) error {
			assert.Equal(t, sp2, sp)
			return nil
		})
	defer patchesDeleteSubnetPort.Reset()
	err := r.deleteSubnetPortByPodName(context.TODO(), ns, podName)
	assert.Nil(t, err)
}
