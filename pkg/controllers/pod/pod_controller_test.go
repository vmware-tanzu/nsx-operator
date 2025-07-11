package pod

import (
	"context"
	"errors"
	"testing"
	"time"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/node"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

type fakeRecorder struct{}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
}

func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}

func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}

type fakeStatusWriter struct{}

func (writer fakeStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return nil
}

func (writer fakeStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return nil
}

func (writer fakeStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return nil
}

func TestPodReconciler_Reconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	fakewriter := fakeStatusWriter{}
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
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
		SubnetService: &subnet.SubnetService{
			SubnetStore: &subnet.SubnetStore{},
		},
		Recorder: fakeRecorder{},
	}
	r.StatusUpdater = common.NewStatusUpdater(k8sClient, r.SubnetPortService.NSXConfig, r.Recorder, MetricResTypePod, "SubnetPort", "Pod")
	tests := []struct {
		name           string
		prepareFunc    func(*testing.T, *PodReconciler) *gomonkey.Patches
		expectedErr    string
		expectedResult ctrl.Result
		restoreMode    bool
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
					func(r *PodReconciler, ctx context.Context, pod *v1.Pod) (bool, string, error) {
						return false, "", errors.New("failed to get subnet path")
					})

				k8sClient.EXPECT().Status().Return(fakewriter).AnyTimes()
				patchesGetSubnetPathForPod.ApplyFunc(fakeStatusWriter.Update,
					func(writer fakeStatusWriter, ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						pod := obj.(*v1.Pod)
						assert.Equal(t, "error occurred while processing the Pod. Error: failed to get subnet path", pod.Status.Conditions[0].Message)
						assert.Equal(t, "PodNotReady", pod.Status.Conditions[0].Reason)
						assert.Equal(t, v1.ConditionFalse, pod.Status.Conditions[0].Status)
						assert.Equal(t, v1.PodReady, pod.Status.Conditions[0].Type)
						return nil
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
					func(r *PodReconciler, ctx context.Context, pod *v1.Pod) (bool, string, error) {
						return false, "subnet-path-1", nil
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
					func(r *PodReconciler, ctx context.Context, pod *v1.Pod) (bool, string, error) {
						return false, "subnet-path-1", nil
					})
				patches.ApplyFunc((*PodReconciler).GetNodeByName,
					func(r *PodReconciler, nodeName string) (*model.HostTransportNode, error) {
						return &model.HostTransportNode{UniqueId: servicecommon.String("node-1")}, nil
					})
				patches.ApplyFunc((*subnet.SubnetService).GetSubnetByPath,
					func(r *subnet.SubnetService, path string, sharedSubnet bool) (*model.VpcSubnet, error) {
						return nil, errors.New("failed to get subnet")
					})

				k8sClient.EXPECT().Status().Return(fakewriter).AnyTimes()
				patches.ApplyFunc(fakeStatusWriter.Update,
					func(writer fakeStatusWriter, ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						pod := obj.(*v1.Pod)
						assert.Equal(t, "error occurred while processing the Pod. Error: failed to get subnet", pod.Status.Conditions[0].Message)
						assert.Equal(t, "PodNotReady", pod.Status.Conditions[0].Reason)
						assert.Equal(t, v1.ConditionFalse, pod.Status.Conditions[0].Status)
						assert.Equal(t, v1.PodReady, pod.Status.Conditions[0].Type)
						return nil
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
					func(r *PodReconciler, ctx context.Context, pod *v1.Pod) (bool, string, error) {
						return false, "subnet-path-1", nil
					})
				patches.ApplyFunc((*PodReconciler).GetNodeByName,
					func(r *PodReconciler, nodeName string) (*model.HostTransportNode, error) {
						return &model.HostTransportNode{UniqueId: servicecommon.String("node-1")}, nil
					})
				patches.ApplyFunc((*subnet.SubnetService).GetSubnetByPath,
					func(r *subnet.SubnetService, path string, sharedSubnet bool) (*model.VpcSubnet, error) {
						return &model.VpcSubnet{}, nil
					})
				patches.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
					func(r *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string, isVmSubnetPort bool, restoreMode bool) (*model.SegmentPortState, bool, error) {
						return nil, false, errors.New("failed to create subnetport")
					})

				k8sClient.EXPECT().Status().Return(fakewriter).AnyTimes()
				patches.ApplyFunc(fakeStatusWriter.Update,
					func(writer fakeStatusWriter, ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						pod := obj.(*v1.Pod)
						assert.Equal(t, "error occurred while processing the Pod. Error: failed to create subnetport", pod.Status.Conditions[0].Message)
						assert.Equal(t, "PodNotReady", pod.Status.Conditions[0].Reason)
						assert.Equal(t, v1.ConditionFalse, pod.Status.Conditions[0].Status)
						assert.Equal(t, v1.PodReady, pod.Status.Conditions[0].Type)
						return nil
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
					func(r *PodReconciler, ctx context.Context, pod *v1.Pod) (bool, string, error) {
						return false, "subnet-path-1", nil
					})
				patches.ApplyFunc((*PodReconciler).GetNodeByName,
					func(r *PodReconciler, nodeName string) (*model.HostTransportNode, error) {
						return &model.HostTransportNode{UniqueId: servicecommon.String("node-1")}, nil
					})
				patches.ApplyFunc((*subnet.SubnetService).GetSubnetByPath,
					func(s *subnet.SubnetService, path string, sharedSubnet bool) (*model.VpcSubnet, error) {
						return &model.VpcSubnet{}, nil
					})
				patches.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
					func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string) (*model.SegmentPortState, bool, error) {
						return &model.SegmentPortState{
							RealizedBindings: []model.AddressBindingEntry{
								{
									Binding: &model.PacketAddressClassifier{
										MacAddress: servicecommon.String("aa:bb:cc:dd:ee:ff"),
									},
								},
							},
						}, false, nil
					})

				k8sClient.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).Do(func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
					pod := obj.(*v1.Pod)
					assert.Equal(t, "aa:bb:cc:dd:ee:ff", pod.GetAnnotations()[servicecommon.AnnotationPodMAC])
					return nil
				})
				k8sClient.EXPECT().Status().Return(fakewriter).AnyTimes()
				patches.ApplyFunc(fakeStatusWriter.Update,
					func(writer fakeStatusWriter, ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						pod := obj.(*v1.Pod)
						assert.Equal(t, "Pod has been successfully created/updated", pod.Status.Conditions[0].Message)
						assert.Equal(t, "PodReady", pod.Status.Conditions[0].Reason)
						assert.Equal(t, v1.ConditionTrue, pod.Status.Conditions[0].Status)
						assert.Equal(t, v1.PodReady, pod.Status.Conditions[0].Type)
						return nil
					})
				patches.ApplyFunc(common.UpdateRestoreAnnotation, func(client client.Client, ctx context.Context, obj client.Object, value string) error {
					assert.Equal(t, "true", value)
					return nil
				})
				return patches
			},
			expectedResult: common.ResultNormal,
			restoreMode:    true,
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
				patchesDeleteSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPort,
					func(s *subnetport.SubnetPortService, port *model.VpcSubnetPort) error {
						return nil
					})
				patchesDeleteSubnetPort.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
					func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
						return &model.VpcSubnetPort{Id: servicecommon.String("port1")}, nil
					})
				return patchesDeleteSubnetPort
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
				patchesDeleteSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPort,
					func(s *subnetport.SubnetPortService, port *model.VpcSubnetPort) error {
						return errors.New("failed to delete subnetport")
					})
				patchesDeleteSubnetPort.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
					func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
						return &model.VpcSubnetPort{Id: servicecommon.String("port1")}, nil
					})
				return patchesDeleteSubnetPort
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
			r.restoreMode = tt.restoreMode
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

