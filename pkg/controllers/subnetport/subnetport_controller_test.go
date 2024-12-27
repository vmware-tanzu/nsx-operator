package subnetport

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	vmv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
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

type fakeStatusWriter struct {
	t           *testing.T
	validateObj bool
	expectObj   client.Object
}

func (writer fakeStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return nil
}
func (writer fakeStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if writer.validateObj {
		assert.Equal(writer.t, writer.expectObj, obj)
	}
	return nil
}
func (writer fakeStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return nil
}

func TestSubnetPortReconciler_Reconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	fakewriter := fakeStatusWriter{}
	defer mockCtl.Finish()
	service := &subnetport.SubnetPortService{
		Service: servicecommon.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
		SubnetPortStore: &subnetport.SubnetPortStore{},
	}
	subnetService := &subnet.SubnetService{
		Service: servicecommon.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
	}
	r := &SubnetPortReconciler{
		Client:            k8sClient,
		Scheme:            nil,
		SubnetPortService: service,
		SubnetService:     subnetService,
		Recorder:          fakeRecorder{},
	}
	r.StatusUpdater = common.NewStatusUpdater(k8sClient, service.NSXConfig, r.Recorder, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")
	ctx := context.Background()
	req := controllerruntime.Request{NamespacedName: types.NamespacedName{Namespace: "dummy", Name: "dummy"}}
	patchesGetSubnetByPath := gomonkey.ApplyFunc((*subnet.SubnetService).GetSubnetByPath,
		func(s *subnet.SubnetService, nsxSubnetPath string) (*model.VpcSubnet, error) {
			nsxSubnet := &model.VpcSubnet{}
			return nsxSubnet, nil
		})
	defer patchesGetSubnetByPath.Reset()

	// fail to get
	errFailToGet := errors.New("failed to get CR")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errFailToGet)
	_, ret := r.Reconcile(ctx, req)
	assert.Equal(t, errFailToGet, ret)

	// not found and deletion success
	errNotFound := apierrors.NewNotFound(v1alpha1.Resource("subnetport"), "")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errNotFound)

	patchesDeleteSubnetPortByName := gomonkey.ApplyFunc((*SubnetPortReconciler).deleteSubnetPortByName,
		func(r *SubnetPortReconciler, ctx context.Context, ns string, name string) error {
			return nil
		})
	defer patchesDeleteSubnetPortByName.Reset()
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, nil, ret)

	// not found and deletion failed
	err := errors.New("Deletion failed")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errNotFound)
	patchesDeleteSubnetPortByName = gomonkey.ApplyFunc((*SubnetPortReconciler).deleteSubnetPortByName,
		func(r *SubnetPortReconciler, ctx context.Context, ns string, name string) error {
			return err
		})
	defer patchesDeleteSubnetPortByName.Reset()
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, err, ret)

	// CheckAndGetSubnetPathForSubnetPort fails
	sp := &v1alpha1.SubnetPort{}
	err = errors.New("CheckAndGetSubnetPathForSubnetPort failed")
	patchesCheckAndGetSubnetPathForSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).CheckAndGetSubnetPathForSubnetPort,
		func(r *SubnetPortReconciler, ctx context.Context, obj *v1alpha1.SubnetPort) (bool, bool, string, error) {
			return false, false, "", err
		})
	defer patchesCheckAndGetSubnetPathForSubnetPort.Reset()
	patchesGetByKey := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetByKey,
		func(s *subnetport.SubnetPortStore, key string) *model.VpcSubnetPort {
			return nil
		})
	defer patchesGetByKey.Reset()
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			v1sp := obj.(*v1alpha1.SubnetPort)
			v1sp.Spec.Subnet = "subnet1"
			return nil
		})
	k8sClient.EXPECT().Status().Return(fakewriter)
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, err, ret)

	// getLabelsFromVirtualMachine fails
	err = errors.New("getLabelsFromVirtualMachine failed")
	patchesCheckAndGetSubnetPathForSubnetPort = gomonkey.ApplyFunc((*SubnetPortReconciler).CheckAndGetSubnetPathForSubnetPort,
		func(r *SubnetPortReconciler, ctx context.Context, obj *v1alpha1.SubnetPort) (bool, bool, string, error) {
			return true, false, "", nil
		})
	defer patchesCheckAndGetSubnetPathForSubnetPort.Reset()
	patchesGetLabelsFromVirtualMachine := gomonkey.ApplyFunc((*SubnetPortReconciler).getLabelsFromVirtualMachine,
		func(r *SubnetPortReconciler, ctx context.Context, obj *v1alpha1.SubnetPort) (*map[string]string, error) {
			return nil, err
		})
	defer patchesGetLabelsFromVirtualMachine.Reset()
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			v1sp := obj.(*v1alpha1.SubnetPort)
			v1sp.Spec.Subnet = "subnet1"
			return nil
		})
	k8sClient.EXPECT().Status().Return(fakewriter)
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, err, ret)

	// CreateOrUpdateSubnetPort fails
	patchesGetLabelsFromVirtualMachine = gomonkey.ApplyFunc((*SubnetPortReconciler).getLabelsFromVirtualMachine,
		func(r *SubnetPortReconciler, ctx context.Context, obj *v1alpha1.SubnetPort) (*map[string]string, error) {
			return nil, nil
		})
	defer patchesGetLabelsFromVirtualMachine.Reset()
	patchesVmMapFunc := gomonkey.ApplyFunc((*SubnetPortReconciler).vmMapFunc,
		func(r *SubnetPortReconciler, _ context.Context, vm client.Object) []reconcile.Request {
			requests := []reconcile.Request{}
			return requests
		})
	defer patchesVmMapFunc.Reset()
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			v1sp := obj.(*v1alpha1.SubnetPort)
			v1sp.Spec.Subnet = "subnet1"
			return nil
		})
	err = errors.New("CreateOrUpdateSubnetPort failed")
	patchesCreateOrUpdateSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
		func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string) (*model.SegmentPortState, error) {
			return nil, err
		})
	defer patchesCreateOrUpdateSubnetPort.Reset()
	k8sClient.EXPECT().Status().Return(fakewriter)
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, err, ret)

	// happy path
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			v1sp := obj.(*v1alpha1.SubnetPort)
			v1sp.Spec.Subnet = "subnet1"
			return nil
		})
	portIP := "1.2.3.4"
	portMac := "aa:bb:cc:dd"
	attachmentID := "attachment-id"
	portState := &model.SegmentPortState{
		RealizedBindings: []model.AddressBindingEntry{
			{
				Binding: &model.PacketAddressClassifier{
					IpAddress:  &portIP,
					MacAddress: &portMac,
				},
			},
		},
		Attachment: &model.SegmentPortAttachmentState{
			Id: &attachmentID,
		},
	}
	patchesCreateOrUpdateSubnetPort = gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
		func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string) (*model.SegmentPortState, error) {
			return portState, nil
		})
	defer patchesCreateOrUpdateSubnetPort.Reset()
	patchesSetAddressBindingStatus := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort,
		func(client client.Client, ctx context.Context, subnetPort *v1alpha1.SubnetPort, subnetPortService *subnetport.SubnetPortService) {
			return
		})
	defer patchesSetAddressBindingStatus.Reset()
	k8sClient.EXPECT().Status().Return(fakewriter)
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, nil, ret)

	// handle deletion event - delete NSX subnet port failed
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			v1sp := obj.(*v1alpha1.SubnetPort)
			v1sp.Spec.Subnet = "subnet1"
			time := metav1.Now()
			v1sp.ObjectMeta.DeletionTimestamp = &time
			return nil
		})
	err = errors.New("DeleteSubnetPort failed")
	patchesDeleteSubnetPortById := gomonkey.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPortById,
		func(s *subnetport.SubnetPortService, uid string) error {
			return err
		})
	defer patchesDeleteSubnetPortById.Reset()
	patchesCreateOrUpdateSubnetPort = gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
		func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string) (*model.SegmentPortState, error) {
			assert.FailNow(t, "should not be called")
			return nil, nil
		})
	defer patchesCreateOrUpdateSubnetPort.Reset()
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, err, ret)

	// handle deletion event - successfully deleted
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			v1sp := obj.(*v1alpha1.SubnetPort)
			v1sp.Spec.Subnet = "subnet1"
			time := metav1.Now()
			v1sp.ObjectMeta.DeletionTimestamp = &time
			return nil
		})
	patchesDeleteSubnetPortById = gomonkey.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPortById,
		func(s *subnetport.SubnetPortService, uid string) error {
			return nil
		})
	defer patchesDeleteSubnetPortById.Reset()
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, nil, ret)
}