func TestPodReconciler_getSubnetByPod(t *testing.T) {
	r := &PodReconciler{
		SubnetService: &subnet.SubnetService{},
	}

	patches := gomonkey.ApplyFunc((*subnet.SubnetService).GetSubnetsByIndex, func(s *subnet.SubnetService, key string, value string) []*model.VpcSubnet {
		assert.Equal(t, value, "subnetset-1")
		return []*model.VpcSubnet{
			{
				Path:        servicecommon.String("/subnet-1"),
				IpAddresses: []string{"10.0.0.0/28"},
			},
			{
				Path:        servicecommon.String("/subnet-2"),
				IpAddresses: []string{"10.0.0.16/28"},
			},
			{
				Path:        servicecommon.String("/subnet-3"),
				IpAddresses: []string{"10.0.0.32/28"},
			},
		}
	})
	defer patches.Reset()

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "Pod-1",
			Namespace: "ns-1",
		},
		Status: v1.PodStatus{
			PodIP: "10.0.0.20",
		},
	}
	subnetPath, err := r.getSubnetByPod(pod, "subnetset-1")
	assert.Nil(t, err)
	assert.Equal(t, "/subnet-2", subnetPath)
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
	r.StatusUpdater = common.NewStatusUpdater(k8sClient, r.SubnetPortService.NSXConfig, r.Recorder, MetricResTypePod, "SubnetPort", "Pod")
	ListNSXSubnetPortIDForPod := gomonkey.ApplyFunc((*subnetport.SubnetPortService).ListNSXSubnetPortIDForPod,
		func(s *subnetport.SubnetPortService) sets.Set[string] {
			a := sets.New[string]()
			a.Insert("uuid-1")
			a.Insert("uuid-2")
			return a
		})
	defer ListNSXSubnetPortIDForPod.Reset()
	patchesGetVpsSubnetPortByUID := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
		func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
			return &model.VpcSubnetPort{Id: servicecommon.String("port1")}, nil
		})
	defer patchesGetVpsSubnetPortByUID.Reset()
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

func TestPodReconciler_GetSubnetPathForPod(t *testing.T) {
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
		expectedIsExisting bool
		restoreMode        bool
	}{
		{
			name: "SubnetExisted",
			prepareFunc: func(t *testing.T, pr *PodReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetSubnetPathForSubnetPortFromStore,
					func(s *subnetport.SubnetPortService, uid types.UID) string {
						return subnetPath
					})
				return patches
			},
			expectedSubnetPath: subnetPath,
			expectedIsExisting: true,
		},
		{
			name: "NoGetDefaultSubnetSet",
			prepareFunc: func(t *testing.T, pr *PodReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetSubnetPathForSubnetPortFromStore,
					func(s *subnetport.SubnetPortService, uid types.UID) string {
						return ""
					})
				patches.ApplyFunc(common.GetDefaultSubnetSetByNamespace,
					func(client client.Client, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
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
					func(s *subnetport.SubnetPortService, uid types.UID) string {
						return ""
					})
				patches.ApplyFunc(common.GetDefaultSubnetSetByNamespace,
					func(client client.Client, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
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
					func(s *subnetport.SubnetPortService, uid types.UID) string {
						return ""
					})
				patches.ApplyFunc(common.GetDefaultSubnetSetByNamespace,
					func(client client.Client, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
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
		{
			name: "Restore",
			prepareFunc: func(t *testing.T, pr *PodReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetSubnetPathForSubnetPortFromStore,
					func(s *subnetport.SubnetPortService, uid types.UID) string {
						return ""
					})
				patches.ApplyFunc(common.GetDefaultSubnetSetByNamespace,
					func(client client.Client, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
						return &v1alpha1.SubnetSet{
							ObjectMeta: metav1.ObjectMeta{
								Name: "subnetset-1",
								UID:  "uid-1",
							},
						}, nil
					})
				patches.ApplyFunc((*PodReconciler).getSubnetByPod, func(r *PodReconciler, pod *v1.Pod, subnetSetUID string) (string, error) {
					assert.Equal(t, "uid-1", subnetSetUID)
					return subnetPath, nil
				})
				return patches
			},
			expectedSubnetPath: subnetPath,
			expectedIsExisting: true,
			restoreMode:        true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.prepareFunc(t, r)
			defer patches.Reset()
			r.restoreMode = tt.restoreMode
			isExisting, path, err := r.GetSubnetPathForPod(context.TODO(), &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "ns-1",
				},
				Status: v1.PodStatus{
					PodIP: "10.0.0.1",
				},
			})
			if tt.expectedErr != "" {
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, subnetPath, path)
				assert.Equal(t, tt.expectedIsExisting, isExisting)
			}
		})
	}
}