func TestSubnetPortReconciler_GarbageCollector(t *testing.T) {
	// gc collect item "2345", local store has more item than k8s cache
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
	patchesListNSXSubnetPortIDForCR := gomonkey.ApplyFunc((*subnetport.SubnetPortService).ListNSXSubnetPortIDForCR,
		func(s *subnetport.SubnetPortService) sets.Set[string] {
			a := sets.New[string]()
			a.Insert("1234")
			a.Insert("2345")
			return a
		})
	defer patchesListNSXSubnetPortIDForCR.Reset()
	patchesDeleteSubnetPortById := gomonkey.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPortById,
		func(s *subnetport.SubnetPortService, uid string) error {
			return nil
		})
	defer patchesDeleteSubnetPortById.Reset()

	r := &SubnetPortReconciler{
		Client:            k8sClient,
		Scheme:            nil,
		SubnetPortService: service,
	}
	r.StatusUpdater = common.NewStatusUpdater(k8sClient, service.NSXConfig, r.Recorder, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")
	subnetPortList := &v1alpha1.SubnetPortList{}
	k8sClient.EXPECT().List(gomock.Any(), subnetPortList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.SubnetPortList)
		a.Items = append(a.Items, v1alpha1.SubnetPort{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "1234"
		a.Items[0].Name = "subnetPort1"
		return nil
	})
	patches := gomonkey.ApplyPrivateMethod(r, "collectAddressBindingGarbage", func(r *SubnetPortReconciler, _ context.Context) {})
	defer patches.Reset()
	r.CollectGarbage(context.Background())
}

func TestSubnetPortReconciler_subnetPortNamespaceVMIndexFunc(t *testing.T) {
	tests := []struct {
		name           string
		expectedResult []string
		obj            client.Object
	}{
		{
			name:           "Success",
			expectedResult: []string{"ns1/vm1"},
			obj: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{servicecommon.AnnotationAttachmentRef: "virtualmachine/vm1/port1"},
					Namespace:   "ns1",
				},
			},
		},
		{
			name:           "InvalidObj",
			expectedResult: []string{},
			obj:            &v1alpha1.Subnet{},
		},
		{
			name:           "InvalidAnnotation",
			expectedResult: []string{},
			obj: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := subnetPortNamespaceVMIndexFunc(tt.obj)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestSubnetPortReconciler_addressBindingNamespaceVMIndexFunc(t *testing.T) {
	tests := []struct {
		name           string
		expectedResult []string
		obj            client.Object
	}{
		{
			name:           "Success",
			expectedResult: []string{"ns1/vm1"},
			obj: &v1alpha1.AddressBinding{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1",
				},
				Spec: v1alpha1.AddressBindingSpec{
					VMName: "vm1",
				},
			},
		},
		{
			name:           "InvalidObj",
			expectedResult: []string{},
			obj:            &v1alpha1.Subnet{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := addressBindingNamespaceVMIndexFunc(tt.obj)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestSubnetPortReconciler_vmMapFunc(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	service := &subnetport.SubnetPortService{}
	r := &SubnetPortReconciler{
		Client:            k8sClient,
		SubnetPortService: service,
	}
	subnetPortList := &v1alpha1.SubnetPortList{}
	k8sClient.EXPECT().List(gomock.Any(), subnetPortList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.SubnetPortList)
		a.Items = append(a.Items, v1alpha1.SubnetPort{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   "ns",
				Name:        "subentport-1",
				Annotations: map[string]string{servicecommon.AnnotationAttachmentRef: "virtualmachine/vm1/port1"},
			},
		})
		return nil
	})
	// mock the vm using pod
	vm := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vm1",
			Namespace: "ns",
		},
	}
	requests := r.vmMapFunc(context.TODO(), vm)
	assert.Equal(t, 1, len(requests))
	assert.Equal(t, reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "subentport-1",
			Namespace: "ns",
		},
	}, requests[0])
}

func TestSubnetPortReconciler_deleteSubnetPortByName(t *testing.T) {
	subnetportId1 := "subnetport-1"
	subnetportId2 := "subnetport-2"
	namespaceScope := "nsx-op/vm_namespace"
	subnetportName := "subnetport"
	ns := "ns"
	nameScope := "nsx-op/subnetport_name"
	sp1 := &model.VpcSubnetPort{
		Id: &subnetportId1,
		Tags: []model.Tag{
			{
				Scope: &namespaceScope,
				Tag:   &ns,
			},
			{
				Scope: &nameScope,
				Tag:   &subnetportName,
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
				Tag:   &subnetportName,
			},
		},
	}
	r := &SubnetPortReconciler{
		SubnetPortService: &subnetport.SubnetPortService{},
	}
	patchesListSubnetPortIDsFromCRs := gomonkey.ApplyFunc((*subnetport.SubnetPortService).ListSubnetPortIDsFromCRs,
		func(s *subnetport.SubnetPortService, _ context.Context) (sets.Set[string], error) {
			crSubnetPortIDsSet := sets.New[string]()
			crSubnetPortIDsSet.Insert(subnetportId1)
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
	err := r.deleteSubnetPortByName(context.TODO(), ns, subnetportName)
	assert.Nil(t, err)
}

func TestSubnetPortReconciler_setReadyStatusTrue(t *testing.T) {
	subnetportId1 := "subnetport-1"
	subnetportNamespacedNamescope := "nsx-op/subnetport_namespaced_name"
	subnetportNamespacedName := "ns/subnetport"
	externalIpAddress := "10.0.0.1"
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	subnetPortService := &subnetport.SubnetPortService{
		Service: servicecommon.Service{
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
		SubnetPortStore: &subnetport.SubnetPortStore{},
	}

	patchesGetByKey := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetByKey,
		func(s *subnetport.SubnetPortStore, key string) *model.VpcSubnetPort {
			return &model.VpcSubnetPort{
				Id: &subnetportId1,
				Tags: []model.Tag{
					{
						Scope: &subnetportNamespacedNamescope,
						Tag:   &subnetportNamespacedName,
					},
				},
				ExternalAddressBinding: &model.ExternalAddressBinding{
					ExternalIpAddress: &externalIpAddress,
				},
			}
		})
	defer patchesGetByKey.Reset()

	patchesGetAddressBindingBySubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetAddressBindingBySubnetPort,
		func(s *subnetport.SubnetPortService, sp *v1alpha1.SubnetPort) *v1alpha1.AddressBinding {
			return &v1alpha1.AddressBinding{}
		})
	defer patchesGetAddressBindingBySubnetPort.Reset()

	fakewriter := fakeStatusWriter{}
	k8sClient.EXPECT().Status().Return(fakewriter)

	patches := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort, func(client client.Client, ctx context.Context, subnetPort *v1alpha1.SubnetPort, subnetPortService *subnetport.SubnetPortService, transitionTime metav1.Time, e error) {
	})
	defer patches.Reset()
	sp := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "subnetport-1",
		},
		Status: v1alpha1.SubnetPortStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type: v1alpha1.Ready,
				},
			},
		},
	}
	setReadyStatusTrue(k8sClient, context.TODO(), sp, metav1.Now(), subnetPortService)
}