func TestPodReconciler_deleteSubnetPortByPodName(t *testing.T) {
	subnetportId1 := "subnetport-1"
	subnetportId2 := "subnetport-2"
	podName1 := "pod-1"
	podName2 := "pod-2"
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
				Tag:   &podName1,
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
				Tag:   &podName2,
			},
		},
	}
	r := &PodReconciler{
		SubnetPortService: &subnetport.SubnetPortService{},
	}
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
	err := r.deleteSubnetPortByPodName(context.TODO(), ns, podName2)
	assert.Nil(t, err)
}

func TestPodReconciler_StartController(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects().Build()
	vpcService := &vpc.VPCService{
		Service: servicecommon.Service{
			Client: fakeClient,
		},
	}
	subnetService := &subnet.SubnetService{
		Service: servicecommon.Service{
			Client: fakeClient,
		},
	}
	subnetPortService := &subnetport.SubnetPortService{
		Service: servicecommon.Service{},
	}
	nodeService := &node.NodeService{
		Service: servicecommon.Service{
			Client: fakeClient,
		},
	}
	mockMgr := &MockManager{scheme: runtime.NewScheme()}
	patches := gomonkey.ApplyFunc((*PodReconciler).setupWithManager, func(r *PodReconciler, mgr manager.Manager) error {
		return nil
	})
	patches.ApplyFunc(common.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context) error) {
		return
	})
	defer patches.Reset()
	r := NewPodReconciler(mockMgr, subnetPortService, subnetService, vpcService, nodeService)
	err := r.StartController(mockMgr, nil)
	assert.Nil(t, err)
}

func TestPodReconciler_RestoreReconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	r := &PodReconciler{
		Client: k8sClient,
		SubnetPortService: &subnetport.SubnetPortService{
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}
	patches := gomonkey.ApplyFunc((*servicecommon.ResourceStore).ListIndexFuncValues, func(s *servicecommon.ResourceStore, key string) sets.Set[string] {
		return sets.New[string]("pod-1", "pod-3")
	})
	defer patches.Reset()

	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		podList := list.(*v1.PodList)
		podList.Items = []v1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pod-1",
					Namespace:   "ns-1",
					UID:         "pod-1",
					Annotations: map[string]string{servicecommon.AnnotationPodMAC: "aa:bb:cc:dd:ee:01"},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pod-2",
					Namespace:   "ns-1",
					UID:         "pod-2",
					Annotations: map[string]string{servicecommon.AnnotationPodMAC: "aa:bb:cc:dd:ee:02"},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "pod-3",
					Namespace:   "ns-1",
					UID:         "pod-3",
					Annotations: map[string]string{servicecommon.AnnotationPodMAC: "aa:bb:cc:dd:ee:03"},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-4", Namespace: "ns-1", UID: "pod-4"},
			},
		}
		return nil
	})

	patches.ApplyFunc((*PodReconciler).Reconcile, func(r *PodReconciler, ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
		assert.Equal(t, "pod-2", req.Name)
		assert.Equal(t, "ns-1", req.Namespace)
		return common.ResultNormal, nil
	})
	err := r.RestoreReconcile()
	assert.Nil(t, err)
}

type MockManager struct {
	ctrl.Manager
	client client.Client
	scheme *runtime.Scheme
}

func (m *MockManager) GetClient() client.Client {
	return m.client
}

func (m *MockManager) GetScheme() *runtime.Scheme {
	return m.scheme
}

func (m *MockManager) GetEventRecorderFor(name string) record.EventRecorder {
	return nil
}

func (m *MockManager) Add(runnable manager.Runnable) error {
	return nil
}

func (m *MockManager) Start(context.Context) error {
	return nil
}