func TestSubnetPortReconciler_CheckAndGetSubnetPathForSubnetPort(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	r := &SubnetPortReconciler{
		Client: k8sClient,
		SubnetPortService: &subnetport.SubnetPortService{
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
		SubnetService: &subnet.SubnetService{},
	}

	tests := []struct {
		name               string
		prepareFunc        func(*testing.T, *SubnetPortReconciler) *gomonkey.Patches
		expectedIsStale    bool
		expectedErr        string
		expectedSubnetPath string
		expectedIsExisting bool
		subnetport         *v1alpha1.SubnetPort
	}{
		{
			name: "ExistedSubnet",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetSubnetPathForSubnetPortFromStore,
					func(s *subnetport.SubnetPortService, nsxSubnetPortID string) string {
						return "subnet-path-1"
					})
				patches.ApplyFunc((*subnet.SubnetService).GetSubnetByPath,
					func(s *subnet.SubnetService, path string) (*model.VpcSubnet, error) {
						return nil, nil
					})
				return patches
			},
			expectedSubnetPath: "subnet-path-1",
			subnetport: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnetport-1",
					Namespace: "ns-1",
				},
			},
			expectedIsExisting: true,
		},
		{
			name: "FailedToDeleteSubnetPort",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetSubnetPathForSubnetPortFromStore,
					func(s *subnetport.SubnetPortService, nsxSubnetPortID string) string {
						return "subnet-1"
					})
				patches.ApplyFunc((*subnet.SubnetService).GetSubnetByPath,
					func(s *subnet.SubnetService, path string) (*model.VpcSubnet, error) {
						return nil, fmt.Errorf("mock error")
					})
				patches.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPortById,
					func(s *subnetport.SubnetPortService, portID string) error {
						return fmt.Errorf("failed to delete")
					})
				return patches
			},
			expectedErr: "failed to delete",
			subnetport: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnetport-1",
					Namespace: "ns-1",
				},
			},
		},
		{
			name: "SpecificSubnetNotExisted",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetSubnetPathForSubnetPortFromStore,
					func(s *subnetport.SubnetPortService, nsxSubnetPortID string) string {
						return ""
					})
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("not found"))
				return patches
			},
			expectedErr: "not found",
			subnetport: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnetport-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					Subnet: "subnet-1",
				},
			},
		},
		{
			name: "SpecificSubnetDeleted",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetSubnetPathForSubnetPortFromStore,
					func(s *subnetport.SubnetPortService, nsxSubnetPortID string) string {
						return ""
					})
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					subnetCR := obj.(*v1alpha1.Subnet)
					subnetCR.DeletionTimestamp = &metav1.Time{Time: time.Now()}
					return nil
				})
				return patches
			},
			expectedErr:     "subnet ns-1/subnet-1 is being deleted, cannot operate subnetport subnetport-1",
			expectedIsStale: true,
			subnetport: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnetport-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					Subnet: "subnet-1",
				},
			},
		},
		{
			name: "SpecificSubnetFoundMultiple",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetSubnetPathForSubnetPortFromStore,
					func(s *subnetport.SubnetPortService, nsxSubnetPortID string) string {
						return ""
					})
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					subnetCR := obj.(*v1alpha1.Subnet)
					subnetCR.Name = "subnet-1"
					subnetCR.UID = "subnet-id-1"
					return nil
				})
				patches.ApplyFunc((*subnet.SubnetService).GetSubnetsByIndex,
					func(s *subnet.SubnetService, key string, value string) []*model.VpcSubnet {
						return []*model.VpcSubnet{{}, {}}
					})
				return patches
			},
			expectedErr: "multiple NSX subnets found for subnet CR subnet-1(subnet-id-1)",
			subnetport: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnetport-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					Subnet: "subnet-1",
				},
			},
		},
		{
			name: "SpecificSubnetSuccess",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetSubnetPathForSubnetPortFromStore,
					func(s *subnetport.SubnetPortService, nsxSubnetPortID string) string {
						return ""
					})
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					subnetCR := obj.(*v1alpha1.Subnet)
					subnetCR.Name = "subnet-1"
					subnetCR.UID = "subnet-id-1"
					return nil
				})
				patches.ApplyFunc((*subnet.SubnetService).GetSubnetsByIndex,
					func(s *subnet.SubnetService, key string, value string) []*model.VpcSubnet {
						return []*model.VpcSubnet{{
							Path:           servicecommon.String("subnet-path-1"),
							Ipv4SubnetSize: servicecommon.Int64(16),
							Id:             servicecommon.String("subnet-1"),
						}}
					})
				patches.ApplyFunc((*subnetport.SubnetPortService).AllocatePortFromSubnet,
					func(s *subnetport.SubnetPortService, nsxSubnet *model.VpcSubnet) bool {
						return true
					})
				return patches
			},
			expectedSubnetPath: "subnet-path-1",
			subnetport: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnetport-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					Subnet: "subnet-1",
				},
			},
		},

		{
			name: "SpecificSubnetSetNotExisted",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetSubnetPathForSubnetPortFromStore,
					func(s *subnetport.SubnetPortService, nsxSubnetPortID string) string {
						return ""
					})
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("not found"))
				return patches
			},
			expectedErr: "not found",
			subnetport: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnetport-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					SubnetSet: "subnetset-1",
				},
			},
		},
		{
			name: "SpecificSubnetSetDeleted",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetSubnetPathForSubnetPortFromStore,
					func(s *subnetport.SubnetPortService, nsxSubnetPortID string) string {
						return ""
					})
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					subnetSetCR := obj.(*v1alpha1.SubnetSet)
					subnetSetCR.DeletionTimestamp = &metav1.Time{Time: time.Now()}
					return nil
				})
				return patches
			},
			expectedErr:     "subnetset ns-1/subnetset-1 is being deleted, cannot operate subnetport subnetport-1",
			expectedIsStale: true,
			subnetport: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnetport-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					SubnetSet: "subnetset-1",
				},
			},
		},
		{
			name: "SpecificSubnetSetSuccess",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetSubnetPathForSubnetPortFromStore,
					func(s *subnetport.SubnetPortService, nsxSubnetPortID string) string {
						return ""
					})
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					subnetSetCR := obj.(*v1alpha1.SubnetSet)
					subnetSetCR.Name = "subnetset-1"
					subnetSetCR.UID = "subnetset-id-1"
					return nil
				})
				patches.ApplyFunc(common.AllocateSubnetFromSubnetSet,
					func(subnetSet *v1alpha1.SubnetSet, vpcService servicecommon.VPCServiceProvider, subnetService servicecommon.SubnetServiceProvider, subnetPortService servicecommon.SubnetPortServiceProvider) (string, error) {
						return "subnet-path-1", nil
					})
				return patches
			},
			expectedSubnetPath: "subnet-path-1",
			subnetport: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnetport-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					SubnetSet: "subnetset-1",
				},
			},
		},
		{
			name: "DefaultSubnetDeleted",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetSubnetPathForSubnetPortFromStore,
					func(s *subnetport.SubnetPortService, nsxSubnetPortID string) string {
						return ""
					})
				patches.ApplyFunc(common.GetDefaultSubnetSet,
					func(client client.Client, ctx context.Context, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
						subnetSetCR := &v1alpha1.SubnetSet{}
						subnetSetCR.DeletionTimestamp = &metav1.Time{Time: time.Now()}
						subnetSetCR.Name = "default-subnetset"
						return subnetSetCR, nil
					})
				return patches
			},
			expectedErr:     "default subnetset default-subnetset is being deleted, cannot operate subnetport subnetport-1",
			expectedIsStale: true,
			subnetport: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnetport-1",
					Namespace: "ns-1",
				},
			},
		},
		{
			name: "DefaultSubnetSuccess",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetSubnetPathForSubnetPortFromStore,
					func(s *subnetport.SubnetPortService, nsxSubnetPortID string) string {
						return ""
					})
				patches.ApplyFunc(common.GetDefaultSubnetSet,
					func(client client.Client, ctx context.Context, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
						subnetSetCR := &v1alpha1.SubnetSet{}
						subnetSetCR.Name = "default-subnetset"
						return subnetSetCR, nil
					})
				patches.ApplyFunc(common.AllocateSubnetFromSubnetSet,
					func(subnetSet *v1alpha1.SubnetSet, vpcService servicecommon.VPCServiceProvider, subnetService servicecommon.SubnetServiceProvider, subnetPortService servicecommon.SubnetPortServiceProvider) (string, error) {
						return "subnet-path-1", nil
					})
				return patches
			},
			expectedSubnetPath: "subnet-path-1",
			subnetport: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnetport-1",
					Namespace: "ns-1",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()
			patches := tt.prepareFunc(t, r)
			defer patches.Reset()
			isExisting, isStale, subnetPath, err := r.CheckAndGetSubnetPathForSubnetPort(ctx, tt.subnetport)
			assert.Equal(t, tt.expectedIsStale, isStale)
			assert.Equal(t, tt.expectedIsExisting, isExisting)
			if tt.expectedErr != "" {
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				assert.Equal(t, tt.expectedSubnetPath, subnetPath)
			}
		})
	}
}

func TestSubnetPortReconciler_updateSubnetStatusOnSubnetPort(t *testing.T) {
	patchesGetGatewayPrefixForSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetGatewayPrefixForSubnetPort,
		func(s *subnetport.SubnetPortService, obj *v1alpha1.SubnetPort, nsxSubnetPath string) (string, int, error) {
			return "10.0.0.1", 28, nil
		})
	defer patchesGetGatewayPrefixForSubnetPort.Reset()
	patchesGetSubnetByPath := gomonkey.ApplyFunc((*subnet.SubnetService).GetSubnetByPath,
		func(s *subnet.SubnetService, path string) (*model.VpcSubnet, error) {
			return &model.VpcSubnet{
				RealizationId: servicecommon.String("realization-id-1"),
			}, nil
		})
	defer patchesGetSubnetByPath.Reset()
	sp := &v1alpha1.SubnetPort{
		Status: v1alpha1.SubnetPortStatus{
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{IPAddress: "10.0.0.2"},
				},
			},
		},
	}
	r := &SubnetPortReconciler{
		SubnetPortService: &subnetport.SubnetPortService{},
		SubnetService:     &subnet.SubnetService{},
	}
	err := r.updateSubnetStatusOnSubnetPort(sp, "subnet-path-1")
	assert.Nil(t, err)
	expectedSp := &v1alpha1.SubnetPort{
		Status: v1alpha1.SubnetPortStatus{
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{
						IPAddress: "10.0.0.2/28",
						Gateway:   "10.0.0.1",
					},
				},
				LogicalSwitchUUID: "realization-id-1",
			},
		},
	}
	assert.Equal(t, expectedSp, sp)
}

func TestSubnetPortReconciler_getLabelsFromVirtualMachine(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	r := &SubnetPortReconciler{
		Client: k8sClient,
	}
	patchesGetVirtualMachineNameForSubnetPort := gomonkey.ApplyFunc(common.GetVirtualMachineNameForSubnetPort,
		func(subnetPort *v1alpha1.SubnetPort) (string, string, error) {
			return "vm-1", "", nil
		})
	defer patchesGetVirtualMachineNameForSubnetPort.Reset()

	labels := map[string]string{"fakeLabel": "exists"}
	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		vm := obj.(*vmv1alpha1.VirtualMachine)
		vm.ObjectMeta.Labels = labels
		return nil
	})
	sp := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "subnetport-1",
			Namespace: "ns-1",
		},
	}
	res, err := r.getLabelsFromVirtualMachine(context.TODO(), sp)
	assert.Nil(t, err)
	assert.Equal(t, labels, *res)
}

func TestSubnetPortReconciler_addressBindingMapFunc(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	r := &SubnetPortReconciler{
		Client: k8sClient,
	}

	tests := []struct {
		name           string
		prepareFunc    func(*testing.T, *SubnetPortReconciler) *gomonkey.Patches
		expectedResult []reconcile.Request
		obj            client.Object
	}{
		{
			name:        "AddressBindingInvalid",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches { return nil },
			obj:         &v1alpha1.SubnetPort{},
		},
		{
			name: "DefaultAddressBinding",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.SubnetPortList)
					a.Items = append(a.Items, v1alpha1.SubnetPort{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "ns-1",
							Name:      "subnetport-1",
						},
					})
					return nil
				})
				return nil
			},
			obj: &v1alpha1.AddressBinding{},
			expectedResult: []reconcile.Request{{
				NamespacedName: types.NamespacedName{
					Namespace: "ns-1",
					Name:      "subnetport-1",
				},
			}},
		},
		{
			name: "SpeficAddressBinding",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.SubnetPortList)
					a.Items = append(a.Items, v1alpha1.SubnetPort{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "ns-1",
							Name:      "subnetport-1",
						},
					})
					return nil
				})
				patchesGetVirtualMachineNameForSubnetPort := gomonkey.ApplyFunc(common.GetVirtualMachineNameForSubnetPort,
					func(subnetPort *v1alpha1.SubnetPort) (string, string, error) {
						return "vm-1", "port-1", nil
					})
				return patchesGetVirtualMachineNameForSubnetPort
			},
			obj: &v1alpha1.AddressBinding{
				Spec: v1alpha1.AddressBindingSpec{
					InterfaceName: "port-1",
				},
			},
			expectedResult: []reconcile.Request{{
				NamespacedName: types.NamespacedName{
					Namespace: "ns-1",
					Name:      "subnetport-1",
				},
			}},
		},
		{
			name: "ListSubnetPortError",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("mock error")).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					return nil
				})
				return nil
			},
			obj: &v1alpha1.AddressBinding{
				Spec: v1alpha1.AddressBindingSpec{
					InterfaceName: "port-1",
				},
			},
			expectedResult: nil,
		},
		{
			name: "NoSubnetPort",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					return nil
				})
				patches := gomonkey.ApplyFunc(setAddressBindingStatus, func(client client.Client, ctx context.Context, ab *v1alpha1.AddressBinding, transitionTime metav1.Time, e error, ipAddress string) {
					assert.Equal(t, vmOrInterfaceNotFoundError, e)
					assert.Equal(t, "", ipAddress)
				})
				return patches
			},
			obj: &v1alpha1.AddressBinding{
				Spec: v1alpha1.AddressBindingSpec{
					InterfaceName: "port-1",
				},
			},
			expectedResult: nil,
		},
		{
			name: "DefaultAddressBindingMultiInterface",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.SubnetPortList)
					a.Items = append(a.Items, v1alpha1.SubnetPort{
						ObjectMeta: metav1.ObjectMeta{
							Namespace:         "ns-1",
							Name:              "subnetport-1",
							CreationTimestamp: metav1.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
						},
					}, v1alpha1.SubnetPort{
						ObjectMeta: metav1.ObjectMeta{
							Namespace:         "ns-1",
							Name:              "subnetport-2",
							CreationTimestamp: metav1.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC),
						},
					})
					return nil
				})
				patches := gomonkey.ApplyFunc(setAddressBindingStatus, func(client client.Client, ctx context.Context, ab *v1alpha1.AddressBinding, transitionTime metav1.Time, e error, ipAddress string) {
					assert.Equal(t, multipleInterfaceFoundError, e)
					assert.Equal(t, "", ipAddress)
				})
				return patches
			},
			obj: &v1alpha1.AddressBinding{},
			expectedResult: []reconcile.Request{{
				NamespacedName: types.NamespacedName{
					Namespace: "ns-1",
					Name:      "subnetport-2",
				},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.prepareFunc(t, r)
			if patches != nil {
				defer patches.Reset()
			}
			reqs := r.addressBindingMapFunc(context.TODO(), tt.obj)
			assert.Equal(t, tt.expectedResult, reqs)
		})
	}
}

func TestSubnetPortReconciler_setAddressBindingStatusBySubnetPort(t *testing.T) {
	type args struct {
		subnetPort     *v1alpha1.SubnetPort
		transitionTime metav1.Time
		e              error
	}
	tests := []struct {
		name        string
		prepareFunc func(r *SubnetPortReconciler) *gomonkey.Patches
		args        args
	}{
		{
			name: "NoAddressBinding",
			prepareFunc: func(r *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(r.SubnetPortService.SubnetPortStore, "GetByKey", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				patches.ApplyMethodSeq(r.SubnetPortService, "GetAddressBindingBySubnetPort", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
			args: args{
				subnetPort:     &v1alpha1.SubnetPort{},
				transitionTime: metav1.Now(),
				e:              nil,
			},
		},
		{
			name: "NoSubnetPort",
			prepareFunc: func(r *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(r.SubnetPortService.SubnetPortStore, "GetByKey", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				patches.ApplyMethodSeq(r.SubnetPortService, "GetAddressBindingBySubnetPort", []gomonkey.OutputCell{{
					Values: gomonkey.Params{&v1alpha1.AddressBinding{ObjectMeta: metav1.ObjectMeta{Name: "ab1", Namespace: "ns1"}}},
					Times:  1,
				}})
				patches.ApplyFunc(setAddressBindingStatus, func(client client.Client, ctx context.Context, ab *v1alpha1.AddressBinding, transitionTime metav1.Time, e error, ipAddress string) {
					assert.Equal(t, &v1alpha1.AddressBinding{ObjectMeta: metav1.ObjectMeta{Name: "ab1", Namespace: "ns1"}}, ab)
					assert.Equal(t, metav1.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), transitionTime)
					assert.Equal(t, vmOrInterfaceNotFoundError, e)
					assert.Equal(t, "", ipAddress)
				})
				return patches
			},
			args: args{
				subnetPort:     &v1alpha1.SubnetPort{},
				transitionTime: metav1.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
				e:              nil,
			},
		},
		{
			name: "HasIP",
			prepareFunc: func(r *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethodSeq(r.SubnetPortService.SubnetPortStore, "GetByKey", []gomonkey.OutputCell{{
					Values: gomonkey.Params{&model.VpcSubnetPort{ExternalAddressBinding: &model.ExternalAddressBinding{ExternalIpAddress: ptr.To("192.168.0.2")}}},
					Times:  1,
				}})
				patches.ApplyMethodSeq(r.SubnetPortService, "GetAddressBindingBySubnetPort", []gomonkey.OutputCell{{
					Values: gomonkey.Params{&v1alpha1.AddressBinding{ObjectMeta: metav1.ObjectMeta{Name: "ab1", Namespace: "ns1"}}},
					Times:  1,
				}})
				patches.ApplyFunc(setAddressBindingStatus, func(client client.Client, ctx context.Context, ab *v1alpha1.AddressBinding, transitionTime metav1.Time, e error, ipAddress string) {
					assert.Equal(t, &v1alpha1.AddressBinding{ObjectMeta: metav1.ObjectMeta{Name: "ab1", Namespace: "ns1"}}, ab)
					assert.Equal(t, metav1.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), transitionTime)
					assert.Equal(t, nil, e)
					assert.Equal(t, "192.168.0.2", ipAddress)
				})
				return patches
			},
			args: args{
				subnetPort:     &v1alpha1.SubnetPort{},
				transitionTime: metav1.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
				e:              nil,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &SubnetPortReconciler{
				SubnetPortService: &subnetport.SubnetPortService{SubnetPortStore: &subnetport.SubnetPortStore{}},
			}
			patches := tt.prepareFunc(r)
			if patches != nil {
				defer patches.Reset()
			}
			setAddressBindingStatusBySubnetPort(r.Client, context.TODO(), tt.args.subnetPort, r.SubnetPortService, tt.args.transitionTime, tt.args.e)
		})
	}
}

func TestSubnetPortReconciler_setAddressBindingStatus(t *testing.T) {
	resourceType := "AddressBinding"
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	r := &SubnetPortReconciler{
		Client: k8sClient,
	}
	type args struct {
		ab             *v1alpha1.AddressBinding
		transitionTime metav1.Time
		e              error
		ipAddress      string
	}
	tests := []struct {
		name        string
		prepareFunc func(r *SubnetPortReconciler) *gomonkey.Patches
		args        args
	}{
		{
			name:        "SuccessNoUpdate",
			prepareFunc: func(r *SubnetPortReconciler) *gomonkey.Patches { return nil },
			args: args{
				ab: &v1alpha1.AddressBinding{Status: v1alpha1.AddressBindingStatus{
					Conditions: []v1alpha1.Condition{{
						Type:               v1alpha1.Ready,
						Status:             corev1.ConditionTrue,
						Message:            fmt.Sprintf("%s has been successfully created/updated", resourceType),
						Reason:             fmt.Sprintf("%sReady", resourceType),
						LastTransitionTime: metav1.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
					}},
					IPAddress: "192.168.0.2",
				}},
				transitionTime: metav1.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC),
				e:              nil,
				ipAddress:      "192.168.0.2",
			},
		},
		{
			name: "SuccessNewCondition",
			prepareFunc: func(r *SubnetPortReconciler) *gomonkey.Patches {
				statusWriter := fakeStatusWriter{
					t:           t,
					validateObj: true,
					expectObj: &v1alpha1.AddressBinding{Status: v1alpha1.AddressBindingStatus{
						Conditions: []v1alpha1.Condition{{
							Type:               v1alpha1.Ready,
							Status:             corev1.ConditionTrue,
							Message:            fmt.Sprintf("%s has been successfully created/updated", resourceType),
							Reason:             fmt.Sprintf("%sReady", resourceType),
							LastTransitionTime: metav1.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
						}},
						IPAddress: "192.168.0.3",
					}},
				}
				r.Client.(*mock_client.MockClient).EXPECT().Status().Return(statusWriter)
				return nil
			},
			args: args{
				ab:             &v1alpha1.AddressBinding{Status: v1alpha1.AddressBindingStatus{}},
				transitionTime: metav1.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
				e:              nil,
				ipAddress:      "192.168.0.3",
			},
		},
		{
			name: "SuccessToFail",
			prepareFunc: func(r *SubnetPortReconciler) *gomonkey.Patches {
				statusWriter := fakeStatusWriter{
					t:           t,
					validateObj: true,
					expectObj: &v1alpha1.AddressBinding{Status: v1alpha1.AddressBindingStatus{
						Conditions: []v1alpha1.Condition{{
							Type:               v1alpha1.Ready,
							Status:             corev1.ConditionFalse,
							Message:            fmt.Sprintf("error occurred while processing the %s CR. Error: %v", resourceType, fmt.Errorf("mock error")),
							Reason:             fmt.Sprintf("%sNotReady", resourceType),
							LastTransitionTime: metav1.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
						}},
						IPAddress: "",
					}},
				}
				r.Client.(*mock_client.MockClient).EXPECT().Status().Return(statusWriter)
				return nil
			},
			args: args{
				ab: &v1alpha1.AddressBinding{Status: v1alpha1.AddressBindingStatus{
					Conditions: []v1alpha1.Condition{{
						Type:               v1alpha1.Ready,
						Status:             corev1.ConditionTrue,
						Message:            fmt.Sprintf("%s has been successfully created/updated", resourceType),
						Reason:             fmt.Sprintf("%sReady", resourceType),
						LastTransitionTime: metav1.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
					}},
					IPAddress: "192.168.0.2",
				}},
				transitionTime: metav1.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC),
				e:              fmt.Errorf("mock error"),
				ipAddress:      "",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.prepareFunc(r)
			if patches != nil {
				defer patches.Reset()
			}
			setAddressBindingStatus(r.Client, context.TODO(), tt.args.ab, tt.args.transitionTime, tt.args.e, tt.args.ipAddress)
		})
	}
}

func TestSubnetPortReconciler_collectAddressBindingGarbage(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	r := &SubnetPortReconciler{
		Client: k8sClient,
	}
	type args struct {
		namespace *string
		ipAddress *string
	}
	tests := []struct {
		name        string
		prepareFunc func(r *SubnetPortReconciler) *gomonkey.Patches
		args        args
	}{
		{
			name: "ListAddressBindingError",
			prepareFunc: func(r *SubnetPortReconciler) *gomonkey.Patches {
				abList := &v1alpha1.AddressBindingList{}
				k8sClient.EXPECT().List(context.TODO(), abList).Return(fmt.Errorf("mock error"))
				return nil
			},
		},
		{
			name: "ListSubnetPortError",
			prepareFunc: func(r *SubnetPortReconciler) *gomonkey.Patches {
				abList := &v1alpha1.AddressBindingList{}
				spList := &v1alpha1.SubnetPortList{}
				k8sClient.EXPECT().List(context.TODO(), abList, gomock.Any()).Return(nil).Do(func(ctx context.Context, list *v1alpha1.AddressBindingList, opts ...client.ListOption) error {
					list.Items = append(list.Items, v1alpha1.AddressBinding{})
					return nil
				})
				k8sClient.EXPECT().List(context.TODO(), spList, gomock.Any()).Return(fmt.Errorf("mock error"))
				return nil
			},
		},
		{
			name: "GCDefaultAddressBindingWithMultipleInterfaces",
			prepareFunc: func(r *SubnetPortReconciler) *gomonkey.Patches {
				abList := &v1alpha1.AddressBindingList{}
				spList := &v1alpha1.SubnetPortList{}
				k8sClient.EXPECT().List(context.TODO(), abList, gomock.Any()).Return(nil).Do(func(ctx context.Context, list *v1alpha1.AddressBindingList, opts ...client.ListOption) error {
					list.Items = append(list.Items, v1alpha1.AddressBinding{Spec: v1alpha1.AddressBindingSpec{VMName: "vm1"}})
					return nil
				})
				k8sClient.EXPECT().List(context.TODO(), spList, gomock.Any()).Return(nil).Do(func(ctx context.Context, list *v1alpha1.SubnetPortList, opts ...client.ListOption) error {
					list.Items = append(list.Items, v1alpha1.SubnetPort{}, v1alpha1.SubnetPort{})
					return nil
				})
				patches := gomonkey.ApplyFunc(setAddressBindingStatus, func(client client.Client, ctx context.Context, ab *v1alpha1.AddressBinding, transitionTime metav1.Time, e error, ipAddress string) {
					assert.Equal(t, &v1alpha1.AddressBinding{Spec: v1alpha1.AddressBindingSpec{VMName: "vm1"}}, ab)
					assert.Equal(t, multipleInterfaceFoundError, e)
					assert.Equal(t, "", ipAddress)
				})
				return patches
			},
		},
		{
			name: "GCAddressBindingWithSubnetPortError",
			prepareFunc: func(r *SubnetPortReconciler) *gomonkey.Patches {
				abList := &v1alpha1.AddressBindingList{}
				spList := &v1alpha1.SubnetPortList{}
				k8sClient.EXPECT().List(context.TODO(), abList, gomock.Any()).Return(nil).Do(func(ctx context.Context, list *v1alpha1.AddressBindingList, opts ...client.ListOption) error {
					list.Items = append(list.Items, v1alpha1.AddressBinding{Spec: v1alpha1.AddressBindingSpec{VMName: "vm1", InterfaceName: "inf1"}})
					return nil
				})
				k8sClient.EXPECT().List(context.TODO(), spList, gomock.Any()).Return(nil).Do(func(ctx context.Context, list *v1alpha1.SubnetPortList, opts ...client.ListOption) error {
					list.Items = append(list.Items, v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{servicecommon.AnnotationAttachmentRef: "invalid"}}})
					return nil
				})
				patches := gomonkey.ApplyFunc(setAddressBindingStatus, func(client client.Client, ctx context.Context, ab *v1alpha1.AddressBinding, transitionTime metav1.Time, e error, ipAddress string) {
					assert.Equal(t, &v1alpha1.AddressBinding{Spec: v1alpha1.AddressBindingSpec{VMName: "vm1", InterfaceName: "inf1"}}, ab)
					assert.Equal(t, vmOrInterfaceNotFoundError, e)
					assert.Equal(t, "", ipAddress)
				})
				return patches
			},
		},
		{
			name: "SubnetPortFound",
			prepareFunc: func(r *SubnetPortReconciler) *gomonkey.Patches {
				abList := &v1alpha1.AddressBindingList{}
				spList := &v1alpha1.SubnetPortList{}
				k8sClient.EXPECT().List(context.TODO(), abList, gomock.Any()).Return(nil).Do(func(ctx context.Context, list *v1alpha1.AddressBindingList, opts ...client.ListOption) error {
					list.Items = append(list.Items, v1alpha1.AddressBinding{Spec: v1alpha1.AddressBindingSpec{VMName: "vm1", InterfaceName: "inf1"}})
					return nil
				})
				k8sClient.EXPECT().List(context.TODO(), spList, gomock.Any()).Return(nil).Do(func(ctx context.Context, list *v1alpha1.SubnetPortList, opts ...client.ListOption) error {
					list.Items = append(list.Items, v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{servicecommon.AnnotationAttachmentRef: fmt.Sprintf("%s/%s/%s", servicecommon.ResourceTypeVirtualMachine, "vm1", "inf1")}}})
					return nil
				})
				return nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.prepareFunc(r)
			if patches != nil {
				defer patches.Reset()
			}
			r.collectAddressBindingGarbage(context.TODO(), tt.args.namespace, tt.args.ipAddress)
		})
	}
}
