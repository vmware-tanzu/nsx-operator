package subnetport

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	testifymock "github.com/stretchr/testify/mock"
	vmv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/mock"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	pkgutil "github.com/vmware-tanzu/nsx-operator/pkg/util"
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

func (writer fakeStatusWriter) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
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
				K8sConfig: &config.K8sConfig{
					SupportReusePort: true,
				},
			},
		},
		SubnetPortStore: &subnetport.SubnetPortStore{},
	}
	subnetService := &mock.MockSubnetServiceProvider{}
	ipAllocationService := &mock.MockIPAddressAllocationProvider{}
	vpcService := &mock.MockVPCServiceProvider{}
	vpcService.On("IsRADeactivatedByVPCPath", testifymock.Anything).Return(false, nil)

	r := &SubnetPortReconciler{
		Client:                     k8sClient,
		APIReader:                  k8sClient,
		Scheme:                     nil,
		SubnetPortService:          service,
		SubnetService:              subnetService,
		VPCService:                 vpcService,
		Recorder:                   fakeRecorder{},
		IpAddressAllocationService: ipAllocationService,
		restoreMode:                false,
	}
	r.StatusUpdater = common.NewStatusUpdater(k8sClient, service.NSXConfig, r.Recorder, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	ctx := context.Background()
	req := controllerruntime.Request{NamespacedName: types.NamespacedName{Namespace: "dummy", Name: "dummy"}}

	patchesGetSubnetByPath := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetByPath",
		func(s *mock.MockSubnetServiceProvider, nsxSubnetPath string, sharedSubnet bool) (*model.VpcSubnet, error) {
			nsxSubnet := &model.VpcSubnet{
				Id:            ptr.To("subnet-1"),
				RealizationId: ptr.To("realization-1"),
			}
			return nsxSubnet, nil
		})
	defer patchesGetSubnetByPath.Reset()

	patchesGetSubnetCR := gomonkey.ApplyFunc((*SubnetPortReconciler).getSubnetCR,
		func(r *SubnetPortReconciler, ctx context.Context, subnetPort *v1alpha1.SubnetPort) (*v1alpha1.Subnet, bool, error) {
			return &v1alpha1.Subnet{
				Spec: v1alpha1.SubnetSpec{
					IPAddressType: v1alpha1.IPAddressTypeIPv4,
				},
			}, false, nil
		})
	defer patchesGetSubnetCR.Reset()

	patchesCreateIPAllocation := gomonkey.ApplyFunc((*mock.MockIPAddressAllocationProvider).CreateIPAddressAllocationForAddressBinding,
		func(_ *mock.MockIPAddressAllocationProvider, _ *v1alpha1.AddressBinding, _ *v1alpha1.SubnetPort, _ bool) error {
			return nil
		})
	defer patchesCreateIPAllocation.Reset()

	patchesDeleteIPAllocation := gomonkey.ApplyFunc((*mock.MockIPAddressAllocationProvider).DeleteIPAddressAllocationForAddressBinding,
		func(_ *mock.MockIPAddressAllocationProvider, _ interface{}) error {
			return nil
		})
	defer patchesDeleteIPAllocation.Reset()

	patchesUpdateSubnetPortIPType := gomonkey.ApplyFunc((*SubnetPortReconciler).updateSubnetPortIPType,
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort, _ v1alpha1.IPAddressType, _ *model.VpcSubnet) error {
			return nil
		})
	defer patchesUpdateSubnetPortIPType.Reset()

	attachmentID := "attachment-id"

	// fail to get
	t.Run("failed to get SubnetPort CR", func(t *testing.T) {
		errFailToGet := errors.New("failed to get CR")
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errFailToGet)
		_, ret := r.Reconcile(ctx, req)
		assert.Equal(t, errFailToGet, ret)
	})

	// not found and deletion success
	t.Run("not found the SubnetPort CR and delete VpcSubnetPort success", func(t *testing.T) {
		errNotFound := apierrors.NewNotFound(v1alpha1.Resource("subnetport"), "")
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errNotFound)

		patchesDeleteSubnetPortByName := gomonkey.ApplyFunc((*SubnetPortReconciler).deleteSubnetPortByName,
			func(r *SubnetPortReconciler, ctx context.Context, ns string, name string) error {
				return nil
			})
		defer patchesDeleteSubnetPortByName.Reset()
		_, ret := r.Reconcile(ctx, req)
		assert.Equal(t, nil, ret)
	})

	// not found and deletion failed
	t.Run("not found the SubnetPort CR and delete VpcSubnetPort failed", func(t *testing.T) {
		errNotFound := apierrors.NewNotFound(v1alpha1.Resource("subnetport"), "")
		err := errors.New("Deletion failed")
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errNotFound)
		patchesDeleteSubnetPortByName := gomonkey.ApplyFunc((*SubnetPortReconciler).deleteSubnetPortByName,
			func(r *SubnetPortReconciler, ctx context.Context, ns string, name string) error {
				return err
			})
		defer patchesDeleteSubnetPortByName.Reset()
		_, ret := r.Reconcile(ctx, req)
		assert.Equal(t, err, ret)
	})

	// CheckAndGetSubnetPathForSubnetPort fails
	t.Run("failed with CheckAndGetSubnetPathForSubnetPort", func(t *testing.T) {
		err := errors.New("CheckAndGetSubnetPathForSubnetPort failed")
		patchesCheckAndGetSubnetPathForSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).CheckAndGetSubnetPathForSubnetPort,
			func(r *SubnetPortReconciler, ctx context.Context, subnetPort *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
				return false, false, "", nil, nil, "", "", err
			})
		defer patchesCheckAndGetSubnetPathForSubnetPort.Reset()

		patchesGetByKey := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
			func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
				return nil, nil
			})
		defer patchesGetByKey.Reset()

		// 1st: Reconciler fetching the resource
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Spec.Subnet = "subnet1"
				return nil
			})
		// 2nd: Triggered by updateSubnetPortStatusConditions inside StatusUpdater.UpdateFail
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil)

		k8sClient.EXPECT().Status().Return(fakewriter)
		_, ret := r.Reconcile(ctx, req)
		assert.Equal(t, err, ret)
	})

	// GetDefaultInterfaceIPType in Reconcile fails
	t.Run("failed with GetDefaultInterfaceIPType error", func(t *testing.T) {
		err := errors.New("mocked interface type error in reconcile")
		patchesCheckAndGetSubnetPathForSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).CheckAndGetSubnetPathForSubnetPort,
			func(r *SubnetPortReconciler, ctx context.Context, subnetPort *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
				return true, false, "subnet-path-1", nil, nil, "", "", nil
			})
		defer patchesCheckAndGetSubnetPathForSubnetPort.Reset()

		patchesIsSharedSubnetPath := gomonkey.ApplyFunc(common.IsSharedSubnetPath, func(ctx context.Context, client client.Client, path string, ns string) (bool, error) {
			return false, nil
		})
		defer patchesIsSharedSubnetPath.Reset()

		patchesGetDefaultInterfaceIPType := gomonkey.ApplyFunc(subnetport.GetDefaultInterfaceIPType,
			func(interfaceIPType v1alpha1.IPAddressType, parentIPAddressType v1alpha1.IPAddressType) (v1alpha1.IPAddressType, error) {
				return "", err
			})
		defer patchesGetDefaultInterfaceIPType.Reset()

		// 1st: Reconciler fetching the resource
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Spec.Subnet = "subnet1"
				return nil
			})

		_, ret := r.Reconcile(ctx, req)
		assert.Equal(t, err, ret)
	})

	// getVirtualMachine fails
	t.Run("failed with getVirtualMachine", func(t *testing.T) {
		err := errors.New("getVirtualMachine failed")
		patchesCheckAndGetSubnetPathForSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).CheckAndGetSubnetPathForSubnetPort,
			func(r *SubnetPortReconciler, ctx context.Context, subnetPort *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
				return true, false, "", nil, nil, v1alpha1.IPAddressTypeIPv4, "", nil
			})
		defer patchesCheckAndGetSubnetPathForSubnetPort.Reset()
		patchesGetByUID := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
			func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
				return &model.VpcSubnetPort{Id: servicecommon.String("port1")}, nil
			})
		defer patchesGetByUID.Reset()
		patchesGetVirtualMachine := gomonkey.ApplyFunc((*SubnetPortReconciler).getVirtualMachine,
			func(r *SubnetPortReconciler, ctx context.Context, obj *v1alpha1.SubnetPort, restoreMode bool) (*vmv1alpha1.VirtualMachine, string, error) {
				return nil, "", err
			})
		defer patchesGetVirtualMachine.Reset()

		// 1st: Fetch in Reconcile
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Spec.Subnet = "subnet1"
				return nil
			})
		// 2nd: Fetch inside updateSubnetPortStatusConditions
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil)

		k8sClient.EXPECT().Status().Return(fakewriter)
		_, ret := r.Reconcile(ctx, req)
		assert.Equal(t, err, ret)
	})

	// CreateOrUpdateSubnetPort fails
	t.Run("failed with CreateOrUpdateSubnetPort", func(t *testing.T) {
		patchesGetVirtualMachine := gomonkey.ApplyFunc((*SubnetPortReconciler).getVirtualMachine,
			func(r *SubnetPortReconciler, ctx context.Context, obj *v1alpha1.SubnetPort, restoreMode bool) (*vmv1alpha1.VirtualMachine, string, error) {
				return nil, "", nil
			})
		defer patchesGetVirtualMachine.Reset()
		patchesCheckAndGetSubnetPathForSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).CheckAndGetSubnetPathForSubnetPort,
			func(r *SubnetPortReconciler, ctx context.Context, subnetPort *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
				return true, false, "", nil, nil, v1alpha1.IPAddressTypeIPv4, "", nil
			})
		defer patchesCheckAndGetSubnetPathForSubnetPort.Reset()
		patchesGetByUID := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
			func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
				return &model.VpcSubnetPort{Id: servicecommon.String("port1")}, nil
			})
		defer patchesGetByUID.Reset()
		patchesVmMapFunc := gomonkey.ApplyFunc((*SubnetPortReconciler).vmMapFunc,
			func(r *SubnetPortReconciler, _ context.Context, vm client.Object) []reconcile.Request {
				requests := []reconcile.Request{}
				return requests
			})
		defer patchesVmMapFunc.Reset()

		// 1st: Fetch in Reconcile
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Spec.Subnet = "subnet1"
				return nil
			})
		// 2nd: Fetch inside updateSubnetPortStatusConditions
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil)

		patchesIsSharedSubnetPath := gomonkey.ApplyFunc(common.IsSharedSubnetPath, func(ctx context.Context, client client.Client, path string, ns string) (bool, error) {
			return false, nil
		})
		defer patchesIsSharedSubnetPath.Reset()
		err := errors.New("CreateOrUpdateSubnetPort failed")

		patchesCreateOrUpdateSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
			func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string, isVmSubnetPort bool, restoreMode bool, interfaceIPType v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
				return nil, err
			})
		defer patchesCreateOrUpdateSubnetPort.Reset()
		patchesGetAddressBindingBySubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetAddressBindingBySubnetPort, func(_ *subnetport.SubnetPortService, _ *v1alpha1.SubnetPort) *v1alpha1.AddressBinding {
			return nil
		})
		defer patchesGetAddressBindingBySubnetPort.Reset()
		k8sClient.EXPECT().Status().Return(fakewriter)
		_, ret := r.Reconcile(ctx, req)
		assert.Equal(t, err, ret)
	})

	// CreateOrUpdateSubnetPort fails with RealizeStateError
	t.Run("failed with CreateOrUpdateSubnetPort with RealizeStateError", func(t *testing.T) {
		patchesGetVirtualMachine := gomonkey.ApplyFunc((*SubnetPortReconciler).getVirtualMachine,
			func(r *SubnetPortReconciler, ctx context.Context, obj *v1alpha1.SubnetPort, restoreMode bool) (*vmv1alpha1.VirtualMachine, string, error) {
				return nil, "", nil
			})
		defer patchesGetVirtualMachine.Reset()
		patchesCheckAndGetSubnetPathForSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).CheckAndGetSubnetPathForSubnetPort,
			func(r *SubnetPortReconciler, ctx context.Context, subnetPort *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
				return true, false, "", nil, nil, v1alpha1.IPAddressTypeIPv4, "", nil
			})
		defer patchesCheckAndGetSubnetPathForSubnetPort.Reset()
		patchesGetByUID := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
			func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
				return &model.VpcSubnetPort{Id: servicecommon.String("port1")}, nil
			})
		defer patchesGetByUID.Reset()
		patchesVmMapFunc := gomonkey.ApplyFunc((*SubnetPortReconciler).vmMapFunc,
			func(r *SubnetPortReconciler, _ context.Context, vm client.Object) []reconcile.Request {
				requests := []reconcile.Request{}
				return requests
			})
		defer patchesVmMapFunc.Reset()

		// 1st: Fetch in Reconcile
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Spec.Subnet = "subnet1"
				return nil
			})
		// 2nd: Fetch inside updateSubnetPortStatusConditions
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil)

		patchesIsSharedSubnetPath := gomonkey.ApplyFunc(common.IsSharedSubnetPath, func(ctx context.Context, client client.Client, path string, ns string) (bool, error) {
			return false, nil
		})
		defer patchesIsSharedSubnetPath.Reset()
		err := nsxutil.NewRealizeStateError("CreateOrUpdateSubnetPort failed", 0)
		patchesCreateOrUpdateSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
			func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string, isVmSubnetPort bool, restoreMode bool, interfaceIPType v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
				return nil, err
			})
		defer patchesCreateOrUpdateSubnetPort.Reset()
		patchesGetAddressBindingBySubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetAddressBindingBySubnetPort, func(_ *subnetport.SubnetPortService, _ *v1alpha1.SubnetPort) *v1alpha1.AddressBinding {
			return nil
		})
		defer patchesGetAddressBindingBySubnetPort.Reset()
		k8sClient.EXPECT().Status().Return(fakewriter)
		_, ret := r.Reconcile(ctx, req)
		assert.Equal(t, nil, ret)
	})

	// happy path
	t.Run("succeeded with create or update SubnetPort", func(t *testing.T) {
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Spec.Subnet = "subnet1"
				return nil
			}).Times(2)
		portIP := "1.2.3.4"
		portMac := "aa:bb:cc:dd"
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
		patchesCheckAndGetSubnetPathForSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).CheckAndGetSubnetPathForSubnetPort,
			func(r *SubnetPortReconciler, ctx context.Context, subnetPort *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
				return true, false, "", nil, nil, v1alpha1.IPAddressTypeIPv4, "", nil
			})
		defer patchesCheckAndGetSubnetPathForSubnetPort.Reset()
		patchesIsSharedSubnetPath := gomonkey.ApplyFunc(common.IsSharedSubnetPath, func(ctx context.Context, client client.Client, path string, ns string) (bool, error) {
			return false, nil
		})
		defer patchesIsSharedSubnetPath.Reset()
		patchesCreateOrUpdateSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
			func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string, isVmSubnetPort bool, restoreMode bool, interfaceIPType v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
				return portState, nil
			})
		defer patchesCreateOrUpdateSubnetPort.Reset()
		patchesSetAddressBindingStatus := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort,
			func(client client.Client, ctx context.Context, subnetPort *v1alpha1.SubnetPort, subnetPortService *subnetport.SubnetPortService) {
			})
		defer patchesSetAddressBindingStatus.Reset()
		patchesUpdateSubnetStatusOnSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).updateSubnetStatusOnSubnetPort,
			func(r *SubnetPortReconciler, subnetPort *v1alpha1.SubnetPort, nsxSubnet *model.VpcSubnet) error {
				return nil
			})
		defer patchesUpdateSubnetStatusOnSubnetPort.Reset()
		k8sClient.EXPECT().Status().Return(fakewriter)
		_, ret := r.Reconcile(ctx, req)
		assert.Equal(t, nil, ret)

		// happy path - update addressbindings day 2
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Spec.Subnet = "subnet1"
				v1sp.Spec.AddressBindings = []v1alpha1.PortAddressBinding{
					{
						IPAddress:  "1.2.3.5",
						MACAddress: "00:11:22:33:44:55",
					},
					{
						IPAddress:  "1.2.3.6",
						MACAddress: "00:11:22:33:44:55",
					},
				}
				return nil
			}).Times(2)
		portState3 := &model.SegmentPortState{
			RealizedBindings: []model.AddressBindingEntry{
				{Binding: &model.PacketAddressClassifier{IpAddress: ptr.To("1.2.3.5"), MacAddress: ptr.To("00:11:22:33:44:55")}},
				{Binding: &model.PacketAddressClassifier{IpAddress: ptr.To("1.2.3.6"), MacAddress: ptr.To("00:11:22:33:44:55")}},
			},
			Attachment: &model.SegmentPortAttachmentState{
				Id: &attachmentID,
			},
		}
		patchesCreateOrUpdateSubnetPort.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
			func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string, isVmSubnetPort bool, restoreMode bool, interfaceIPType v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
				return portState3, nil
			})
		k8sClient.EXPECT().Status().Return(fakewriter)
		_, ret = r.Reconcile(ctx, req)
		assert.Equal(t, nil, ret)

		// happy path - dhcp subnetport with mac only
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Spec.Subnet = "subnet1"
				v1sp.Spec.AddressBindings = []v1alpha1.PortAddressBinding{
					{
						MACAddress: "00:11:22:33:44:55",
					},
				}
				return nil
			}).Times(2)
		portState2 := &model.SegmentPortState{
			RealizedBindings: []model.AddressBindingEntry{},
			Attachment: &model.SegmentPortAttachmentState{
				Id: &attachmentID,
			},
		}
		patchesGetSubnetByPath.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetByPath",
			func(s *mock.MockSubnetServiceProvider, nsxSubnetPath string, sharedSubnet bool) (*model.VpcSubnet, error) {
				nsxSubnet := &model.VpcSubnet{
					Id:            ptr.To("subnet-1"),
					RealizationId: ptr.To("realization-1"),
					AdvancedConfig: &model.SubnetAdvancedConfig{
						StaticIpAllocation: &model.StaticIpAllocation{
							Enabled: servicecommon.Bool(false),
						},
					},
				}
				return nsxSubnet, nil
			})
		defer patchesGetSubnetByPath.Reset()
		patchesCreateOrUpdateSubnetPort.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
			func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string, isVmSubnetPort bool, restoreMode bool, interfaceIPType v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
				return portState2, nil
			})
		k8sClient.EXPECT().Status().Return(fakewriter)
		_, ret = r.Reconcile(ctx, req)
		assert.Equal(t, nil, ret)
	})

	// happy path - empty interfaceIPType for existing SubnetPort
	t.Run("succeeded with empty interfaceIPType for existing SubnetPort", func(t *testing.T) {
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Spec.Subnet = "subnet1"
				return nil
			}).Times(2)
		portState := &model.SegmentPortState{
			Attachment: &model.SegmentPortAttachmentState{
				Id: &attachmentID,
			},
		}
		patchesCheckAndGetSubnetPathForSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).CheckAndGetSubnetPathForSubnetPort,
			func(r *SubnetPortReconciler, ctx context.Context, subnetPort *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
				return true, false, "path1", nil, nil, "", "", nil
			})
		defer patchesCheckAndGetSubnetPathForSubnetPort.Reset()
		patchesIsSharedSubnetPath := gomonkey.ApplyFunc(common.IsSharedSubnetPath, func(ctx context.Context, client client.Client, path string, ns string) (bool, error) {
			return false, nil
		})
		defer patchesIsSharedSubnetPath.Reset()

		pGetSubnetCR := gomonkey.ApplyFunc((*SubnetPortReconciler).getSubnetCR,
			func(r *SubnetPortReconciler, ctx context.Context, subnetPort *v1alpha1.SubnetPort) (*v1alpha1.Subnet, bool, error) {
				return &v1alpha1.Subnet{
					Spec: v1alpha1.SubnetSpec{
						IPAddressType: v1alpha1.IPAddressTypeIPv6,
					},
				}, false, nil
			})
		defer pGetSubnetCR.Reset()

		pGetSubnetByPath := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetByPath",
			func(s *mock.MockSubnetServiceProvider, nsxSubnetPath string, sharedSubnet bool) (*model.VpcSubnet, error) {
				nsxSubnet := &model.VpcSubnet{
					Id:            ptr.To("subnet-1"),
					RealizationId: ptr.To("realization-1"),
				}
				return nsxSubnet, nil
			})
		defer pGetSubnetByPath.Reset()

		var capturedIPTypeInUpdate v1alpha1.IPAddressType
		pUpdateSubnetPortIPType := gomonkey.ApplyFunc((*SubnetPortReconciler).updateSubnetPortIPType,
			func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort, interfaceIPType v1alpha1.IPAddressType, _ *model.VpcSubnet) error {
				capturedIPTypeInUpdate = interfaceIPType
				return nil
			})
		defer pUpdateSubnetPortIPType.Reset()

		var capturedIPTypeInCreate v1alpha1.IPAddressType
		patchesCreateOrUpdateSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
			func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string, isVmSubnetPort bool, restoreMode bool, interfaceIPType v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
				capturedIPTypeInCreate = interfaceIPType
				return portState, nil
			})
		defer patchesCreateOrUpdateSubnetPort.Reset()

		patchesSetAddressBindingStatus := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort,
			func(client client.Client, ctx context.Context, subnetPort *v1alpha1.SubnetPort, subnetPortService *subnetport.SubnetPortService) {
			})
		defer patchesSetAddressBindingStatus.Reset()
		patchesUpdateSubnetStatusOnSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).updateSubnetStatusOnSubnetPort,
			func(r *SubnetPortReconciler, subnetPort *v1alpha1.SubnetPort, nsxSubnet *model.VpcSubnet) error {
				return nil
			})
		defer patchesUpdateSubnetStatusOnSubnetPort.Reset()
		k8sClient.EXPECT().Status().Return(fakewriter)

		_, ret := r.Reconcile(ctx, req)
		assert.Equal(t, nil, ret)
		assert.Equal(t, v1alpha1.IPAddressTypeIPv6, capturedIPTypeInUpdate)
		assert.Equal(t, v1alpha1.IPAddressTypeIPv6, capturedIPTypeInCreate)

		// Test IPv4_IPV6 defaulting to IPv4
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Spec.Subnet = "subnet1"
				return nil
			}).Times(2)
		pGetSubnetCRDual := gomonkey.ApplyFunc((*SubnetPortReconciler).getSubnetCR,
			func(r *SubnetPortReconciler, ctx context.Context, subnetPort *v1alpha1.SubnetPort) (*v1alpha1.Subnet, bool, error) {
				return &v1alpha1.Subnet{
					Spec: v1alpha1.SubnetSpec{
						IPAddressType: v1alpha1.IPAddressTypeIPv4IPv6,
					},
				}, false, nil
			})
		defer pGetSubnetCRDual.Reset()
		k8sClient.EXPECT().Status().Return(fakewriter)

		_, ret = r.Reconcile(ctx, req)
		assert.Equal(t, nil, ret)
		assert.Equal(t, v1alpha1.IPAddressTypeIPv4, capturedIPTypeInUpdate)
		assert.Equal(t, v1alpha1.IPAddressTypeIPv4, capturedIPTypeInCreate)

		// Test IPv4 Subnet parent CR
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Spec.Subnet = "subnet1"
				v1sp.Spec.InterfaceIPType = v1alpha1.IPAddressTypeIPv4
				return nil
			}).Times(2)
		pGetSubnetCRIPv4 := gomonkey.ApplyFunc((*SubnetPortReconciler).getSubnetCR,
			func(r *SubnetPortReconciler, ctx context.Context, subnetPort *v1alpha1.SubnetPort) (*v1alpha1.Subnet, bool, error) {
				return &v1alpha1.Subnet{
					Spec: v1alpha1.SubnetSpec{
						IPAddressType: v1alpha1.IPAddressTypeIPv4,
					},
				}, false, nil
			})
		defer pGetSubnetCRIPv4.Reset()
		k8sClient.EXPECT().Status().Return(fakewriter)

		_, ret = r.Reconcile(ctx, req)
		assert.Equal(t, nil, ret)
		assert.Equal(t, v1alpha1.IPAddressTypeIPv4, capturedIPTypeInUpdate)
		assert.Equal(t, v1alpha1.IPAddressTypeIPv4, capturedIPTypeInCreate)
	})

	// handle deletion event - delete NSX subnet port failed
	t.Run("failed to delete NSX VpcSubnetPort", func(t *testing.T) {
		sp := &v1alpha1.SubnetPort{}
		k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Spec.Subnet = "subnet1"
				time := metav1.Now()
				v1sp.ObjectMeta.DeletionTimestamp = &time
				return nil
			})
		err := errors.New("DeleteSubnetPort failed")
		patchesGetByUID := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
			func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
				return &model.VpcSubnetPort{Id: servicecommon.String("port1")}, nil
			})
		defer patchesGetByUID.Reset()
		patchesDeleteSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPort,
			func(s *subnetport.SubnetPortService, port *model.VpcSubnetPort) error {
				return err
			})
		defer patchesDeleteSubnetPort.Reset()
		patchesCreateOrUpdateSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
			func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string, isVmSubnetPort bool, restoreMode bool, interfaceIPType v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
				assert.FailNow(t, "should not be called")
				return nil, nil
			})
		defer patchesCreateOrUpdateSubnetPort.Reset()
		_, ret := r.Reconcile(ctx, req)
		assert.Equal(t, err, ret)
	})

	// handle deletion event - successfully deleted
	t.Run("successfully delete NSX VpcSubnetPort", func(t *testing.T) {
		sp := &v1alpha1.SubnetPort{}
		k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Spec.Subnet = "subnet1"
				time := metav1.Now()
				v1sp.ObjectMeta.DeletionTimestamp = &time
				return nil
			})
		patchesGetByUID := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
			func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
				return &model.VpcSubnetPort{Id: servicecommon.String("port1")}, nil
			})
		defer patchesGetByUID.Reset()
		patchesDeleteSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPort,
			func(s *subnetport.SubnetPortService, port *model.VpcSubnetPort) error {
				return nil
			})
		defer patchesDeleteSubnetPort.Reset()
		_, ret := r.Reconcile(ctx, req)
		assert.Equal(t, nil, ret)
	})

	attachmentIDAfterRestore := "attachment-id-2"
	subnetPort := &v1alpha1.SubnetPort{
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: attachmentID,
			},
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{
						Gateway:   "10.0.0.1",
						IPAddress: "10.0.0.3/28",
					},
				},
				MACAddress: "04:50:56:00:74:00",
			},
			Conditions: []v1alpha1.Condition{
				{
					Type:    v1alpha1.Ready,
					Status:  corev1.ConditionTrue,
					Message: "NSX subnet port has been successfully created/updated",
					Reason:  "SubnetPortReady",
				},
			},
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "subnetport-1",
			Namespace: "ns-1",
			Labels:    map[string]string{servicecommon.LabelCPVM: "true"},
		},
	}
	portState := &model.SegmentPortState{
		RealizedBindings: []model.AddressBindingEntry{
			{
				Binding: &model.PacketAddressClassifier{
					IpAddress:  servicecommon.String("10.0.0.3"),
					MacAddress: servicecommon.String("04:50:56:00:74:00"),
				},
			},
		},
		Attachment: &model.SegmentPortAttachmentState{
			Id: &attachmentIDAfterRestore,
		},
	}
	// Restore case
	t.Run("restore", func(t *testing.T) {
		sp := &v1alpha1.SubnetPort{}
		r.restoreMode = true
		k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Status = subnetPort.Status
				v1sp.ObjectMeta = subnetPort.ObjectMeta
				return nil
			}).Times(2)
		patchesCheckAndGetSubnetPathForSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).CheckAndGetSubnetPathForSubnetPort,
			func(r *SubnetPortReconciler, ctx context.Context, subnetPort *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
				return true, false, "", nil, nil, v1alpha1.IPAddressTypeIPv4, "", nil
			})
		defer patchesCheckAndGetSubnetPathForSubnetPort.Reset()
		patchesGetSubnetByPath := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetByPath",
			func(s *mock.MockSubnetServiceProvider, nsxSubnetPath string, sharedSubnet bool) (*model.VpcSubnet, error) {
				nsxSubnet := &model.VpcSubnet{
					Id:            ptr.To("subnet-1"),
					RealizationId: ptr.To("realization-1"),
					AdvancedConfig: &model.SubnetAdvancedConfig{
						StaticIpAllocation: &model.StaticIpAllocation{
							Enabled: servicecommon.Bool(true),
						},
					},
				}
				return nsxSubnet, nil
			})
		defer patchesGetSubnetByPath.Reset()
		patchesIsSharedSubnetPath := gomonkey.ApplyFunc(common.IsSharedSubnetPath, func(ctx context.Context, client client.Client, path string, ns string) (bool, error) {
			return false, nil
		})
		defer patchesIsSharedSubnetPath.Reset()

		patchesCreateOrUpdateSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
			func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string, isVmSubnetPort bool, restoreMode bool, interfaceIPType v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
				return portState, nil
			})
		defer patchesCreateOrUpdateSubnetPort.Reset()
		k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Status = subnetPort.Status
				v1sp.Status.Attachment.ID = attachmentIDAfterRestore
				v1sp.ObjectMeta = subnetPort.ObjectMeta
				return nil
			})
		patchesSetAddressBindingStatus := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort,
			func(client client.Client, ctx context.Context, subnetPort *v1alpha1.SubnetPort, subnetPortService *subnetport.SubnetPortService) {
			})
		defer patchesSetAddressBindingStatus.Reset()
		patchesUpdateSubnetStatusOnSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).updateSubnetStatusOnSubnetPort,
			func(r *SubnetPortReconciler, subnetPort *v1alpha1.SubnetPort, nsxSubnet *model.VpcSubnet) error {
				return nil
			})
		defer patchesUpdateSubnetStatusOnSubnetPort.Reset()
		patchesRestoreVifFeatureEnabled := gomonkey.ApplyFunc(nsx.RestoreVifFeatureEnabled, func(_ *nsx.Client, _ *config.NSXOperatorConfig) bool {
			return false
		})
		defer patchesRestoreVifFeatureEnabled.Reset()

		k8sClient.EXPECT().Status().Return(fakewriter)
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Status = subnetPort.Status
				v1sp.ObjectMeta = subnetPort.ObjectMeta
				return nil
			})
		k8sClient.EXPECT().Update(gomock.Any(), gomock.Any()).Do(func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
			assert.Equal(t, "cpvm", obj.GetAnnotations()[servicecommon.AnnotationReconfigureNic])
			return nil
		})
		_, ret := r.Reconcile(ctx, req)
		assert.Equal(t, nil, ret)
	})

	portStateNoIP := &model.SegmentPortState{
		Attachment: &model.SegmentPortAttachmentState{
			Id: &attachmentIDAfterRestore,
		},
	}
	// Restore case without IP/MAC in state
	t.Run("restore without IP in state", func(t *testing.T) {
		r.restoreMode = true

		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Status = subnetPort.Status
				v1sp.ObjectMeta = subnetPort.ObjectMeta
				return nil
			}).Times(2)
		patchesCheckAndGetSubnetPathForSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).CheckAndGetSubnetPathForSubnetPort,
			func(r *SubnetPortReconciler, ctx context.Context, subnetPort *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
				return true, false, "", nil, nil, v1alpha1.IPAddressTypeIPv4, "", nil
			})
		defer patchesCheckAndGetSubnetPathForSubnetPort.Reset()
		patchesGetSubnetByIP := gomonkey.ApplyFunc((*SubnetPortReconciler).getSubnetBySubnetPort, func(r *SubnetPortReconciler, subnetPort *v1alpha1.SubnetPort) (string, error) {
			return "subnet-path", nil
		})
		defer patchesGetSubnetByIP.Reset()
		patchesGetSubnetByPath := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetByPath",
			func(s *mock.MockSubnetServiceProvider, nsxSubnetPath string, sharedSubnet bool) (*model.VpcSubnet, error) {
				nsxSubnet := &model.VpcSubnet{
					Id:            ptr.To("subnet-1"),
					RealizationId: ptr.To("realization-1"),
					AdvancedConfig: &model.SubnetAdvancedConfig{
						StaticIpAllocation: &model.StaticIpAllocation{
							Enabled: servicecommon.Bool(true),
						},
					},
				}
				return nsxSubnet, nil
			})
		defer patchesGetSubnetByPath.Reset()
		patchesIsSharedSubnetPath := gomonkey.ApplyFunc(common.IsSharedSubnetPath, func(ctx context.Context, client client.Client, path string, ns string) (bool, error) {
			return false, nil
		})
		defer patchesIsSharedSubnetPath.Reset()
		patchesCreateOrUpdateSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
			func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string, isVmSubnetPort bool, restoreMode bool, interfaceIPType v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
				return portStateNoIP, nil
			})
		defer patchesCreateOrUpdateSubnetPort.Reset()
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Status = subnetPort.Status
				v1sp.Status.Attachment.ID = attachmentIDAfterRestore
				v1sp.ObjectMeta = subnetPort.ObjectMeta
				return nil
			})
		patchesSetAddressBindingStatus := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort,
			func(client client.Client, ctx context.Context, subnetPort *v1alpha1.SubnetPort, subnetPortService *subnetport.SubnetPortService) {
			})
		defer patchesSetAddressBindingStatus.Reset()
		patchesUpdateSubnetStatusOnSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).updateSubnetStatusOnSubnetPort,
			func(r *SubnetPortReconciler, subnetPort *v1alpha1.SubnetPort, nsxSubnet *model.VpcSubnet) error {
				return nil
			})
		defer patchesUpdateSubnetStatusOnSubnetPort.Reset()
		patchesNSXCheckVersion := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
			return false
		})
		defer patchesNSXCheckVersion.Reset()

		k8sClient.EXPECT().Status().Return(fakewriter)
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Status = subnetPort.Status
				v1sp.ObjectMeta = subnetPort.ObjectMeta
				return nil
			})
		k8sClient.EXPECT().Update(gomock.Any(), gomock.Any()).Do(func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
			assert.Equal(t, "cpvm", obj.GetAnnotations()[servicecommon.AnnotationReconfigureNic])
			return nil
		})
		_, ret := r.Reconcile(ctx, req)
		assert.Equal(t, nil, ret)
	})

	dhcpSubnetPort := &v1alpha1.SubnetPort{
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: attachmentID,
			},
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{
						Gateway: "10.0.0.1",
					},
				},
				MACAddress: "04:50:56:00:74:00",
			},
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "subnetport-1",
			Namespace:   "ns-1",
			Annotations: map[string]string{servicecommon.AnnotationAttachmentRef: "VirtualMachine/vm-1/eth0"},
		},
	}
	dhcpPortState := &model.SegmentPortState{
		Attachment: &model.SegmentPortAttachmentState{
			Id: &attachmentIDAfterRestore,
		},
	}
	// Restore case with dhcp SubnetPort
	t.Run("restore dhcp", func(t *testing.T) {
		sp := &v1alpha1.SubnetPort{}
		vm := &vmv1alpha1.VirtualMachine{}
		r.restoreMode = true
		k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Status = dhcpSubnetPort.Status
				v1sp.ObjectMeta = dhcpSubnetPort.ObjectMeta
				return nil
			}).Times(2)

		patchesCheckAndGetSubnetPathForSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).CheckAndGetSubnetPathForSubnetPort,
			func(r *SubnetPortReconciler, ctx context.Context, subnetPort *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
				return true, false, "", nil, nil, v1alpha1.IPAddressTypeIPv4, "", nil
			})
		defer patchesCheckAndGetSubnetPathForSubnetPort.Reset()
		patchesGetSubnetByIP := gomonkey.ApplyFunc((*SubnetPortReconciler).getSubnetBySubnetPort, func(r *SubnetPortReconciler, subnetPort *v1alpha1.SubnetPort) (string, error) {
			return "subnet-path", nil
		})
		k8sClient.EXPECT().Get(ctx, gomock.Any(), vm).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1vm := obj.(*vmv1alpha1.VirtualMachine)
				v1vm.ObjectMeta = metav1.ObjectMeta{
					Name:      "vm-1",
					Namespace: "ns-1",
				}
				return nil
			})
		defer patchesGetSubnetByIP.Reset()
		patchesGetSubnetByPath := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetByPath",
			func(s *mock.MockSubnetServiceProvider, nsxSubnetPath string, sharedSubnet bool) (*model.VpcSubnet, error) {
				nsxSubnet := &model.VpcSubnet{
					Id:            ptr.To("subnet-1"),
					RealizationId: ptr.To("realization-1"),
					AdvancedConfig: &model.SubnetAdvancedConfig{
						StaticIpAllocation: &model.StaticIpAllocation{
							Enabled: servicecommon.Bool(false),
						},
					},
				}
				return nsxSubnet, nil
			})
		defer patchesGetSubnetByPath.Reset()
		patchesIsSharedSubnetPath := gomonkey.ApplyFunc(common.IsSharedSubnetPath, func(ctx context.Context, client client.Client, path string, ns string) (bool, error) {
			return false, nil
		})
		defer patchesIsSharedSubnetPath.Reset()
		patchesCreateOrUpdateSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
			func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string, isVmSubnetPort bool, restoreMode bool, interfaceIPType v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
				return dhcpPortState, nil
			})
		defer patchesCreateOrUpdateSubnetPort.Reset()
		patchesSetAddressBindingStatus := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort,
			func(client client.Client, ctx context.Context, subnetPort *v1alpha1.SubnetPort, subnetPortService *subnetport.SubnetPortService) {
			})
		defer patchesSetAddressBindingStatus.Reset()
		patchesUpdateSubnetStatusOnSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).updateSubnetStatusOnSubnetPort,
			func(r *SubnetPortReconciler, subnetPort *v1alpha1.SubnetPort, nsxSubnet *model.VpcSubnet) error {
				return nil
			})
		defer patchesUpdateSubnetStatusOnSubnetPort.Reset()

		patchesGetAddressBindingBySubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetAddressBindingBySubnetPort, func(_ *subnetport.SubnetPortService, _ *v1alpha1.SubnetPort) *v1alpha1.AddressBinding {
			return nil
		})
		defer patchesGetAddressBindingBySubnetPort.Reset()

		patchesRestoreVifFeatureEnabled := gomonkey.ApplyFunc(nsx.RestoreVifFeatureEnabled, func(_ *nsx.Client, _ *config.NSXOperatorConfig) bool {
			return false
		})
		defer patchesRestoreVifFeatureEnabled.Reset()

		k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Status = dhcpSubnetPort.Status
				v1sp.Status.Attachment.ID = attachmentIDAfterRestore
				v1sp.ObjectMeta = dhcpSubnetPort.ObjectMeta
				return nil
			})
		k8sClient.EXPECT().Status().Return(fakewriter)
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				return nil
			})
		k8sClient.EXPECT().Update(gomock.Any(), gomock.Any()).Do(func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
			assert.Equal(t, "eth0", obj.GetAnnotations()[servicecommon.AnnotationReconfigureNic])
			return nil
		})
		_, ret := r.Reconcile(ctx, req)
		assert.Equal(t, nil, ret)
	})

	// Restore case with update failure
	t.Run("restore with update failure", func(t *testing.T) {
		r.restoreMode = true

		// Return original attachment ID to trigger failure mismatch inside APIReader checker
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Status = subnetPort.Status
				v1sp.ObjectMeta = subnetPort.ObjectMeta
				return nil
			}).Times(2)
		patchesCheckAndGetSubnetPathForSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).CheckAndGetSubnetPathForSubnetPort,
			func(r *SubnetPortReconciler, ctx context.Context, subnetPort *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
				return true, false, "", nil, nil, v1alpha1.IPAddressTypeIPv4, "", nil
			})
		defer patchesCheckAndGetSubnetPathForSubnetPort.Reset()
		patchesGetSubnetByIP := gomonkey.ApplyFunc((*SubnetPortReconciler).getSubnetBySubnetPort, func(r *SubnetPortReconciler, subnetPort *v1alpha1.SubnetPort) (string, error) {
			return "subnet-path", nil
		})
		defer patchesGetSubnetByIP.Reset()

		patchesIsSharedSubnetPath := gomonkey.ApplyFunc(common.IsSharedSubnetPath, func(ctx context.Context, client client.Client, path string, ns string) (bool, error) {
			return false, nil
		})
		defer patchesIsSharedSubnetPath.Reset()
		patchesGetSubnetByPath := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetByPath",
			func(s *mock.MockSubnetServiceProvider, nsxSubnetPath string, sharedSubnet bool) (*model.VpcSubnet, error) {
				nsxSubnet := &model.VpcSubnet{
					Id:            ptr.To("subnet-1"),
					RealizationId: ptr.To("realization-1"),
					AdvancedConfig: &model.SubnetAdvancedConfig{
						StaticIpAllocation: &model.StaticIpAllocation{
							Enabled: servicecommon.Bool(true),
						},
					},
				}
				return nsxSubnet, nil
			})
		defer patchesGetSubnetByPath.Reset()
		patchesCreateOrUpdateSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
			func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string, isVmSubnetPort bool, restoreMode bool, interfaceIPType v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
				return portState, nil
			})
		defer patchesCreateOrUpdateSubnetPort.Reset()
		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Status = subnetPort.Status
				v1sp.ObjectMeta = subnetPort.ObjectMeta
				return nil
			})
		patchesSetAddressBindingStatus := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort,
			func(client client.Client, ctx context.Context, subnetPort *v1alpha1.SubnetPort, subnetPortService *subnetport.SubnetPortService) {
			})
		defer patchesSetAddressBindingStatus.Reset()
		patchesUpdateSubnetStatusOnSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).updateSubnetStatusOnSubnetPort,
			func(r *SubnetPortReconciler, subnetPort *v1alpha1.SubnetPort, nsxSubnet *model.VpcSubnet) error {
				return nil
			})
		defer patchesUpdateSubnetStatusOnSubnetPort.Reset()

		patchesRestoreVifFeatureEnabled := gomonkey.ApplyFunc(nsx.RestoreVifFeatureEnabled, func(_ *nsx.Client, _ *config.NSXOperatorConfig) bool {
			return false
		})
		defer patchesRestoreVifFeatureEnabled.Reset()

		k8sClient.EXPECT().Status().Return(fakewriter)
		_, ret := r.Reconcile(ctx, req)
		assert.Equal(t, fmt.Errorf("SubnetPort Attachment ID is not updated"), ret)
	})

	t.Run("Static IP missing realized bindings", func(t *testing.T) {
		r.restoreMode = false

		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				v1sp := obj.(*v1alpha1.SubnetPort)
				v1sp.Spec.Subnet = "subnet1"
				v1sp.Spec.StaticIPAllocationType = "Static"
				return nil
			}).Times(2)
		k8sClient.EXPECT().Status().Return(fakewriter)

		portStateLocal := &model.SegmentPortState{
			RealizedBindings: []model.AddressBindingEntry{},
			Attachment: &model.SegmentPortAttachmentState{
				Id: &attachmentID,
			},
		}

		patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort, func(_ *subnetport.SubnetPortService, _ interface{}, _ *model.VpcSubnet, _ string, _ *map[string]string, _ bool, _ bool, _ v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
			return portStateLocal, nil
		})
		defer patches.Reset()

		patches2 := gomonkey.ApplyFunc((*subnetport.SubnetPortService).GetAddressBindingBySubnetPort, func(_ *subnetport.SubnetPortService, _ *v1alpha1.SubnetPort) *v1alpha1.AddressBinding {
			return nil
		})
		defer patches2.Reset()

		patchesIPAllocCreate := gomonkey.ApplyMethod(reflect.TypeOf(r.IpAddressAllocationService), "CreateIPAddressAllocationForAddressBinding", func(_ *mock.MockIPAddressAllocationProvider, _ *v1alpha1.AddressBinding, _ *v1alpha1.SubnetPort, _ bool) error {
			return nil
		})
		defer patchesIPAllocCreate.Reset()

		patchesIPAllocDelete := gomonkey.ApplyMethod(reflect.TypeOf(r.IpAddressAllocationService), "DeleteIPAddressAllocationForAddressBinding", func(_ *mock.MockIPAddressAllocationProvider, _ metav1.Object) error {
			return nil
		})
		defer patchesIPAllocDelete.Reset()

		patches4 := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetByPath", func(_ *mock.MockSubnetServiceProvider, _ string, _ bool) (*model.VpcSubnet, error) {
			return &model.VpcSubnet{
				AdvancedConfig: &model.SubnetAdvancedConfig{
					StaticIpAllocation: &model.StaticIpAllocation{
						Enabled: ptr.To(true),
					},
				},
			}, nil
		})
		defer patches4.Reset()

		patches5 := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getSubnetCR", func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (*v1alpha1.Subnet, error) {
			return &v1alpha1.Subnet{}, nil
		})
		defer patches5.Reset()

		patches6 := gomonkey.ApplyMethod(reflect.TypeOf(r), "CheckAndGetSubnetPathForSubnetPort", func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
			return true, false, "/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1", nil, nil, "IPv4", "", nil
		})
		defer patches6.Reset()

		patchesIsSharedSubnetPath := gomonkey.ApplyFunc(common.IsSharedSubnetPath, func(ctx context.Context, client client.Client, path string, ns string) (bool, error) {
			return false, nil
		})
		defer patchesIsSharedSubnetPath.Reset()

		patchesUpdateSubnetStatus := gomonkey.ApplyFunc((*SubnetPortReconciler).updateSubnetStatusOnSubnetPort,
			func(r *SubnetPortReconciler, subnetPort *v1alpha1.SubnetPort, nsxSubnet *model.VpcSubnet) error {
				return nil
			})
		defer patchesUpdateSubnetStatus.Reset()

		patchesSetAddressBindingStatus := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort,
			func(client client.Client, ctx context.Context, subnetPort *v1alpha1.SubnetPort, subnetPortService *subnetport.SubnetPortService, transitionTime metav1.Time, e error) {
			})
		defer patchesSetAddressBindingStatus.Reset()

		_, err := r.Reconcile(ctx, req)
		assert.NotNil(t, err)
		assert.Equal(t, "IP and MAC are missing for SubnetPort", err.Error())
	})

	t.Run("Preserve IPv6 InterfaceIPType on dual-stack Subnet during reconcile", func(t *testing.T) {
		r.restoreMode = false

		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				if v1sp, ok := obj.(*v1alpha1.SubnetPort); ok {
					v1sp.Spec.Subnet = "subnet1"
					v1sp.Spec.InterfaceIPType = v1alpha1.IPAddressTypeIPv6
				}
				return nil
			}).Times(2)
		k8sClient.EXPECT().Status().Return(fakewriter)

		pGetSubnetCRDual := gomonkey.ApplyFunc((*SubnetPortReconciler).getSubnetCR,
			func(r *SubnetPortReconciler, ctx context.Context, subnetPort *v1alpha1.SubnetPort) (*v1alpha1.Subnet, bool, error) {
				return &v1alpha1.Subnet{
					Spec: v1alpha1.SubnetSpec{
						IPAddressType: v1alpha1.IPAddressTypeIPv4IPv6,
					},
				}, false, nil
			})
		defer pGetSubnetCRDual.Reset()

		portStateLocal := &model.SegmentPortState{
			Attachment: &model.SegmentPortAttachmentState{
				Id: &attachmentID,
			},
		}

		var passedInterfaceIPType v1alpha1.IPAddressType
		patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
			func(_ *subnetport.SubnetPortService, _ interface{}, _ *model.VpcSubnet, _ string, _ *map[string]string, _ bool, _ bool, ifType v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
				passedInterfaceIPType = ifType
				return portStateLocal, nil
			})
		defer patches.Reset()

		ipAllocCreateCalled := false
		patchesIPAllocCreate := gomonkey.ApplyMethod(reflect.TypeOf(r.IpAddressAllocationService), "CreateIPAddressAllocationForAddressBinding",
			func(_ *mock.MockIPAddressAllocationProvider, _ *v1alpha1.AddressBinding, _ *v1alpha1.SubnetPort, _ bool) error {
				ipAllocCreateCalled = true
				return nil
			})
		defer patchesIPAllocCreate.Reset()

		patches4 := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetByPath", func(_ *mock.MockSubnetServiceProvider, _ string, _ bool) (*model.VpcSubnet, error) {
			return &model.VpcSubnet{
				IpAddressType: ptr.To("IPV4_IPV6"),
			}, nil
		})
		defer patches4.Reset()

		// Simulate existing port where CheckAndGetSubnetPathForSubnetPort returns empty interfaceIPType
		patches6 := gomonkey.ApplyMethod(reflect.TypeOf(r), "CheckAndGetSubnetPathForSubnetPort", func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
			return true, false, "/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1", nil, nil, "", "", nil
		})
		defer patches6.Reset()

		patchesIsSharedSubnetPath := gomonkey.ApplyFunc(common.IsSharedSubnetPath, func(ctx context.Context, client client.Client, path string, ns string) (bool, error) {
			return false, nil
		})
		defer patchesIsSharedSubnetPath.Reset()

		patchesUpdateSubnetStatus := gomonkey.ApplyFunc((*SubnetPortReconciler).updateSubnetStatusOnSubnetPort,
			func(r *SubnetPortReconciler, subnetPort *v1alpha1.SubnetPort, nsxSubnet *model.VpcSubnet) error {
				return nil
			})
		defer patchesUpdateSubnetStatus.Reset()

		patchesSetAddressBindingStatus := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort,
			func(client client.Client, ctx context.Context, subnetPort *v1alpha1.SubnetPort, subnetPortService *subnetport.SubnetPortService, transitionTime metav1.Time, e error) {
			})
		defer patchesSetAddressBindingStatus.Reset()

		_, err := r.Reconcile(ctx, req)
		assert.NoError(t, err)
		assert.Equal(t, v1alpha1.IPAddressTypeIPv6, passedInterfaceIPType)
		assert.False(t, ipAllocCreateCalled, "CreateIPAddressAllocationForAddressBinding should not be called for IPv6 interface")
	})

	t.Run("Preserve IPv6 InterfaceIPType on dual-stack SubnetSet where NSX Subnet is IPv4 during reconcile", func(t *testing.T) {
		r.restoreMode = false

		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, key client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				if v1sp, ok := obj.(*v1alpha1.SubnetPort); ok {
					v1sp.Spec.SubnetSet = "subnetset1"
					v1sp.Spec.InterfaceIPType = v1alpha1.IPAddressTypeIPv6
					return nil
				}
				if ss, ok := obj.(*v1alpha1.SubnetSet); ok {
					ss.Name = "subnetset1"
					ss.Spec.IPAddressType = v1alpha1.IPAddressTypeIPv4IPv6
					return nil
				}
				return nil
			}).Times(3)
		k8sClient.EXPECT().Status().Return(fakewriter)

		portStateLocal := &model.SegmentPortState{
			Attachment: &model.SegmentPortAttachmentState{
				Id: &attachmentID,
			},
		}

		var passedInterfaceIPType v1alpha1.IPAddressType
		patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
			func(_ *subnetport.SubnetPortService, _ interface{}, _ *model.VpcSubnet, _ string, _ *map[string]string, _ bool, _ bool, ifType v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
				passedInterfaceIPType = ifType
				return portStateLocal, nil
			})
		defer patches.Reset()

		// NSX Subnet only has IPV4 (or nil) in this allocated subnet
		patches4 := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetByPath", func(_ *mock.MockSubnetServiceProvider, _ string, _ bool) (*model.VpcSubnet, error) {
			return &model.VpcSubnet{
				IpAddressType: ptr.To("IPV4"),
			}, nil
		})
		defer patches4.Reset()

		// Simulate existing port where CheckAndGetSubnetPathForSubnetPort returns empty interfaceIPType
		patches6 := gomonkey.ApplyMethod(reflect.TypeOf(r), "CheckAndGetSubnetPathForSubnetPort", func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
			return true, false, "/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1", nil, nil, "", "", nil
		})
		defer patches6.Reset()

		patchesIsSharedSubnetPath := gomonkey.ApplyFunc(common.IsSharedSubnetPath, func(ctx context.Context, client client.Client, path string, ns string) (bool, error) {
			return false, nil
		})
		defer patchesIsSharedSubnetPath.Reset()

		patchesUpdateSubnetStatus := gomonkey.ApplyFunc((*SubnetPortReconciler).updateSubnetStatusOnSubnetPort,
			func(r *SubnetPortReconciler, subnetPort *v1alpha1.SubnetPort, nsxSubnet *model.VpcSubnet) error {
				return nil
			})
		defer patchesUpdateSubnetStatus.Reset()

		patchesSetAddressBindingStatus := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort,
			func(client client.Client, ctx context.Context, subnetPort *v1alpha1.SubnetPort, subnetPortService *subnetport.SubnetPortService, transitionTime metav1.Time, e error) {
			})
		defer patchesSetAddressBindingStatus.Reset()

		_, err := r.Reconcile(ctx, req)
		assert.NoError(t, err)
		assert.Equal(t, v1alpha1.IPAddressTypeIPv6, passedInterfaceIPType)
	})

	t.Run("Reconcile existing IPv4 SubnetPort on Subnet with nil IpAddressType", func(t *testing.T) {
		r.restoreMode = false

		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				if v1sp, ok := obj.(*v1alpha1.SubnetPort); ok {
					v1sp.Spec.Subnet = "subnet1"
					v1sp.Spec.InterfaceIPType = v1alpha1.IPAddressTypeIPv4
				}
				return nil
			}).Times(2)
		k8sClient.EXPECT().Status().Return(fakewriter)

		portStateLocal := &model.SegmentPortState{
			Attachment: &model.SegmentPortAttachmentState{
				Id: &attachmentID,
			},
		}

		var passedInterfaceIPType v1alpha1.IPAddressType
		patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
			func(_ *subnetport.SubnetPortService, _ interface{}, _ *model.VpcSubnet, _ string, _ *map[string]string, _ bool, _ bool, ifType v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
				passedInterfaceIPType = ifType
				return portStateLocal, nil
			})
		defer patches.Reset()

		patches4 := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetByPath", func(_ *mock.MockSubnetServiceProvider, _ string, _ bool) (*model.VpcSubnet, error) {
			return &model.VpcSubnet{
				IpAddressType: nil,
			}, nil
		})
		defer patches4.Reset()

		// Simulate existing port where CheckAndGetSubnetPathForSubnetPort returns empty interfaceIPType
		patches6 := gomonkey.ApplyMethod(reflect.TypeOf(r), "CheckAndGetSubnetPathForSubnetPort", func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
			return true, false, "/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1", nil, nil, "", "", nil
		})
		defer patches6.Reset()

		patchesIsSharedSubnetPath := gomonkey.ApplyFunc(common.IsSharedSubnetPath, func(ctx context.Context, client client.Client, path string, ns string) (bool, error) {
			return false, nil
		})
		defer patchesIsSharedSubnetPath.Reset()

		patchesUpdateSubnetStatus := gomonkey.ApplyFunc((*SubnetPortReconciler).updateSubnetStatusOnSubnetPort,
			func(r *SubnetPortReconciler, subnetPort *v1alpha1.SubnetPort, nsxSubnet *model.VpcSubnet) error {
				return nil
			})
		defer patchesUpdateSubnetStatus.Reset()

		patchesSetAddressBindingStatus := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort,
			func(client client.Client, ctx context.Context, subnetPort *v1alpha1.SubnetPort, subnetPortService *subnetport.SubnetPortService, transitionTime metav1.Time, e error) {
			})
		defer patchesSetAddressBindingStatus.Reset()

		_, err := r.Reconcile(ctx, req)
		assert.NoError(t, err)
		assert.Equal(t, v1alpha1.IPAddressTypeIPv4, passedInterfaceIPType)
	})

	t.Run("Preserve IPv4 InterfaceIPType on default SubnetSet during reconcile", func(t *testing.T) {
		r.restoreMode = false

		k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
			func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				if v1sp, ok := obj.(*v1alpha1.SubnetPort); ok {
					v1sp.Spec.Subnet = ""
					v1sp.Spec.SubnetSet = ""
					v1sp.Spec.InterfaceIPType = v1alpha1.IPAddressTypeIPv4
				}
				return nil
			}).Times(2)
		k8sClient.EXPECT().Status().Return(fakewriter).Times(1)

		patchesDefaultSubnetSet := gomonkey.ApplyFunc(common.GetDefaultSubnetSetByNamespace,
			func(client client.Client, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
				return &v1alpha1.SubnetSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default-vm-network",
						Namespace: namespace,
					},
					Spec: v1alpha1.SubnetSetSpec{
						IPAddressType: v1alpha1.IPAddressTypeIPv4IPv6,
					},
				}, nil
			})
		defer patchesDefaultSubnetSet.Reset()

		portStateLocal := &model.SegmentPortState{
			Attachment: &model.SegmentPortAttachmentState{
				Id: &attachmentID,
			},
		}

		var passedInterfaceIPType v1alpha1.IPAddressType
		patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
			func(_ *subnetport.SubnetPortService, _ interface{}, _ *model.VpcSubnet, _ string, _ *map[string]string, _ bool, _ bool, ifType v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
				passedInterfaceIPType = ifType
				return portStateLocal, nil
			})
		defer patches.Reset()

		patches4 := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetByPath", func(_ *mock.MockSubnetServiceProvider, _ string, _ bool) (*model.VpcSubnet, error) {
			return &model.VpcSubnet{
				IpAddressType: ptr.To("IPV4"),
			}, nil
		})
		defer patches4.Reset()

		// Simulate existing port where CheckAndGetSubnetPathForSubnetPort returns empty interfaceIPType
		patches6 := gomonkey.ApplyMethod(reflect.TypeOf(r), "CheckAndGetSubnetPathForSubnetPort", func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
			return true, false, "/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1", nil, nil, "", "", nil
		})
		defer patches6.Reset()

		patchesIsSharedSubnetPath := gomonkey.ApplyFunc(common.IsSharedSubnetPath, func(ctx context.Context, client client.Client, path string, ns string) (bool, error) {
			return false, nil
		})
		defer patchesIsSharedSubnetPath.Reset()

		patchesUpdateSubnetStatus := gomonkey.ApplyFunc((*SubnetPortReconciler).updateSubnetStatusOnSubnetPort,
			func(r *SubnetPortReconciler, subnetPort *v1alpha1.SubnetPort, nsxSubnet *model.VpcSubnet) error {
				return nil
			})
		defer patchesUpdateSubnetStatus.Reset()

		patchesSetAddressBindingStatus := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort,
			func(client client.Client, ctx context.Context, subnetPort *v1alpha1.SubnetPort, subnetPortService *subnetport.SubnetPortService, transitionTime metav1.Time, e error) {
			})
		defer patchesSetAddressBindingStatus.Reset()

		_, err := r.Reconcile(ctx, req)
		assert.NoError(t, err)
		assert.Equal(t, v1alpha1.IPAddressTypeIPv4, passedInterfaceIPType)
	})
}

type raCapturingStatusWriter struct {
	captured *v1alpha1.SubnetPort
}

func (w *raCapturingStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return nil
}

func (w *raCapturingStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	w.captured = obj.(*v1alpha1.SubnetPort).DeepCopy()
	return nil
}

func (w *raCapturingStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return nil
}

func (w *raCapturingStatusWriter) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	return nil
}

func TestSubnetPortReconciler_Reconcile_RADeactivated(t *testing.T) {
	for _, tt := range []struct {
		name                string
		raDeactivated       bool
		raErr               error
		expectRADeactivated bool
		expectReconcileErr  bool
	}{
		{name: "RA deactivated", raDeactivated: true, expectRADeactivated: true},
		{name: "RA not deactivated", raDeactivated: false, expectRADeactivated: false},
		{name: "RA lookup error fails reconcile", raErr: errors.New("lookup failed"), expectReconcileErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mockCtl := gomock.NewController(t)
			k8sClient := mock_client.NewMockClient(mockCtl)
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
			subnetService := &mock.MockSubnetServiceProvider{}
			ipAllocationService := &mock.MockIPAddressAllocationProvider{}
			vpcService := &mock.MockVPCServiceProvider{}
			vpcService.On("IsRADeactivatedByVPCPath", testifymock.Anything).Return(tt.raDeactivated, tt.raErr)

			r := &SubnetPortReconciler{
				Client:                     k8sClient,
				APIReader:                  k8sClient,
				SubnetPortService:          service,
				SubnetService:              subnetService,
				VPCService:                 vpcService,
				Recorder:                   fakeRecorder{},
				IpAddressAllocationService: ipAllocationService,
			}
			r.StatusUpdater = common.NewStatusUpdater(k8sClient, service.NSXConfig, r.Recorder, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")

			ctx := context.Background()
			req := controllerruntime.Request{NamespacedName: types.NamespacedName{Namespace: "dummy", Name: "dummy"}}

			// 1st Get: Reconciler fetching the CR. 2nd Get: updateSubnetPortStatusConditions
			// re-fetching the latest CR before persisting the merged status.
			k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(
				func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					v1sp := obj.(*v1alpha1.SubnetPort)
					v1sp.Spec.Subnet = "subnet1"
					return nil
				}).Times(2)

			patchesCheckAndGetSubnetPathForSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).CheckAndGetSubnetPathForSubnetPort,
				func(r *SubnetPortReconciler, ctx context.Context, subnetPort *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
					return true, false, "/orgs/default/projects/default/vpcs/vpc1/subnets/subnet-1", nil, nil, v1alpha1.IPAddressTypeIPv4, "", nil
				})
			defer patchesCheckAndGetSubnetPathForSubnetPort.Reset()

			patchesGetSubnetByPath := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetByPath",
				func(s *mock.MockSubnetServiceProvider, nsxSubnetPath string, sharedSubnet bool) (*model.VpcSubnet, error) {
					return &model.VpcSubnet{
						Id:            ptr.To("subnet-1"),
						RealizationId: ptr.To("realization-1"),
					}, nil
				})
			defer patchesGetSubnetByPath.Reset()

			patchesIsSharedSubnetPath := gomonkey.ApplyFunc(common.IsSharedSubnetPath, func(ctx context.Context, client client.Client, path string, ns string) (bool, error) {
				return false, nil
			})
			defer patchesIsSharedSubnetPath.Reset()

			patchesUpdateSubnetPortIPType := gomonkey.ApplyFunc((*SubnetPortReconciler).updateSubnetPortIPType,
				func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort, _ v1alpha1.IPAddressType, _ *model.VpcSubnet) error {
					return nil
				})
			defer patchesUpdateSubnetPortIPType.Reset()

			patchesGetAddressBinding := gomonkey.ApplyMethod(reflect.TypeOf(&subnetport.SubnetPortService{}), "GetAddressBindingBySubnetPort", func(_ *subnetport.SubnetPortService, _ *v1alpha1.SubnetPort) *v1alpha1.AddressBinding {
				return nil
			})
			defer patchesGetAddressBinding.Reset()

			patchesIPAllocCreate := gomonkey.ApplyMethod(reflect.TypeOf(r.IpAddressAllocationService), "CreateIPAddressAllocationForAddressBinding", func(_ *mock.MockIPAddressAllocationProvider, _ *v1alpha1.AddressBinding, _ *v1alpha1.SubnetPort, _ bool) error {
				return nil
			})
			defer patchesIPAllocCreate.Reset()

			patchesIPAllocDelete := gomonkey.ApplyMethod(reflect.TypeOf(r.IpAddressAllocationService), "DeleteIPAddressAllocationForAddressBinding", func(_ *mock.MockIPAddressAllocationProvider, _ metav1.Object) error {
				return nil
			})
			defer patchesIPAllocDelete.Reset()

			attachmentID := "attachment-id"
			portState := &model.SegmentPortState{
				RealizedBindings: []model.AddressBindingEntry{},
				Attachment: &model.SegmentPortAttachmentState{
					Id: &attachmentID,
				},
			}
			patchesCreateOrUpdateSubnetPort := gomonkey.ApplyMethod(reflect.TypeOf(&subnetport.SubnetPortService{}), "CreateOrUpdateSubnetPort",
				func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string, isVmSubnetPort bool, restoreMode bool, interfaceIPType v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
					return portState, nil
				})
			defer patchesCreateOrUpdateSubnetPort.Reset()

			patchesUpdateSubnetStatusOnSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).updateSubnetStatusOnSubnetPort,
				func(r *SubnetPortReconciler, subnetPort *v1alpha1.SubnetPort, nsxSubnet *model.VpcSubnet) error {
					return nil
				})
			defer patchesUpdateSubnetStatusOnSubnetPort.Reset()

			patchesSetAddressBindingStatus := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort,
				func(client client.Client, ctx context.Context, subnetPort *v1alpha1.SubnetPort, subnetPortService *subnetport.SubnetPortService, transitionTime metav1.Time, e error) {
				})
			defer patchesSetAddressBindingStatus.Reset()

			statusWriter := &raCapturingStatusWriter{}
			k8sClient.EXPECT().Status().Return(statusWriter).AnyTimes()

			_, err := r.Reconcile(ctx, req)
			if tt.expectReconcileErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			if assert.NotNil(t, statusWriter.captured) {
				assert.Equal(t, tt.expectRADeactivated, statusWriter.captured.Status.NetworkInterfaceConfig.RADeactivated)
			}
		})
	}
}

func TestSubnetPortReconciler_GarbageCollector(t *testing.T) {
	// gc collect item "sp2345", local store has more item than k8s cache
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
			a.Insert("sp1234")
			a.Insert("sp2345")
			return a
		})
	defer patchesListNSXSubnetPortIDForCR.Reset()
	patchesGetVpcSubnetPortByUID := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
		func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
			if uid == "sp1234" {
				return &model.VpcSubnetPort{Id: servicecommon.String("subnetPort1")}, nil
			}
			return nil, nil
		})
	defer patchesGetVpcSubnetPortByUID.Reset()
	patchesDeleteSubnetPortById := gomonkey.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPortById,
		func(s *subnetport.SubnetPortService, uid string) error {
			return nil
		})
	defer patchesDeleteSubnetPortById.Reset()

	r := &SubnetPortReconciler{
		Client:                     k8sClient,
		Scheme:                     nil,
		SubnetPortService:          service,
		IpAddressAllocationService: &mock.MockIPAddressAllocationProvider{},
	}
	r.StatusUpdater = common.NewStatusUpdater(k8sClient, service.NSXConfig, r.Recorder, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")
	subnetPortList := &v1alpha1.SubnetPortList{}
	k8sClient.EXPECT().List(gomock.Any(), subnetPortList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.SubnetPortList)
		a.Items = append(a.Items, v1alpha1.SubnetPort{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "sp1234"
		a.Items[0].Name = "subnetPort1"
		return nil
	})
	addressBindingList := &v1alpha1.AddressBindingList{}
	k8sClient.EXPECT().List(gomock.Any(), addressBindingList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.AddressBindingList)
		a.Items = append(a.Items, v1alpha1.AddressBinding{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "ab1234"
		a.Items[0].Name = "addressBinding1"
		return nil
	})
	patches := gomonkey.ApplyPrivateMethod(r, "collectAddressBindingGarbage", func(_ *SubnetPortReconciler, _ context.Context, _, _ *string) {})
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

func TestSubnetPortReconciler_subnetPortSubnetIndexFunc(t *testing.T) {
	tests := []struct {
		name           string
		expectedResult []string
		obj            client.Object
	}{
		{
			name:           "Success",
			expectedResult: []string{"subnet1"},
			obj: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					Subnet: "subnet1",
				},
			},
		},
		{
			name:           "EmptySubnet",
			expectedResult: []string{},
			obj: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					Subnet: "",
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
			result := subnetPortSubnetIndexFunc(tt.obj)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestSubnetPortReconciler_subnetPortMACInNeedIndexFunc(t *testing.T) {
	tests := []struct {
		name           string
		expectedResult []string
		obj            client.Object
	}{
		{
			name:           "InvalidObject",
			expectedResult: []string{},
			obj:            &v1alpha1.Subnet{},
		},
		{
			name:           "empty",
			expectedResult: []string{"false"},
			obj:            &v1alpha1.SubnetPort{},
		},
		{
			name:           "nonempty",
			expectedResult: []string{"true"},
			obj: &v1alpha1.SubnetPort{
				Status: v1alpha1.SubnetPortStatus{
					Attachment: v1alpha1.PortAttachment{
						ID: "VIF-1234",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := subnetPortMACInNeedIndexFunc(tt.obj)
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
	k8sClient.EXPECT().List(gomock.Any(), subnetPortList, client.MatchingFields{pkgutil.SubnetPortNamespaceVMIndexKey: "ns/vm1"}).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
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

func TestSubnetPortReconciler_vmMapFunc_WithIndexer(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))

	sp1 := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sp-vm1-ns1",
			Namespace: "ns1",
			Annotations: map[string]string{
				servicecommon.AnnotationAttachmentRef: "virtualmachine/vm1/port1",
			},
		},
	}
	sp2 := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sp-vm2-ns1",
			Namespace: "ns1",
			Annotations: map[string]string{
				servicecommon.AnnotationAttachmentRef: "virtualmachine/vm2/port1",
			},
		},
	}
	sp3 := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sp-vm1-ns2",
			Namespace: "ns2",
			Annotations: map[string]string{
				servicecommon.AnnotationAttachmentRef: "virtualmachine/vm1/port1",
			},
		},
	}
	sp4 := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sp-no-vm",
			Namespace: "ns1",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithIndex(&v1alpha1.SubnetPort{}, pkgutil.SubnetPortNamespaceVMIndexKey, subnetPortNamespaceVMIndexFunc).
		WithObjects(sp1, sp2, sp3, sp4).
		Build()

	r := &SubnetPortReconciler{
		Client: fakeClient,
	}

	vm := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vm1",
			Namespace: "ns1",
		},
	}

	requests := r.vmMapFunc(context.TODO(), vm)
	assert.Equal(t, 1, len(requests))
	assert.Equal(t, reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "sp-vm1-ns1",
			Namespace: "ns1",
		},
	}, requests[0])
}

func TestSubnetPortReconciler_getSubnetCR(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	service := &subnetport.SubnetPortService{}
	r := &SubnetPortReconciler{
		Client:            k8sClient,
		SubnetPortService: service,
	}
	tests := []struct {
		name           string
		subnetPort     *v1alpha1.SubnetPort
		preparedFunc   func()
		expectedResult *v1alpha1.Subnet
		expectedErr    string
		isStale        bool
	}{
		{
			name: "PortOnSubnetSet",
			subnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "port-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					SubnetSet: "subnetset-1",
				},
			},
		},
		{
			name: "PortOnSubnet",
			subnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "port-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					Subnet: "subnet-1",
				},
			},
			preparedFunc: func() {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					subnetCR := obj.(*v1alpha1.Subnet)
					subnetCR.ObjectMeta = metav1.ObjectMeta{
						Name:      "subnet-1",
						Namespace: "ns-1",
					}
					return nil
				})
			},
			expectedResult: &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnet-1",
					Namespace: "ns-1",
				},
			},
		},
		{
			name: "SubnetNotExist",
			subnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "port-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					Subnet: "subnet-1",
				},
			},
			preparedFunc: func() {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("mock error"))
			},
			expectedErr: "mock error",
		},
		{
			name: "SubnetDeleting",
			subnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "port-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					Subnet: "subnet-1",
				},
			},
			preparedFunc: func() {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					subnetCR := obj.(*v1alpha1.Subnet)
					subnetCR.DeletionTimestamp = &metav1.Time{Time: time.Now()}
					return nil
				})
			},
			expectedErr: "subnet ns-1/subnet-1 is being deleted, cannot operate SubnetPort port-1",
			isStale:     true,
		},
	}
	for _, tc := range tests {
		if tc.preparedFunc != nil {
			tc.preparedFunc()
		}
		subnetCR, isStale, err := r.getSubnetCR(context.Background(), tc.subnetPort)
		if tc.expectedErr != "" {
			assert.Contains(t, err.Error(), tc.expectedErr)
		} else {
			assert.Equal(t, tc.expectedResult, subnetCR)
			assert.Equal(t, tc.isStale, isStale)
		}
	}
}

func TestSubnetPortReconciler_getParentIPAddressTypeFromCR(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	service := &subnetport.SubnetPortService{}
	r := &SubnetPortReconciler{
		Client:            k8sClient,
		SubnetPortService: service,
	}
	tests := []struct {
		name           string
		subnetPort     *v1alpha1.SubnetPort
		preparedFunc   func() *gomonkey.Patches
		expectedResult v1alpha1.IPAddressType
		expectedErr    string
	}{
		{
			name: "PortOnSubnet_IPv4",
			subnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "port-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					Subnet: "subnet-1",
				},
			},
			preparedFunc: func() *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					subnetCR := obj.(*v1alpha1.Subnet)
					subnetCR.Spec.IPAddressType = v1alpha1.IPAddressTypeIPv4
					return nil
				})
				return nil
			},
			expectedResult: v1alpha1.IPAddressTypeIPv4,
		},
		{
			name: "PortOnSubnet_IPv6",
			subnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "port-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					Subnet: "subnet-1",
				},
			},
			preparedFunc: func() *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					subnetCR := obj.(*v1alpha1.Subnet)
					subnetCR.Spec.IPAddressType = v1alpha1.IPAddressTypeIPv6
					return nil
				})
				return nil
			},
			expectedResult: v1alpha1.IPAddressTypeIPv6,
		},
		{
			name: "PortOnSubnet_GetError",
			subnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "port-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					Subnet: "subnet-1",
				},
			},
			preparedFunc: func() *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("subnet get error"))
				return nil
			},
			expectedErr: "subnet get error",
		},
		{
			name: "PortOnSubnetSet_DualStack",
			subnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "port-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					SubnetSet: "subnetset-1",
				},
			},
			preparedFunc: func() *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					ss := obj.(*v1alpha1.SubnetSet)
					ss.Name = "subnetset-1"
					ss.Namespace = "ns-1"
					ss.Spec.IPAddressType = v1alpha1.IPAddressTypeIPv4IPv6
					return nil
				})
				return nil
			},
			expectedResult: v1alpha1.IPAddressTypeIPv4IPv6,
		},
		{
			name: "PortOnSubnetSet_GetError",
			subnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "port-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					SubnetSet: "subnetset-1",
				},
			},
			preparedFunc: func() *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("subnetset not found"))
				return nil
			},
			expectedErr: "subnetset not found",
		},
		{
			name: "PortOnSubnetSet_PendingCalculation",
			subnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "port-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					SubnetSet: "subnetset-1",
				},
			},
			preparedFunc: func() *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					ss := obj.(*v1alpha1.SubnetSet)
					ss.Name = "subnetset-1"
					ss.Namespace = "ns-1"
					ss.Spec.IPAddressType = ""
					return nil
				})
				return nil
			},
			expectedErr: "waiting for SubnetSet ns-1/subnetset-1 IPAddressType calculation",
		},
		{
			name: "PortOnDefaultSubnetSet",
			subnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "port-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{},
			},
			preparedFunc: func() *gomonkey.Patches {
				return gomonkey.ApplyFunc(common.GetDefaultSubnetSetByNamespace,
					func(client client.Client, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
						return &v1alpha1.SubnetSet{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "default-vm-network",
								Namespace: namespace,
							},
							Spec: v1alpha1.SubnetSetSpec{
								IPAddressType: v1alpha1.IPAddressTypeIPv4,
							},
						}, nil
					})
			},
			expectedResult: v1alpha1.IPAddressTypeIPv4,
		},
		{
			name: "PortOnDefaultSubnetSet_Error",
			subnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "port-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{},
			},
			preparedFunc: func() *gomonkey.Patches {
				return gomonkey.ApplyFunc(common.GetDefaultSubnetSetByNamespace,
					func(client client.Client, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
						return nil, fmt.Errorf("default subnetset not found")
					})
			},
			expectedErr: "default subnetset not found",
		},
		{
			name: "PortOnDefaultSubnetSet_PendingCalculation",
			subnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "port-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{},
			},
			preparedFunc: func() *gomonkey.Patches {
				return gomonkey.ApplyFunc(common.GetDefaultSubnetSetByNamespace,
					func(client client.Client, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
						return &v1alpha1.SubnetSet{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "default-vm-network",
								Namespace: namespace,
							},
							Spec: v1alpha1.SubnetSetSpec{
								IPAddressType: "",
							},
						}, nil
					})
			},
			expectedErr: "waiting for SubnetSet ns-1/default-vm-network IPAddressType calculation",
		},
		{
			name: "PortOnSubnetSet_RestoreMode_EmptyIPAddressType",
			subnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "port-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					SubnetSet: "subnetset-1",
				},
			},
			preparedFunc: func() *gomonkey.Patches {
				r.restoreMode = true
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					ss := obj.(*v1alpha1.SubnetSet)
					ss.Name = "subnetset-1"
					ss.Namespace = "ns-1"
					ss.Spec.IPAddressType = ""
					return nil
				})
				return nil
			},
			expectedResult: v1alpha1.IPAddressTypeIPv4,
		},
		{
			name: "PortOnDefaultSubnetSet_RestoreMode_EmptyIPAddressType",
			subnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "port-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{},
			},
			preparedFunc: func() *gomonkey.Patches {
				r.restoreMode = true
				return gomonkey.ApplyFunc(common.GetDefaultSubnetSetByNamespace,
					func(client client.Client, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
						return &v1alpha1.SubnetSet{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "default-vm-network",
								Namespace: namespace,
							},
							Spec: v1alpha1.SubnetSetSpec{
								IPAddressType: "",
							},
						}, nil
					})
			},
			expectedResult: v1alpha1.IPAddressTypeIPv4,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r.restoreMode = false
			if tc.preparedFunc != nil {
				patches := tc.preparedFunc()
				if patches != nil {
					defer patches.Reset()
				}
			}
			ipType, err := r.getParentIPAddressTypeFromCR(context.Background(), tc.subnetPort)
			if tc.expectedErr != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErr)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedResult, ipType)
			}
		})
	}
}

func TestSubnetPortReconciler_deleteSubnetPortByName(t *testing.T) {
	subnetportId1 := "subnetport-id-1"
	subnetportId2 := "subnetport-id-2"
	namespaceScope := "nsx-op/vm_namespace"
	subnetportName1 := "subnetport-1"
	subnetportName2 := "subnetport-2"
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
				Tag:   &subnetportName1,
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
				Tag:   &subnetportName2,
			},
		},
	}
	r := &SubnetPortReconciler{
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
	err := r.deleteSubnetPortByName(context.TODO(), ns, subnetportName2)
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
	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	k8sClient.EXPECT().Status().Return(fakewriter)

	patches := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort, func(client client.Client, ctx context.Context, subnetPort *v1alpha1.SubnetPort, subnetPortService *subnetport.SubnetPortService, transitionTime metav1.Time, e error, isPublicSubnet bool) {
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
	setReadyStatusTrue(k8sClient, context.TODO(), sp, metav1.Now(), subnetPortService, false)
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
		expectedIPType     v1alpha1.IPAddressType
		restoreMode        bool
		subnetport         *v1alpha1.SubnetPort
		subnet             *v1alpha1.Subnet
	}{
		{
			name: "ExistedSubnet",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
					func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
						return &model.VpcSubnetPort{
							Id:         servicecommon.String("port1"),
							ParentPath: servicecommon.String("subnet-path-1"),
						}, nil
					})
				patches.ApplyFunc((*subnet.SubnetService).GetSubnetByPath,
					func(s *subnet.SubnetService, path string, sharedSubnet bool) (*model.VpcSubnet, error) {
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
			expectedIPType:     "",
		},
		{
			name: "SpecificSubnetSuccess",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
					func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
						return nil, nil
					})
				patches.ApplyFunc((*SubnetPortReconciler).getSubnetCR,
					func(r *SubnetPortReconciler, ctx context.Context, subnetPort *v1alpha1.SubnetPort) (*v1alpha1.Subnet, bool, error) {
						return &v1alpha1.Subnet{
							Spec: v1alpha1.SubnetSpec{
								IPAddressType: v1alpha1.IPAddressTypeIPv4, // Prevents blank resolution fallthrough
							},
						}, false, nil
					})
				patches.ApplyMethod(reflect.TypeOf(&subnet.SubnetStore{}), "GetByIndex",
					func(s *subnet.SubnetStore, key string, value string) []*model.VpcSubnet {
						return []*model.VpcSubnet{{
							Path:           servicecommon.String("subnet-path-1"),
							Ipv4SubnetSize: servicecommon.Int64(16),
							Id:             servicecommon.String("subnet-1"),
						}}
					})
				patches.ApplyFunc((*subnetport.SubnetPortService).AllocatePortFromSubnet,
					func(s *subnetport.SubnetPortService, nsxSubnet *model.VpcSubnet, sharedSubnet bool, interfaceType v1alpha1.IPAddressType, staticIPAllocationType v1alpha1.StaticIPAllocationType, addressBindings []v1alpha1.PortAddressBinding) (bool, error) {
						return true, nil
					})
				return patches
			},
			subnet: &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name: "subnet-1",
					UID:  "subnet-id-1",
				},
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
			expectedIPType: v1alpha1.IPAddressTypeIPv4,
		},
		{
			name: "SpecificSubnetSetNotExisted",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
					func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
						return nil, nil
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
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
					func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
						return nil, nil
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
			name: "SpecificSubnetSetWaitingIPCalculation",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
					func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
						return nil, nil
					})
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					subnetSetCR := obj.(*v1alpha1.SubnetSet)
					subnetSetCR.Namespace = "ns-1"
					subnetSetCR.Name = "subnetset-1"
					subnetSetCR.Spec.IPAddressType = ""
					return nil
				})
				return patches
			},
			expectedErr: "waiting for SubnetSet ns-1/subnetset-1 IPAddressType calculation",
			subnetport: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnetport-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					SubnetSet:       "subnetset-1",
					InterfaceIPType: "",
				},
			},
		},
		{
			name: "SpecificSubnetSetSuccess",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
					func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
						return nil, nil
					})
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					subnetSetCR := obj.(*v1alpha1.SubnetSet)
					subnetSetCR.Name = "subnetset-1"
					subnetSetCR.UID = "subnetset-id-1"
					subnetSetCR.Spec.IPAddressType = v1alpha1.IPAddressTypeIPv4
					return nil
				})

				patches.ApplyFunc(common.AllocateSubnetFromSubnetSet,
					func(client client.Client, apiReader client.Reader, subnetSet *v1alpha1.SubnetSet, vpcService servicecommon.VPCServiceProvider, subnetService servicecommon.SubnetServiceProvider, subnetPortService servicecommon.SubnetPortServiceProvider, interfaceType v1alpha1.IPAddressType, rawStaticIPAllocationType v1alpha1.StaticIPAllocationType, addressBindings []v1alpha1.PortAddressBinding) (string, v1alpha1.StaticIPAllocationType, *types.UID, *sync.RWMutex, error) {
						return "subnet-path-1", v1alpha1.StaticIPAllocationTypeIPv4, nil, nil, nil
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
			expectedIPType: v1alpha1.IPAddressTypeIPv4,
		},
		{
			name: "DefaultSubnetDeleted",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
					func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
						return nil, nil
					})
				patches.ApplyFunc(common.GetDefaultSubnetSetByNamespace,
					func(client client.Client, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
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
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
					func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
						return nil, nil
					})
				patches.ApplyFunc(common.GetDefaultSubnetSetByNamespace,
					func(client client.Client, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
						subnetSetCR := &v1alpha1.SubnetSet{}
						subnetSetCR.Name = "default-subnetset"
						subnetSetCR.Spec.IPAddressType = v1alpha1.IPAddressTypeIPv4 // Ensure validation bypass
						return subnetSetCR, nil
					})

				patches.ApplyFunc(common.AllocateSubnetFromSubnetSet,
					func(client client.Client, apiReader client.Reader, subnetSet *v1alpha1.SubnetSet, vpcService servicecommon.VPCServiceProvider, subnetService servicecommon.SubnetServiceProvider, subnetPortService servicecommon.SubnetPortServiceProvider, interfaceType v1alpha1.IPAddressType, rawStaticIPAllocationType v1alpha1.StaticIPAllocationType, addressBindings []v1alpha1.PortAddressBinding) (string, v1alpha1.StaticIPAllocationType, *types.UID, *sync.RWMutex, error) {
						return "subnet-path-1", v1alpha1.StaticIPAllocationTypeIPv4, nil, nil, nil
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
			expectedIPType: v1alpha1.IPAddressTypeIPv4,
		},
		{
			name: "RestoreSubnetPortSuccess",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
					func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
						return nil, nil
					})
				patches.ApplyFunc((*SubnetPortReconciler).getSubnetBySubnetPort, func(r *SubnetPortReconciler, subnetPort *v1alpha1.SubnetPort) (string, error) {
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
				Status: v1alpha1.SubnetPortStatus{
					NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
						IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
							{Gateway: "10.0.0.1"},
						},
					},
				},
			},
			restoreMode:        true,
			expectedIsExisting: true,
			expectedIPType:     "",
		},
		{
			name: "NoSubnetForRestoredSubnetPort",
			prepareFunc: func(t *testing.T, spr *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
					func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
						return nil, nil
					})
				patches.ApplyFunc((*SubnetPortReconciler).getSubnetBySubnetPort, func(r *SubnetPortReconciler, subnetPort *v1alpha1.SubnetPort) (string, error) {
					return "", errors.New("mocked getSubnetBySubnetPort error")
				})
				return patches
			},
			subnetport: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnetport-1",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetPortSpec{
					SubnetSet: "subnetset-1",
				},
				Status: v1alpha1.SubnetPortStatus{
					NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
						IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
							{Gateway: "10.0.0.1"},
						},
					},
				},
			},
			restoreMode: true,
			expectedErr: "mocked getSubnetBySubnetPort error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()
			patches := tt.prepareFunc(t, r)
			defer patches.Reset()
			r.restoreMode = tt.restoreMode
			// Expanded to consume 8 return values properly
			isExisting, isStale, subnetPath, _, _, interfaceType, _, err := r.CheckAndGetSubnetPathForSubnetPort(ctx, tt.subnetport)
			assert.Equal(t, tt.expectedIsStale, isStale)
			assert.Equal(t, tt.expectedIsExisting, isExisting)
			if tt.expectedErr != "" {
				assert.NotNil(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, tt.expectedSubnetPath, subnetPath)
				assert.Equal(t, tt.expectedIPType, interfaceType)
			}
		})
	}
}

func TestSubnetPortReconciler_updateSubnetStatusOnSubnetPort(t *testing.T) {
	r := &SubnetPortReconciler{
		SubnetPortService: &subnetport.SubnetPortService{},
		SubnetService:     &subnet.SubnetService{},
	}

	tests := []struct {
		name        string
		gateways    []servicecommon.GatewayPrefixInfo
		initialIPs  []v1alpha1.NetworkInterfaceIPAddress
		expectedIPs []v1alpha1.NetworkInterfaceIPAddress
	}{
		{
			name: "Pre-set Gateway",
			gateways: []servicecommon.GatewayPrefixInfo{
				{Gateway: "10.0.0.1", Prefix: 28},
			},
			initialIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "10.0.0.2/28", Gateway: "10.0.0.1"},
			},
			expectedIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "10.0.0.2/28", Gateway: "10.0.0.1"},
			},
		},
		{
			name: "Pre-set Gateway without prefix",
			gateways: []servicecommon.GatewayPrefixInfo{
				{Gateway: "10.0.0.1", Prefix: 28},
			},
			initialIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "10.0.0.2", Gateway: "10.0.0.1"},
			},
			expectedIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "10.0.0.2/28", Gateway: "10.0.0.1"},
			},
		},
		{
			name: "Single IPv4 address binding",
			gateways: []servicecommon.GatewayPrefixInfo{
				{Gateway: "10.0.0.1", Prefix: 28},
			},
			initialIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "10.0.0.2"},
			},
			expectedIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "10.0.0.2/28", Gateway: "10.0.0.1"},
			},
		},
		{
			name: "Multiple address bindings - Dual Stack IPv4 and IPv6",
			gateways: []servicecommon.GatewayPrefixInfo{
				{Gateway: "10.0.0.1", Prefix: 24},
				{Gateway: "2001:db8::1", Prefix: 64},
			},
			initialIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "2001:db8::2"},
				{IPAddress: "10.0.0.5"},
			},
			expectedIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "10.0.0.5/24", Gateway: "10.0.0.1"},
				{IPAddress: "2001:db8::2/64", Gateway: "2001:db8::1"},
			},
		},
		{
			name: "Empty IP configuration",
			gateways: []servicecommon.GatewayPrefixInfo{
				{Gateway: "10.0.0.1", Prefix: 24},
			},
			initialIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: ""},
			},
			expectedIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "", Gateway: "10.0.0.1"},
			},
		},
		{
			name: "Empty IP configuration - Dual Stack IPv4 and IPv6",
			gateways: []servicecommon.GatewayPrefixInfo{
				{Gateway: "10.0.0.1", Prefix: 24},
				{Gateway: "2001:db8::1", Prefix: 64},
			},
			initialIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: ""},
				{IPAddress: ""},
			},
			expectedIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "", Gateway: "10.0.0.1"},
				{IPAddress: "", Gateway: "2001:db8::1"},
			},
		},
		{
			name: "Extra IPv6 Address",
			gateways: []servicecommon.GatewayPrefixInfo{
				{Gateway: "10.0.0.1", Prefix: 24},
				{Gateway: "2001:db8::1", Prefix: 64},
			},
			initialIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "10.0.0.5"},
				{IPAddress: "2002:db8::2"}, // Mismatch
				{IPAddress: "2001:db8::2"},
			},
			expectedIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "10.0.0.5/24", Gateway: "10.0.0.1"},
				{IPAddress: "2001:db8::2/64", Gateway: "2001:db8::1"},
			},
		},
		{
			name: "Empty IP configuration without update",
			gateways: []servicecommon.GatewayPrefixInfo{
				{Gateway: "10.0.0.1", Prefix: 24},
				{Gateway: "2001:db8::1", Prefix: 64},
			},
			initialIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "", Gateway: "10.0.0.1"},
				{IPAddress: "", Gateway: "2001:db8::1"},
			},
			expectedIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "", Gateway: "10.0.0.1"},
				{IPAddress: "", Gateway: "2001:db8::1"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Mock the SubnetService method inside the scope of this subtest
			patchesGetAllGatewayPrefixesOfSubnet := gomonkey.ApplyFunc((*subnet.SubnetService).GetAllGatewayPrefixesOfSubnet,
				func(s *subnet.SubnetService, obj *model.VpcSubnet) ([]servicecommon.GatewayPrefixInfo, error) {
					return tt.gateways, nil
				})
			defer patchesGetAllGatewayPrefixesOfSubnet.Reset()

			sp := &v1alpha1.SubnetPort{
				Status: v1alpha1.SubnetPortStatus{
					NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
						IPAddresses: tt.initialIPs,
					},
				},
			}

			err := r.updateSubnetStatusOnSubnetPort(sp, &model.VpcSubnet{
				RealizationId: servicecommon.String("realization-id-1"),
			})

			assert.Nil(t, err)
			assert.Equal(t, "realization-id-1", sp.Status.NetworkInterfaceConfig.LogicalSwitchUUID)
			assert.Equal(t, tt.expectedIPs, sp.Status.NetworkInterfaceConfig.IPAddresses)
		})
	}
}

func TestNetworkInterfaceIPAddressesFromRealizedBindings(t *testing.T) {
	tests := []struct {
		name                   string
		realizedBindings       []model.AddressBindingEntry
		staticIPAllocationType v1alpha1.StaticIPAllocationType
		hasExplicitIPv4        bool
		hasExplicitIPv6        bool
		expectedIPs            []v1alpha1.NetworkInterfaceIPAddress
		expectedMAC            string
	}{
		{
			name:                   "No realized bindings",
			realizedBindings:       nil,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
			expectedIPs:            []v1alpha1.NetworkInterfaceIPAddress{},
			expectedMAC:            "",
		},
		{
			name: "Single IPv4 binding",
			realizedBindings: []model.AddressBindingEntry{
				{Binding: &model.PacketAddressClassifier{
					IpAddress:  servicecommon.String("10.0.0.2"),
					MacAddress: servicecommon.String("aa:bb:cc:dd:ee:ff"),
				}},
			},
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
			expectedIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "10.0.0.2"},
			},
			expectedMAC: "aa:bb:cc:dd:ee:ff",
		},
		{
			name: "Dual-stack: 1 IPv4 + 1 IPv6, one MAC",
			realizedBindings: []model.AddressBindingEntry{
				{Binding: &model.PacketAddressClassifier{
					IpAddress:  servicecommon.String("10.0.0.2"),
					MacAddress: servicecommon.String("aa:bb:cc:dd:ee:ff"),
				}},
				{Binding: &model.PacketAddressClassifier{
					IpAddress:  servicecommon.String("fe80::1"),
					MacAddress: servicecommon.String("aa:bb:cc:dd:ee:ff"),
				}},
			},
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4IPv6,
			expectedIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "10.0.0.2"},
				{IPAddress: "fe80::1"},
			},
			expectedMAC: "aa:bb:cc:dd:ee:ff",
		},
		{
			// Mixed-mode Subnet: StaticIPAllocationType=IPv6 only. DHCP independently
			// realizes an IPv4 address too, but it must NOT be shown in status: SubnetPort
			// only reconciles on CR events, while a DHCP lease can change at any time, so
			// showing it here would risk status going stale until an unrelated reconcile
			// happens to refresh it. The static IPv6 address doesn't have that problem,
			// since it only changes via a CR spec change, which itself triggers a reconcile.
			name: "Mixed-mode: DHCP-realized IPv4 is dropped, only static-realized IPv6 is shown",
			realizedBindings: []model.AddressBindingEntry{
				{Binding: &model.PacketAddressClassifier{
					IpAddress:  servicecommon.String("172.26.0.165"),
					MacAddress: servicecommon.String("00:50:56:ba:d6:7a"),
				}},
				{Binding: &model.PacketAddressClassifier{
					IpAddress:  servicecommon.String("2001:db8::165"),
					MacAddress: servicecommon.String("00:50:56:ba:d6:7a"),
				}},
			},
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv6,
			expectedIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "2001:db8::165"},
			},
			expectedMAC: "00:50:56:ba:d6:7a",
		},
		{
			// Pure DHCP with no explicit addressBindings: StaticIPAllocationType=None. No
			// family is statically requested, so the DHCP-realized IPv4 must be dropped
			// entirely - status should show no IP at all, same as before this filtering
			// was added.
			name: "Pure DHCP, no addressBindings: StaticIPAllocationType=None drops the DHCP-realized IPv4",
			realizedBindings: []model.AddressBindingEntry{
				{Binding: &model.PacketAddressClassifier{
					IpAddress:  servicecommon.String("172.26.0.165"),
					MacAddress: servicecommon.String("00:50:56:ba:d6:7a"),
				}},
			},
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
			expectedIPs:            []v1alpha1.NetworkInterfaceIPAddress{},
			expectedMAC:            "00:50:56:ba:d6:7a",
		},
		{
			// DHCP Subnet with an explicit addressBindings entry (IP+MAC bound from the
			// Subnet's MAC pool): StaticIPAllocationType resolves to None because the
			// Subnet itself has no static allocation enabled, but the address was
			// explicitly requested by the user, not an incidental DHCP lease - it must be
			// shown in status. Regression test: this used to be dropped by the same
			// filter that hides unrequested DHCP addresses on mixed-mode Subnets, which
			// left status.NetworkInterfaceConfig.IPAddresses without an IP forever even
			// though NSX had fully realized the port.
			name: "DHCP Subnet with explicit addressBindings: StaticIPAllocationType=None must not drop the requested IP",
			realizedBindings: []model.AddressBindingEntry{
				{Binding: &model.PacketAddressClassifier{
					IpAddress:  servicecommon.String("172.26.0.165"),
					MacAddress: servicecommon.String("00:50:56:ba:d6:7a"),
				}},
			},
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
			hasExplicitIPv4:        true,
			expectedIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "172.26.0.165"},
			},
			expectedMAC: "00:50:56:ba:d6:7a",
		},
		{
			// Dual-stack mixed-mode Subnet: an explicit static IPv4 binding is requested,
			// but IPv6 is not - it's realized independently via DHCP/SLAAC. The explicit
			// IPv4 must be shown (hasExplicitIPv4), while the incidental, unrequested IPv6
			// must still be dropped exactly as in the plain mixed-mode case above:
			// per-family exemption must not become "any explicit binding shows everything".
			name: "Dual-stack, explicit IPv4 only: incidental DHCP IPv6 is still dropped",
			realizedBindings: []model.AddressBindingEntry{
				{Binding: &model.PacketAddressClassifier{
					IpAddress:  servicecommon.String("172.26.0.10"),
					MacAddress: servicecommon.String("00:50:56:ba:d6:7a"),
				}},
				{Binding: &model.PacketAddressClassifier{
					IpAddress:  servicecommon.String("2001:db8::10"),
					MacAddress: servicecommon.String("00:50:56:ba:d6:7a"),
				}},
			},
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
			hasExplicitIPv4:        true,
			expectedIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "172.26.0.10"},
			},
			expectedMAC: "00:50:56:ba:d6:7a",
		},
		{
			name: "Multi-IP addressBindings sharing one MAC (spec 2.4.3): 3 same-family entries",
			realizedBindings: []model.AddressBindingEntry{
				{Binding: &model.PacketAddressClassifier{
					IpAddress:  servicecommon.String("192.168.0.2"),
					MacAddress: servicecommon.String("00:11:22:33:44:55"),
				}},
				{Binding: &model.PacketAddressClassifier{
					IpAddress:  servicecommon.String("192.168.0.3"),
					MacAddress: servicecommon.String("00:11:22:33:44:55"),
				}},
				{Binding: &model.PacketAddressClassifier{
					IpAddress:  servicecommon.String("192.168.0.4"),
					MacAddress: servicecommon.String("00:11:22:33:44:55"),
				}},
			},
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
			expectedIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "192.168.0.2"},
				{IPAddress: "192.168.0.3"},
				{IPAddress: "192.168.0.4"},
			},
			expectedMAC: "00:11:22:33:44:55",
		},
		{
			name: "MAC taken from first binding that has one; only IP-bearing bindings produce entries",
			realizedBindings: []model.AddressBindingEntry{
				{Binding: &model.PacketAddressClassifier{IpAddress: nil}},
				{Binding: &model.PacketAddressClassifier{
					IpAddress:  servicecommon.String("192.168.0.2"),
					MacAddress: servicecommon.String("00:11:22:33:44:55"),
				}},
			},
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
			expectedIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "192.168.0.2"},
			},
			expectedMAC: "00:11:22:33:44:55",
		},
		{
			name: "Zero MAC 00:00:00:00:00:00 is ignored and not set as macAddress",
			realizedBindings: []model.AddressBindingEntry{
				{Binding: &model.PacketAddressClassifier{
					IpAddress:  servicecommon.String("192.168.0.51"),
					MacAddress: servicecommon.String("00:00:00:00:00:00"),
				}},
			},
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
			hasExplicitIPv4:        true,
			expectedIPs: []v1alpha1.NetworkInterfaceIPAddress{
				{IPAddress: "192.168.0.51"},
			},
			expectedMAC: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ipAddresses, macAddress := networkInterfaceIPAddressesFromRealizedBindings(tt.realizedBindings, tt.staticIPAllocationType, tt.hasExplicitIPv4, tt.hasExplicitIPv6)
			assert.Equal(t, tt.expectedIPs, ipAddresses)
			assert.Equal(t, tt.expectedMAC, macAddress)
		})
	}
}

func TestRealizedBindingsCoverStaticFamilies(t *testing.T) {
	ipv4Binding := model.AddressBindingEntry{Binding: &model.PacketAddressClassifier{IpAddress: servicecommon.String("10.0.0.2")}}
	ipv6Binding := model.AddressBindingEntry{Binding: &model.PacketAddressClassifier{IpAddress: servicecommon.String("fe80::1")}}

	tests := []struct {
		name                   string
		realizedBindings       []model.AddressBindingEntry
		staticIPAllocationType v1alpha1.StaticIPAllocationType
		expected               bool
	}{
		{
			name:                   "No realized bindings",
			realizedBindings:       nil,
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
			expected:               false,
		},
		{
			name:                   "IPv4 requested and realized",
			realizedBindings:       []model.AddressBindingEntry{ipv4Binding},
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
			expected:               true,
		},
		{
			name: "Mixed-mode Subnet: StaticIPAllocationType IPv6 but only the DHCP IPv4 is realized yet",
			realizedBindings: []model.AddressBindingEntry{
				ipv4Binding,
			},
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv6,
			expected:               false,
		},
		{
			name: "Mixed-mode Subnet: StaticIPAllocationType IPv6 and the static IPv6 binding is realized alongside DHCP IPv4",
			realizedBindings: []model.AddressBindingEntry{
				ipv4Binding,
				ipv6Binding,
			},
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv6,
			expected:               true,
		},
		{
			name:                   "Dual-stack static: only IPv4 realized so far",
			realizedBindings:       []model.AddressBindingEntry{ipv4Binding},
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4IPv6,
			expected:               false,
		},
		{
			name:                   "Dual-stack static: both families realized",
			realizedBindings:       []model.AddressBindingEntry{ipv4Binding, ipv6Binding},
			staticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4IPv6,
			expected:               true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, realizedBindingsCoverStaticFamilies(tt.realizedBindings, tt.staticIPAllocationType))
		})
	}
}

func TestSubnetPortReconciler_getVirtualMachine(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	apiReader := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	r := &SubnetPortReconciler{
		Client:    k8sClient,
		APIReader: apiReader,
	}
	patchesGetVirtualMachineNameForSubnetPort := gomonkey.ApplyFunc(common.GetVirtualMachineNameForSubnetPort,
		func(subnetPort *v1alpha1.SubnetPort) (string, string, error) {
			return "vm-1", "eth0", nil
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
	res, nic, err := r.getVirtualMachine(context.TODO(), sp, false)
	assert.Nil(t, err)
	assert.Equal(t, labels, res.Labels)
	assert.Equal(t, nic, "eth0")

	apiReader.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		vm := obj.(*vmv1alpha1.VirtualMachine)
		vm.ObjectMeta.Labels = labels
		return nil
	})
	res, nic, err = r.getVirtualMachine(context.TODO(), sp, true)
	assert.Nil(t, err)
	assert.Equal(t, labels, res.Labels)
	assert.Equal(t, nic, "eth0")
}

func TestSubnetPortReconciler_getSubnetBySubnetPort(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	r := &SubnetPortReconciler{
		Client:        k8sClient,
		SubnetService: &subnet.SubnetService{},
	}
	// SubnetPort with SubnetSet in spec
	patches := gomonkey.ApplyFunc(common.GetNSXSubnetsForSubnetSet, func(client client.Client, subnetSet *v1alpha1.SubnetSet, subnetService servicecommon.SubnetServiceProvider) ([]*model.VpcSubnet, error) {
		return []*model.VpcSubnet{
			{
				Path:        servicecommon.String("/subnet-2"),
				IpAddresses: []string{"10.0.0.16/28"},
			},
			{
				Path:        servicecommon.String("/subnet-3"),
				IpAddresses: []string{"10.0.0.32/28"},
			},
			{
				Path:        servicecommon.String("/subnet-4"),
				IpAddresses: []string{"10.0.0.48/28"},
			},
			{
				Path:        servicecommon.String("/subnet-5"),
				IpAddresses: []string{"10.0.0.64/28"},
			},
		}, nil
	})

	// SubnetPort with default SubnetSet
	patches.ApplyFunc(common.GetDefaultSubnetSetByNamespace, func(client client.Client, namespace string, resourceType string) (*v1alpha1.SubnetSet, error) {
		return &v1alpha1.SubnetSet{
			ObjectMeta: metav1.ObjectMeta{Name: "vm-default", Namespace: "ns-1"},
		}, nil
	})
	patches.ApplyFunc((*subnet.SubnetService).GetSubnetByCR, func(s *subnet.SubnetService, subnet *v1alpha1.Subnet) (*model.VpcSubnet, error) {
		return &model.VpcSubnet{
			Path:        servicecommon.String("/subnet-1"),
			IpAddresses: []string{"10.0.0.0/28"},
		}, nil
	})
	defer patches.Reset()

	subnetCR := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "subnet-1",
			Namespace: "ns-1",
		},
	}

	// SubnetPort with Subnet - happy path
	subnetPath, err := r.getSubnetBySubnetPort(&v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "SubnetPort-1",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetPortSpec{
			Subnet: "subnet-1",
		},
		Status: v1alpha1.SubnetPortStatus{
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{Gateway: "10.0.0.2"},
				},
			},
		},
	}, subnetCR)
	assert.Nil(t, err)
	assert.Equal(t, "/subnet-1", subnetPath)

	// SubnetPort with Subnet - gateway ip not match
	_, err = r.getSubnetBySubnetPort(&v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "SubnetPort-1",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetPortSpec{
			Subnet: "subnet-1",
		},
		Status: v1alpha1.SubnetPortStatus{
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{Gateway: "10.0.1.0"},
				},
			},
		},
	}, subnetCR)
	assert.Contains(t, err.Error(), "failed to find Subnet matching")

	// SubnetPort with Subnet - not pass subnet
	_, err = r.getSubnetBySubnetPort(&v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "SubnetPort-1",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetPortSpec{
			Subnet: "subnet-1",
		},
		Status: v1alpha1.SubnetPortStatus{
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{Gateway: "10.0.1.0"},
				},
			},
		},
	}, nil)
	assert.Contains(t, err.Error(), "Subnet CR ns-1/subnet-1 not provided")

	// SubnetPort with SubnetSet
	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	subnetPath, err = r.getSubnetBySubnetPort(&v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "SubnetPort-1",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetPortSpec{
			SubnetSet: "subnetset-1",
		},
		Status: v1alpha1.SubnetPortStatus{
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{Gateway: "10.0.0.35"},
				},
			},
		},
	}, nil)
	assert.Nil(t, err)
	assert.Equal(t, "/subnet-3", subnetPath)

	// SubnetPort with default SubnetSet
	subnetPath, err = r.getSubnetBySubnetPort(&v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "SubnetPort-1",
			Namespace: "ns-1",
		},
		Status: v1alpha1.SubnetPortStatus{
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{Gateway: "10.0.0.50"},
				},
			},
		},
	}, nil)
	assert.Nil(t, err)
	assert.Equal(t, "/subnet-4", subnetPath)
}

func TestSubnetPortReconciler_RestoreReconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	r := &SubnetPortReconciler{
		Client: k8sClient,
		SubnetPortService: &subnetport.SubnetPortService{
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}
	patches := gomonkey.ApplyFunc((*servicecommon.ResourceStore).ListIndexFuncValues, func(s *servicecommon.ResourceStore, key string) sets.Set[string] {
		return sets.New[string]("port-1", "port-3", "port-4")
	})
	defer patches.Reset()

	patches = gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetByIndex, func(s *subnetport.SubnetPortStore, index string, value string) []*model.VpcSubnetPort {
		subnetport := &model.VpcSubnetPort{
			Attachment: &model.PortAttachment{},
		}
		switch value {
		case "port-3":
			subnetport.Attachment.Id = servicecommon.String("attachment-id-3")
		case "port-4":
			subnetport.Attachment.Id = servicecommon.String("attachment-id-4-update")
		}
		return []*model.VpcSubnetPort{subnetport}
	})
	defer patches.Reset()

	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		subnetportList := list.(*v1alpha1.SubnetPortList)
		subnetportList.Items = []v1alpha1.SubnetPort{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "port-1", Namespace: "ns-1", UID: "port-1"},
				Status: v1alpha1.SubnetPortStatus{
					NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
						IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
							{IPAddress: "10.0.0.3/28", Gateway: "10.0.0.1"},
						},
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "port-2", Namespace: "ns-1", UID: "port-2"},
				Status: v1alpha1.SubnetPortStatus{
					NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
						IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
							{IPAddress: "10.0.1.3/28", Gateway: "10.0.1.1"},
						},
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "port-3", Namespace: "ns-2", UID: "port-3"},
				Status: v1alpha1.SubnetPortStatus{
					Attachment: v1alpha1.PortAttachment{
						ID: "attachment-id-3",
					},
					NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
						IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
							{IPAddress: "10.0.0.4/28", Gateway: "10.0.0.1"},
						},
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "port-4", Namespace: "ns-2", UID: "port-4"},
				Status: v1alpha1.SubnetPortStatus{
					Attachment: v1alpha1.PortAttachment{
						ID: "attachment-id-4",
					},
					NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
						IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
							{IPAddress: "10.0.0.5/28", Gateway: "10.0.0.1"},
						},
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "port-4", Namespace: "ns-2", UID: "port-4"},
			},
		}
		return nil
	})

	reqList := []reconcile.Request{}
	patches.ApplyFunc((*SubnetPortReconciler).Reconcile, func(r *SubnetPortReconciler, ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
		reqList = append(reqList, req)
		return common.ResultNormal, nil
	})
	err := r.RestoreReconcile()
	assert.Nil(t, err)
	assert.Equal(t, reconcile.Request{NamespacedName: types.NamespacedName{Name: "port-2", Namespace: "ns-1"}}, reqList[0])
	assert.Equal(t, reconcile.Request{NamespacedName: types.NamespacedName{Name: "port-4", Namespace: "ns-2"}}, reqList[1])
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
		isPublicSubnet bool
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
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService.SubnetPortStore), "GetVpcSubnetPortByUID",
					func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
						return nil, nil
					})
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
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService.SubnetPortStore), "GetVpcSubnetPortByUID",
					func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
						return nil, nil
					})
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
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService.SubnetPortStore), "GetVpcSubnetPortByUID",
					func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
						return &model.VpcSubnetPort{ExternalAddressBinding: &model.ExternalAddressBinding{ExternalIpAddress: ptr.To("192.168.0.2")}}, nil
					})
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
		{
			name: "IPv6SubnetPort",
			prepareFunc: func(r *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService.SubnetPortStore), "GetVpcSubnetPortByUID",
					func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
						return nil, nil
					})
				patches.ApplyMethodSeq(r.SubnetPortService, "GetAddressBindingBySubnetPort", []gomonkey.OutputCell{{
					Values: gomonkey.Params{&v1alpha1.AddressBinding{ObjectMeta: metav1.ObjectMeta{Name: "ab1", Namespace: "ns1"}}},
					Times:  1,
				}})
				patches.ApplyFunc(setAddressBindingStatus, func(client client.Client, ctx context.Context, ab *v1alpha1.AddressBinding, transitionTime metav1.Time, e error, ipAddress string) {
					assert.Equal(t, &v1alpha1.AddressBinding{ObjectMeta: metav1.ObjectMeta{Name: "ab1", Namespace: "ns1"}}, ab)
					assert.Equal(t, metav1.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), transitionTime)
					assert.Equal(t, errorAddressBindingIPv6NotSupported, e)
					assert.Equal(t, "", ipAddress)
				})
				return patches
			},
			args: args{
				subnetPort: &v1alpha1.SubnetPort{
					Spec: v1alpha1.SubnetPortSpec{
						InterfaceIPType: v1alpha1.IPAddressTypeIPv6,
					},
				},
				transitionTime: metav1.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
				e:              nil,
			},
		},
		{
			name: "PublicSubnetPort",
			prepareFunc: func(r *SubnetPortReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService.SubnetPortStore), "GetVpcSubnetPortByUID",
					func(s *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
						return nil, nil
					})
				patches.ApplyMethodSeq(r.SubnetPortService, "GetAddressBindingBySubnetPort", []gomonkey.OutputCell{{
					Values: gomonkey.Params{&v1alpha1.AddressBinding{ObjectMeta: metav1.ObjectMeta{Name: "ab1", Namespace: "ns1"}}},
					Times:  1,
				}})
				patches.ApplyFunc(setAddressBindingStatus, func(client client.Client, ctx context.Context, ab *v1alpha1.AddressBinding, transitionTime metav1.Time, e error, ipAddress string) {
					assert.Equal(t, &v1alpha1.AddressBinding{ObjectMeta: metav1.ObjectMeta{Name: "ab1", Namespace: "ns1"}}, ab)
					assert.Equal(t, metav1.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), transitionTime)
					assert.Equal(t, errorAddressBindingPublicSubnetNotSupported, e)
					assert.Equal(t, "", ipAddress)
				})
				return patches
			},
			args: args{
				subnetPort: &v1alpha1.SubnetPort{
					Spec: v1alpha1.SubnetPortSpec{
						InterfaceIPType: v1alpha1.IPAddressTypeIPv4,
					},
				},
				isPublicSubnet: true,
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
			setAddressBindingStatusBySubnetPort(r.Client, context.TODO(), tt.args.subnetPort, r.SubnetPortService, tt.args.transitionTime, tt.args.e, tt.args.isPublicSubnet)
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
							LastTransitionTime: metav1.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC),
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

func TestSubnetPortReconciler_StartController(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects().Build()
	vpcService := &vpc.VPCService{
		Service: servicecommon.Service{
			Client: fakeClient,
		},
	}
	mockIPAddressAllocationService := mock.MockIPAddressAllocationProvider{}
	subnetService := &subnet.SubnetService{
		Service: servicecommon.Service{
			Client: fakeClient,
		},
		SubnetStore: &subnet.SubnetStore{},
	}
	subnetPortService := &subnetport.SubnetPortService{
		Service: servicecommon.Service{
			Client: fakeClient,
		},
		SubnetPortStore:            nil,
		IpAddressAllocationService: &mockIPAddressAllocationService,
	}
	mockMgr := &MockManager{
		scheme: runtime.NewScheme(),
		client: fakeClient,
	}
	patches := gomonkey.ApplyFunc((*SubnetPortReconciler).setupWithManager, func(r *SubnetPortReconciler, mgr manager.Manager) error {
		return nil
	})
	patches.ApplyFunc(common.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context) error) {
	})
	patches.ApplyFunc((*SubnetPortReconciler).SetupFieldIndexers, func(r *SubnetPortReconciler, mgr manager.Manager) error {
		return nil
	})
	defer patches.Reset()
	r := NewSubnetPortReconciler(mockMgr, subnetPortService, subnetService, vpcService, &mockIPAddressAllocationService)
	err := r.StartController(mockMgr, nil)
	assert.Nil(t, err)
}

type MockManager struct {
	controllerruntime.Manager
	client client.Client
	scheme *runtime.Scheme
}

func (m *MockManager) GetClient() client.Client {
	return m.client
}

func (m *MockManager) GetScheme() *runtime.Scheme {
	return m.scheme
}

func (m *MockManager) GetAPIReader() client.Reader {
	return m.client
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

func TestSubnetPortReconciler_pollAllVIFs(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	fakewriter := fakeStatusWriter{}
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
	defer mockCtl.Finish()

	r := &SubnetPortReconciler{
		Client:            k8sClient,
		SubnetPortService: subnetPortService,
	}
	test := []struct {
		name        string
		prepareFunc func(*testing.T, *SubnetPortReconciler) *gomonkey.Patches
	}{
		{
			name: "ListSubnetPortError",
			prepareFunc: func(t *testing.T, r *SubnetPortReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("mock error")).Times(5)
				// / Expect that the function r.NSXClient.VifsClient.List wont' be called
				patches := gomonkey.ApplyFunc(
					(*subnetport.SubnetPortService).GetAllVIFs,
					func(_ *subnetport.SubnetPortService) (*subnetport.VifStore, error) {
						assert.Fail(t, "GetAllVIFs should not be called")
						return nil, nil
					},
				)
				return patches
			},
		},
		{
			name: "ListSubnetPortEmpty",
			prepareFunc: func(t *testing.T, r *SubnetPortReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					subnetportList := list.(*v1alpha1.SubnetPortList)
					subnetportList.Items = []v1alpha1.SubnetPort{}
					return nil
				})
				// / Expect that the function r.NSXClient.VifsClient.List wont' be called
				patches := gomonkey.ApplyFunc(
					(*subnetport.SubnetPortService).GetAllVIFs,
					func(_ *subnetport.SubnetPortService) (*subnetport.VifStore, error) {
						assert.Fail(t, "GetAllVIFs should not be called")
						return nil, nil
					},
				)
				return patches
			},
		},
		{
			name: "GetAllVIFsError",
			prepareFunc: func(t *testing.T, r *SubnetPortReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					subnetportList := list.(*v1alpha1.SubnetPortList)
					subnetportList.Items = []v1alpha1.SubnetPort{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-subnetport",
								Namespace: "test-namespace",
							},
							Status: v1alpha1.SubnetPortStatus{
								Attachment: v1alpha1.PortAttachment{
									ID: "test-attachment-id",
								},
							},
						},
					}
					return nil
				})
				patches := gomonkey.ApplyFunc(
					(*subnetport.SubnetPortService).GetAllVIFs,
					func(_ *subnetport.SubnetPortService) (*subnetport.VifStore, error) {
						return nil, fmt.Errorf("mock error")
					},
				)
				patches.ApplyFunc(
					(*subnetport.VifStore).GetMACByAttachmentID,
					func(_ *subnetport.VifStore, _ string) (string, error) {
						assert.Fail(t, "GetMACByAttachmentID should not be called")
						return "", nil
					},
				)
				return patches
			},
		},
		{
			name: "GetMACByAttachmentIDError",
			prepareFunc: func(t *testing.T, r *SubnetPortReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					subnetportList := list.(*v1alpha1.SubnetPortList)
					subnetportList.Items = []v1alpha1.SubnetPort{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-subnetport",
								Namespace: "test-namespace",
							},
							Status: v1alpha1.SubnetPortStatus{
								Attachment: v1alpha1.PortAttachment{
									ID: "test-attachment-id",
								},
							},
						},
					}
					return nil
				})
				patches := gomonkey.ApplyFunc(
					(*subnetport.SubnetPortService).GetAllVIFs,
					func(_ *subnetport.SubnetPortService) (*subnetport.VifStore, error) {
						vifStore := subnetport.NewVifStore()
						return &vifStore, nil
					},
				)
				patches.ApplyFunc(
					(*subnetport.VifStore).GetMACByAttachmentID,
					func(_ *subnetport.VifStore, _ string) (string, error) {
						return "", fmt.Errorf("mock error")
					},
				)
				k8sClient.EXPECT().Status().Times(0)
				return patches
			},
		},
		{
			name: "Success",
			prepareFunc: func(t *testing.T, r *SubnetPortReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					subnetportList := list.(*v1alpha1.SubnetPortList)
					subnetportList.Items = []v1alpha1.SubnetPort{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-subnetport",
								Namespace: "test-namespace",
							},
							Status: v1alpha1.SubnetPortStatus{
								Attachment: v1alpha1.PortAttachment{
									ID: "test-attachment-id",
								},
							},
						},
					}
					return nil
				})
				patches := gomonkey.ApplyFunc(
					(*subnetport.SubnetPortService).GetAllVIFs,
					func(_ *subnetport.SubnetPortService) (*subnetport.VifStore, error) {
						vifStore := subnetport.NewVifStore()
						return &vifStore, nil
					},
				)
				patches.ApplyFunc(
					(*subnetport.VifStore).GetMACByAttachmentID,
					func(_ *subnetport.VifStore, _ string) (string, error) {
						return "test-mac-id", nil
					},
				)
				fakewriter = fakeStatusWriter{
					t:           t,
					validateObj: true,
					expectObj: &v1alpha1.SubnetPort{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-subnetport",
							Namespace: "test-namespace",
						},
						Status: v1alpha1.SubnetPortStatus{
							Attachment: v1alpha1.PortAttachment{
								ID: "test-attachment-id",
							},
							NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
								MACAddress: "test-mac-id",
							},
						},
					},
				}
				k8sClient.EXPECT().Status().Times(1).Return(&fakewriter)
				return patches
			},
		},
	}
	for _, tt := range test {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.prepareFunc(t, r)
			defer patches.Reset()
			r.pollAllVIFs()
		})
	}
}

func TestSubnetAssociatedResourceIndexFunc(t *testing.T) {
	tests := []struct {
		name     string
		obj      interface{}
		expected []string
	}{
		{
			name: "Successful indexing - Subnet has the annotation",
			obj: &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-subnet",
					Annotations: map[string]string{
						servicecommon.AnnotationAssociatedResource: "default:ns-1:subnet-1",
					},
				},
			},
			expected: []string{"default:ns-1:subnet-1"},
		},
		{
			name: "Empty indexing - Subnet exists but lacks the annotation",
			obj: &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-subnet-no-anno",
					Annotations: map[string]string{"other-key": "other-val"},
				},
			},
			expected: []string{},
		},
		{
			name: "Empty indexing - Subnet has nil annotations",
			obj: &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-subnet-nil-anno",
				},
			},
			expected: []string{},
		},
		{
			name: "Invalid object type",
			obj: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{Name: "not-a-subnet"},
			},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := subnetAssociatedResourceIndexFunc(tt.obj.(client.Object))
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSubnetPortReconciler_UpdateSubnetPortIPType(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	service := &subnetport.SubnetPortService{}
	r := &SubnetPortReconciler{
		Client:            k8sClient,
		SubnetPortService: service,
		restoreMode:       false,
	}
	r.StatusUpdater = common.NewStatusUpdater(k8sClient, nil, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")
	ctx := context.Background()

	// Case 1: No fields are empty -> No spec change, no k8s client update should be called
	t.Run("no spec changes required", func(t *testing.T) {
		subMockCtl := gomock.NewController(t)
		defer subMockCtl.Finish()
		subClient := mock_client.NewMockClient(subMockCtl)
		r.Client = subClient

		subnetPort := &v1alpha1.SubnetPort{
			Spec: v1alpha1.SubnetPortSpec{
				InterfaceIPType:        v1alpha1.IPAddressTypeIPv4,
				StaticIPAllocationType: v1alpha1.StaticIPAllocationType(v1alpha1.IPAddressTypeIPv4),
			},
		}
		nsxSubnet := &model.VpcSubnet{}

		err := r.updateSubnetPortIPType(ctx, subnetPort, v1alpha1.IPAddressTypeIPv6, nsxSubnet)
		assert.NoError(t, err)
		assert.Equal(t, v1alpha1.IPAddressTypeIPv4, subnetPort.Spec.InterfaceIPType)
	})

	// Case 2: InterfaceIPType is empty -> should default to interfaceIPType passed in
	t.Run("default empty InterfaceIPType", func(t *testing.T) {
		subMockCtl := gomock.NewController(t)
		defer subMockCtl.Finish()
		subClient := mock_client.NewMockClient(subMockCtl)
		r.Client = subClient

		subnetPort := &v1alpha1.SubnetPort{
			Spec: v1alpha1.SubnetPortSpec{
				InterfaceIPType:        "",
				StaticIPAllocationType: v1alpha1.StaticIPAllocationType(v1alpha1.IPAddressTypeIPv4),
			},
		}
		nsxSubnet := &model.VpcSubnet{}

		subClient.EXPECT().Update(ctx, subnetPort).Return(nil)

		err := r.updateSubnetPortIPType(ctx, subnetPort, v1alpha1.IPAddressTypeIPv4IPv6, nsxSubnet)
		assert.NoError(t, err)
		assert.Equal(t, v1alpha1.IPAddressTypeIPv4IPv6, subnetPort.Spec.InterfaceIPType)
	})

	// Case 3: StaticIPAllocationType is empty AND Static IP Allocation is ENABLED on Subnet
	t.Run("default empty StaticIPAllocationType with static IP enabled", func(t *testing.T) {
		subMockCtl := gomock.NewController(t)
		defer subMockCtl.Finish()
		subClient := mock_client.NewMockClient(subMockCtl)
		r.Client = subClient

		subnetPort := &v1alpha1.SubnetPort{
			Spec: v1alpha1.SubnetPortSpec{
				InterfaceIPType:        v1alpha1.IPAddressTypeIPv4,
				StaticIPAllocationType: "",
			},
		}

		subClient.EXPECT().Update(ctx, subnetPort).Return(nil)

		err := r.updateSubnetPortIPType(ctx, subnetPort, v1alpha1.IPAddressTypeIPv4, &model.VpcSubnet{
			AdvancedConfig: &model.SubnetAdvancedConfig{
				StaticIpAllocation: &model.StaticIpAllocation{
					Enabled: ptr.To(true),
				},
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, v1alpha1.StaticIPAllocationType(v1alpha1.IPAddressTypeIPv4), subnetPort.Spec.StaticIPAllocationType)
	})

	// Case 4: StaticIPAllocationType is empty AND Static IP Allocation is DISABLED on Subnet
	t.Run("default empty StaticIPAllocationType with static IP disabled", func(t *testing.T) {
		subMockCtl := gomock.NewController(t)
		defer subMockCtl.Finish()
		subClient := mock_client.NewMockClient(subMockCtl)
		r.Client = subClient

		subnetPort := &v1alpha1.SubnetPort{
			Spec: v1alpha1.SubnetPortSpec{
				InterfaceIPType:        v1alpha1.IPAddressTypeIPv6,
				StaticIPAllocationType: "",
			},
		}

		subClient.EXPECT().Update(ctx, subnetPort).Return(nil)

		err := r.updateSubnetPortIPType(ctx, subnetPort, v1alpha1.IPAddressTypeIPv6, &model.VpcSubnet{})
		assert.NoError(t, err)
		assert.Equal(t, v1alpha1.StaticIPAllocationTypeNone, subnetPort.Spec.StaticIPAllocationType)
	})
}

func TestSubnetPortReconciler_updateSubnetPortStatusConditions(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	tests := []struct {
		name             string
		subnetPort       *v1alpha1.SubnetPort
		latestSubnetPort *v1alpha1.SubnetPort
		newConditions    []v1alpha1.Condition
		expectErr        bool
		expectConditions bool
	}{
		{
			name: "conditions updated",
			subnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{Name: "sp-1", Namespace: "ns-1"},
				Status:     v1alpha1.SubnetPortStatus{},
			},
			latestSubnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{Name: "sp-1", Namespace: "ns-1"},
				Status: v1alpha1.SubnetPortStatus{
					Conditions: []v1alpha1.Condition{{Type: v1alpha1.Ready, Status: corev1.ConditionTrue}},
				},
			},
			newConditions:    []v1alpha1.Condition{{Type: v1alpha1.Ready, Status: corev1.ConditionFalse}},
			expectErr:        false,
			expectConditions: true,
		},
		{
			name: "conditions unchanged",
			subnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{Name: "sp-2", Namespace: "ns-1"},
				Status: v1alpha1.SubnetPortStatus{
					Conditions: []v1alpha1.Condition{{Type: v1alpha1.Ready, Status: corev1.ConditionTrue}},
				},
			},
			latestSubnetPort: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{Name: "sp-2", Namespace: "ns-1"},
				Status: v1alpha1.SubnetPortStatus{
					Conditions: []v1alpha1.Condition{{Type: v1alpha1.Ready, Status: corev1.ConditionTrue}},
				},
			},
			newConditions:    []v1alpha1.Condition{{Type: v1alpha1.Ready, Status: corev1.ConditionTrue}},
			expectErr:        false,
			expectConditions: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, key types.NamespacedName, obj *v1alpha1.SubnetPort, opts ...client.GetOption) error {
				obj.Status = tt.latestSubnetPort.Status
				return nil
			})
			if tt.expectConditions {
				fakewriter := fakeStatusWriter{}
				k8sClient.EXPECT().Status().Return(fakewriter)
			}
			updateSubnetPortStatusConditions(k8sClient, context.TODO(), tt.subnetPort, tt.newConditions)
		})
	}
}

func TestSubnetPortReconciler_isPortReused(t *testing.T) {
	r := &SubnetPortReconciler{}

	// nil SubnetPort
	assert.False(t, r.isPortReused(nil))

	// SubnetPort with empty attachment ID
	sp := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sp-1",
			Namespace: "ns-1",
			UID:       types.UID("uid-1"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
		},
		Status: v1alpha1.SubnetPortStatus{},
	}
	assert.False(t, r.isPortReused(sp))

	sp.Status.Attachment.ID = "attach-1"

	// Non-cpVM SubnetPort should return false even if attachment ID is set
	nonCpvmSP := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sp-1",
			Namespace: "ns-1",
			UID:       types.UID("uid-1"),
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-1",
			},
		},
	}
	assert.False(t, r.isPortReused(nonCpvmSP))

	// SubnetPortService is nil
	assert.False(t, r.isPortReused(sp))

	// SubnetPortStore is nil
	r.SubnetPortService = &subnetport.SubnetPortService{
		Service: servicecommon.Service{
			NSXConfig: &config.NSXOperatorConfig{
				K8sConfig: &config.K8sConfig{
					SupportReusePort: true,
				},
			},
		},
	}
	assert.False(t, r.isPortReused(sp))

	// Port not in store
	r.SubnetPortService.SubnetPortStore = &subnetport.SubnetPortStore{}
	var portToReturn *model.VpcSubnetPort
	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetSubnetPortByAttachmentID",
		func(_ *subnetport.SubnetPortService, attachID string) *model.VpcSubnetPort {
			return portToReturn
		})
	defer patches.Reset()
	assert.False(t, r.isPortReused(sp))

	// Port in store with matching UID, Name, Namespace
	portID := "port-1"
	parentPath := "/subnets/subnet-1"
	matchingPort := &model.VpcSubnetPort{
		Id:         &portID,
		ParentPath: &parentPath,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("uid-1")},
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("sp-1")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("ns-1")},
		},
	}
	portToReturn = matchingPort
	assert.False(t, r.isPortReused(sp))

	spWithCPVM := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sp-1",
			Namespace: "ns-1",
			UID:       types.UID("uid-1"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-1",
			},
		},
	}

	// Port in store with DIFFERENT CR UID -> reused!
	reusedPortDiffUID := &model.VpcSubnetPort{
		Id:         &portID,
		ParentPath: &parentPath,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("new-uid-2")},
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("new-sp-2")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("new-ns-2")},
		},
	}
	portToReturn = reusedPortDiffUID
	assert.True(t, r.isPortReused(spWithCPVM))

	// Port in store with same UID but DIFFERENT Name -> reused!
	reusedPortDiffName := &model.VpcSubnetPort{
		Id:         &portID,
		ParentPath: &parentPath,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("new-sp-2")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("ns-1")},
		},
	}
	portToReturn = reusedPortDiffName
	assert.True(t, r.isPortReused(spWithCPVM))

	// Port in store with TagScopeVMNamespace and different NS -> reused!
	reusedPortDiffNS := &model.VpcSubnetPort{
		Id:         &portID,
		ParentPath: &parentPath,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("sp-1")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("diff-ns")},
		},
	}
	portToReturn = reusedPortDiffNS
	assert.True(t, r.isPortReused(spWithCPVM))

	// In restoreMode, isPortReused should still return true!
	r.restoreMode = true
	assert.True(t, r.isPortReused(spWithCPVM))
	r.restoreMode = false

	// Even when SupportReusePort is disabled, existing backend metadata must still be respected
	r.SubnetPortService.NSXConfig.K8sConfig.SupportReusePort = false
	assert.True(t, r.isPortReused(spWithCPVM))
	r.SubnetPortService.NSXConfig.K8sConfig.SupportReusePort = true

	// Destination CR reusing old port should NOT be considered reused when port tags belong to source CR
	destSP := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "new-sp-2",
			Namespace: "new-ns-2",
			UID:       types.UID("new-uid-2"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
			Annotations: map[string]string{
				servicecommon.AnnotationReusePort: "ns-1/sp-1",
			},
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-1",
			},
		},
	}
	portToReturn = matchingPort // has sp-1 / ns-1 / uid-1 tags
	assert.False(t, r.isPortReused(destSP))
}

func TestSubnetPortReconciler_Reconcile_SkipWhenPortReused(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint: "vmc-enforcementpoint",
		},
		K8sConfig: &config.K8sConfig{
			SupportReusePort: true,
		},
	}
	r := &SubnetPortReconciler{
		Client:        k8sClient,
		StatusUpdater: common.NewStatusUpdater(k8sClient, nsxConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort"),
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	sp := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sp-1",
			Namespace: "ns-1",
			UID:       types.UID("old-uid"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-1",
			},
		},
	}

	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ client.ObjectKey, obj *v1alpha1.SubnetPort, _ ...client.GetOption) error {
			*obj = *sp
			return nil
		}).AnyTimes()
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	patches := gomonkey.ApplyFunc((*SubnetPortReconciler).isPortReused,
		func(_ *SubnetPortReconciler, _ *v1alpha1.SubnetPort) bool {
			return true
		})
	defer patches.Reset()

	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "ns-1",
			Name:      "sp-1",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, common.ResultNormal, res)
}

func TestSubnetPortReconciler_isPortReferencedByReusePort(t *testing.T) {
	nsxConfig := &config.NSXOperatorConfig{
		K8sConfig: &config.K8sConfig{
			SupportReusePort: true,
		},
	}
	r := &SubnetPortReconciler{
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	// nil port
	assert.False(t, r.isPortReferencedByReusePort(nil, nil))

	portID := "port-1"
	port := &model.VpcSubnetPort{
		Id: &portID,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("old-vm")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("kube-system")},
		},
	}

	// Port without LabelCPVM tag (upgrade case) but referenced by active cpVM CR via reuse-port
	activeCRList := []v1alpha1.SubnetPort{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "new-vm",
				Namespace: "user-ns",
				Labels: map[string]string{
					servicecommon.LabelCPVM: "true",
				},
				Annotations: map[string]string{
					servicecommon.AnnotationReusePort: "kube-system/old-vm",
				},
			},
		},
	}
	assert.True(t, r.isPortReferencedByReusePort(port, activeCRList))

	// Referenced by attachment ID
	portWithAttach := &model.VpcSubnetPort{
		Id: &portID,
		Attachment: &model.PortAttachment{
			Id: ptr.To("attach-123"),
		},
	}
	activeCRWithAttach := []v1alpha1.SubnetPort{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "new-vm",
				Namespace: "user-ns",
				Labels: map[string]string{
					servicecommon.LabelCPVM: "true",
				},
			},
			Status: v1alpha1.SubnetPortStatus{
				Attachment: v1alpha1.PortAttachment{
					ID: "attach-123",
				},
			},
		},
	}
	assert.True(t, r.isPortReferencedByReusePort(portWithAttach, activeCRWithAttach))

	// Active CR has reuse-port but is NOT a cpVM -> should return false
	nonCPVMList := []v1alpha1.SubnetPort{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "regular-vm",
				Namespace: "user-ns",
				Annotations: map[string]string{
					servicecommon.AnnotationReusePort: "kube-system/old-vm",
				},
			},
		},
	}
	assert.False(t, r.isPortReferencedByReusePort(port, nonCPVMList))

	// Active CR has reuse-port but has DeletionTimestamp set -> should return false
	now := metav1.Now()
	terminatingCRList := []v1alpha1.SubnetPort{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "new-vm",
				Namespace:         "user-ns",
				DeletionTimestamp: &now,
				Labels: map[string]string{
					servicecommon.LabelCPVM: "true",
				},
				Annotations: map[string]string{
					servicecommon.AnnotationReusePort: "kube-system/old-vm",
				},
			},
		},
	}
	assert.False(t, r.isPortReferencedByReusePort(port, terminatingCRList))

	// Active CR has matching attachment ID but has DeletionTimestamp set -> should return false
	terminatingCRWithAttach := []v1alpha1.SubnetPort{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "new-vm",
				Namespace:         "user-ns",
				DeletionTimestamp: &now,
				Labels: map[string]string{
					servicecommon.LabelCPVM: "true",
				},
			},
			Status: v1alpha1.SubnetPortStatus{
				Attachment: v1alpha1.PortAttachment{
					ID: "attach-123",
				},
			},
		},
	}
	assert.False(t, r.isPortReferencedByReusePort(portWithAttach, terminatingCRWithAttach))

	// Superseded source CR with same attachment ID should NOT prevent deletion of port
	sourceCRWithSameAttach := v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "old-vm",
			Namespace: "kube-system",
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-123",
			},
		},
	}
	// Backend port in store has destination CR's UID (port was taken over by destination)
	destPortWithAttach := &model.VpcSubnetPort{
		Id: &portID,
		Attachment: &model.PortAttachment{
			Id: ptr.To("attach-123"),
		},
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("new-uid")},
		},
	}
	r.SubnetPortService = &subnetport.SubnetPortService{
		Service: servicecommon.Service{
			NSXConfig: nsxConfig,
		},
		SubnetPortStore: &subnetport.SubnetPortStore{},
	}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetSubnetPortByAttachmentID",
		func(_ *subnetport.SubnetPortService, attachID string) *model.VpcSubnetPort {
			if attachID == "attach-123" {
				return destPortWithAttach
			}
			return nil
		})
	defer patches.Reset()

	// List contains only the superseded source CR (destination CR was deleted)
	assert.False(t, r.isPortReferencedByReusePort(destPortWithAttach, []v1alpha1.SubnetPort{sourceCRWithSameAttach}))

	// When SupportReusePort is disabled, CRs with reuse-port annotation do not reference the port
	r.SubnetPortService.NSXConfig.K8sConfig.SupportReusePort = false
	assert.False(t, r.isPortReferencedByReusePort(port, activeCRList))
	r.SubnetPortService.NSXConfig.K8sConfig.SupportReusePort = true
}

func TestSubnetPortReconciler_isPortSuperseded(t *testing.T) {
	nsxConfig := &config.NSXOperatorConfig{
		K8sConfig: &config.K8sConfig{
			SupportReusePort: true,
		},
	}
	r := &SubnetPortReconciler{
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	superseded, err := r.isPortSuperseded(nil, nil)
	assert.NoError(t, err)
	assert.False(t, superseded)

	sourceSP := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "old-vm",
			Namespace: "kube-system",
			UID:       types.UID("old-uid"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-1",
			},
		},
	}

	// Case 1: isPortReused returns true (backend port in store has different UID)
	portID := "port-1"
	parentPath := "/subnets/subnet-1"
	reusedPortDiffUID := &model.VpcSubnetPort{
		Id:         &portID,
		ParentPath: &parentPath,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("new-uid")},
		},
	}
	backendPort := reusedPortDiffUID
	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetSubnetPortByAttachmentID",
		func(_ *subnetport.SubnetPortService, attachID string) *model.VpcSubnetPort {
			return backendPort
		})
	defer patches.Reset()
	superseded, err = r.isPortSuperseded(sourceSP, nil)
	assert.NoError(t, err)
	assert.True(t, superseded)

	// Case 2: Backend port not found, destination CR in subnetPorts references sourceSP via reuse-port
	backendPort = nil

	// Case 2a: Destination CR has NO IP status yet -> sourceSP is NOT superseded (premature exclusion prevented)
	destCRListNoIP := []v1alpha1.SubnetPort{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "new-vm",
				Namespace: "user-ns",
				Labels: map[string]string{
					servicecommon.LabelCPVM: "true",
				},
				Annotations: map[string]string{
					servicecommon.AnnotationReusePort: "kube-system/old-vm",
				},
			},
		},
	}
	superseded, err = r.isPortSuperseded(sourceSP, destCRListNoIP)
	assert.NoError(t, err)
	assert.False(t, superseded)

	// Case 2b: Destination CR has IP status -> sourceSP is superseded
	destCRListWithIP := []v1alpha1.SubnetPort{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "new-vm",
				Namespace: "user-ns",
				Labels: map[string]string{
					servicecommon.LabelCPVM: "true",
				},
				Annotations: map[string]string{
					servicecommon.AnnotationReusePort: "kube-system/old-vm",
				},
			},
			Status: v1alpha1.SubnetPortStatus{
				NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
					IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
						{IPAddress: "10.0.0.3/28", Gateway: "10.0.0.1"},
					},
				},
			},
		},
	}
	superseded, err = r.isPortSuperseded(sourceSP, destCRListWithIP)
	assert.NoError(t, err)
	assert.True(t, superseded)

	// Case 3: Destination CR has DeletionTimestamp set -> not superseded
	now := metav1.Now()
	terminatingDestCRList := []v1alpha1.SubnetPort{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "new-vm",
				Namespace:         "user-ns",
				DeletionTimestamp: &now,
				Labels: map[string]string{
					servicecommon.LabelCPVM: "true",
				},
				Annotations: map[string]string{
					servicecommon.AnnotationReusePort: "kube-system/old-vm",
				},
			},
			Status: v1alpha1.SubnetPortStatus{
				NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
					IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
						{IPAddress: "10.0.0.3/28", Gateway: "10.0.0.1"},
					},
				},
			},
		},
	}
	superseded, err = r.isPortSuperseded(sourceSP, terminatingDestCRList)
	assert.NoError(t, err)
	assert.False(t, superseded)

	// Case 4: Non-CPVM CR -> not superseded
	nonCPVMSP := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "old-vm",
			Namespace: "kube-system",
		},
	}
	superseded, err = r.isPortSuperseded(nonCPVMSP, destCRListWithIP)
	assert.NoError(t, err)
	assert.False(t, superseded)

	// Case 5: Dynamic SubnetPort List lookup failure -> returns error to propagate to Reconcile
	mockCtlList := gomock.NewController(t)
	defer mockCtlList.Finish()
	mockClientList := mock_client.NewMockClient(mockCtlList)
	listErr := errors.New("failed to list subnetports")
	mockClientList.EXPECT().List(gomock.Any(), gomock.Any()).Return(listErr)
	rWithClient := &SubnetPortReconciler{
		Client: mockClientList,
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}
	superseded, err = rWithClient.isPortSuperseded(sourceSP, nil)
	assert.Error(t, err)
	assert.Equal(t, listErr, err)
	assert.False(t, superseded)

	// Case 6: When destination CR has IP status from takeover and ports are not distinct,
	// source CR is superseded even when SupportReusePort is disabled (restore / recreation scenario).
	r.SubnetPortService.NSXConfig.K8sConfig.SupportReusePort = false
	superseded, err = r.isPortSuperseded(sourceSP, destCRListWithIP)
	assert.NoError(t, err)
	assert.True(t, superseded, "Source CR must be superseded when destination CR has completed takeover with IP status even if SupportReusePort is disabled")

	// Case 7: When SupportReusePort is disabled but backend migration metadata exists (origin_cr_uid points to source),
	// source CR is superseded by the active destination owner.
	migratedPortID := "port-migrated-1"
	parentPathMigrated := "/subnets/subnet-1"
	migratedPort := &model.VpcSubnetPort{
		Id:         &migratedPortID,
		ParentPath: &parentPathMigrated,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("new-uid")},
			{Scope: ptr.To(servicecommon.TagScopeOriginCRUID), Tag: ptr.To("old-uid")},
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("new-vm")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("user-ns")},
			{Scope: ptr.To(servicecommon.LabelCPVM), Tag: ptr.To("true")},
		},
	}
	patchesOrigin := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetSubnetPortByOriginUID",
		func(_ *subnetport.SubnetPortService, uid types.UID) *model.VpcSubnetPort {
			if uid == "old-uid" {
				return migratedPort
			}
			return nil
		})
	defer patchesOrigin.Reset()

	destCRListWithUID := []v1alpha1.SubnetPort{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "new-vm",
				Namespace: "user-ns",
				UID:       types.UID("new-uid"),
				Labels: map[string]string{
					servicecommon.LabelCPVM: "true",
				},
				Annotations: map[string]string{
					servicecommon.AnnotationReusePort: "kube-system/old-vm",
				},
			},
			Status: v1alpha1.SubnetPortStatus{
				NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
					IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
						{IPAddress: "10.0.0.3/28", Gateway: "10.0.0.1"},
					},
				},
			},
		},
	}
	superseded, err = r.isPortSuperseded(sourceSP, destCRListWithUID)
	assert.NoError(t, err)
	assert.True(t, superseded, "Source CR must be superseded when backend port carries completed migration origin_cr_uid")

	// Case 8: When both CRs demonstrably own distinct backend ports in cache,
	// destination IP status is not evidence of takeover (even with SupportReusePort enabled).
	r.SubnetPortService.NSXConfig.K8sConfig.SupportReusePort = true
	patchesOrigin.Reset()

	portSourceID := "port-source"
	sourcePort := &model.VpcSubnetPort{
		Id: &portSourceID,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("old-uid")},
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("old-vm")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("kube-system")},
			{Scope: ptr.To(servicecommon.LabelCPVM), Tag: ptr.To("true")},
		},
		Attachment: &model.PortAttachment{
			Id: ptr.To("attach-1"),
		},
	}
	portDestID := "port-dest"
	destPortFresh := &model.VpcSubnetPort{
		Id: &portDestID,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("new-uid")},
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("new-vm")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("user-ns")},
			{Scope: ptr.To(servicecommon.LabelCPVM), Tag: ptr.To("true")},
		},
		Attachment: &model.PortAttachment{
			Id: ptr.To("attach-dest"),
		},
	}
	patchesDistinct := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetSubnetPortByAttachmentID",
		func(_ *subnetport.SubnetPortService, attachID string) *model.VpcSubnetPort {
			if attachID == "attach-1" {
				return sourcePort
			}
			if attachID == "attach-dest" {
				return destPortFresh
			}
			return nil
		})
	defer patchesDistinct.Reset()

	destCRWithDistinctAttach := []v1alpha1.SubnetPort{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "new-vm",
				Namespace: "user-ns",
				UID:       types.UID("new-uid"),
				Labels: map[string]string{
					servicecommon.LabelCPVM: "true",
				},
				Annotations: map[string]string{
					servicecommon.AnnotationReusePort: "kube-system/old-vm",
				},
			},
			Status: v1alpha1.SubnetPortStatus{
				Attachment: v1alpha1.PortAttachment{
					ID: "attach-dest",
				},
				NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
					IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
						{IPAddress: "10.0.0.3/28", Gateway: "10.0.0.1"},
					},
				},
			},
		},
	}
	superseded, err = r.isPortSuperseded(sourceSP, destCRWithDistinctAttach)
	assert.NoError(t, err)
	assert.False(t, superseded, "Source CR must NOT be superseded when both CRs demonstrably own distinct backend ports")

	r.SubnetPortService.NSXConfig.K8sConfig.SupportReusePort = false
	superseded, err = r.isPortSuperseded(sourceSP, destCRWithDistinctAttach)
	assert.NoError(t, err)
	assert.False(t, superseded, "Source CR must NOT be superseded when both CRs demonstrably own distinct backend ports even if SupportReusePort is disabled")
	r.SubnetPortService.NSXConfig.K8sConfig.SupportReusePort = true
}

func TestSubnetPortReconciler_Reconcile_SupersededLookupErrorRequeues(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint: "vmc-enforcementpoint",
		},
		K8sConfig: &config.K8sConfig{
			SupportReusePort: true,
		},
	}
	r := &SubnetPortReconciler{
		Client:        k8sClient,
		StatusUpdater: common.NewStatusUpdater(k8sClient, nsxConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort"),
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	sourceSP := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "old-vm",
			Namespace: "kube-system",
			UID:       types.UID("old-uid"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-1",
			},
		},
	}

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "kube-system", Name: "old-vm"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj *v1alpha1.SubnetPort, _ ...client.GetOption) error {
			sourceSP.DeepCopyInto(obj)
			return nil
		}).AnyTimes()

	listErr := errors.New("transient apiserver connection error")
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(listErr).Times(1)

	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "kube-system",
			Name:      "old-vm",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.Error(t, err)
	assert.Equal(t, listErr, err)
	assert.Equal(t, common.ResultRequeue, res)
}

func TestSubnetPortReconciler_RestoreReconcile_ExcludesSupersededAndTerminating(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	nsxConfig := &config.NSXOperatorConfig{
		K8sConfig: &config.K8sConfig{
			SupportReusePort: true,
		},
	}
	r := &SubnetPortReconciler{
		Client: k8sClient,
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	// NSX store has port for destination CR (dest-uid) and normal-uid
	patches := gomonkey.ApplyFunc((*servicecommon.ResourceStore).ListIndexFuncValues, func(s *servicecommon.ResourceStore, key string) sets.Set[string] {
		return sets.New[string]("dest-uid", "normal-uid")
	})
	defer patches.Reset()

	now := metav1.Now()
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		subnetportList := list.(*v1alpha1.SubnetPortList)
		subnetportList.Items = []v1alpha1.SubnetPort{
			// Superseded source CR whose port was migrated to new-vm
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "source-vm",
					Namespace: "kube-system",
					UID:       "source-uid",
					Labels: map[string]string{
						servicecommon.LabelCPVM: "true",
					},
				},
				Status: v1alpha1.SubnetPortStatus{
					Attachment: v1alpha1.PortAttachment{
						ID: "attach-migrated",
					},
					NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
						IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
							{IPAddress: "10.0.0.3/28", Gateway: "10.0.0.1"},
						},
					},
				},
			},
			// Destination CR that reused source-vm's port
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-vm",
					Namespace: "user-ns",
					UID:       "dest-uid",
					Labels: map[string]string{
						servicecommon.LabelCPVM: "true",
					},
					Annotations: map[string]string{
						servicecommon.AnnotationReusePort: "kube-system/source-vm",
					},
				},
				Status: v1alpha1.SubnetPortStatus{
					Attachment: v1alpha1.PortAttachment{
						ID: "attach-migrated",
					},
					NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
						IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
							{IPAddress: "10.0.0.3/28", Gateway: "10.0.0.1"},
						},
					},
				},
			},
			// Terminating CR
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "terminating-vm",
					Namespace:         "user-ns",
					UID:               "term-uid",
					DeletionTimestamp: &now,
				},
				Status: v1alpha1.SubnetPortStatus{
					NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
						IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
							{IPAddress: "10.0.0.4/28", Gateway: "10.0.0.1"},
						},
					},
				},
			},
			// Normal SubnetPort CR needing restore (status has IP, not in NSX store)
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "need-restore-vm",
					Namespace: "user-ns",
					UID:       "restore-uid",
				},
				Status: v1alpha1.SubnetPortStatus{
					NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
						IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
							{IPAddress: "10.0.0.5/28", Gateway: "10.0.0.1"},
						},
					},
				},
			},
		}
		return nil
	})

	// Mock GetSubnetPortByAttachmentID to return port with dest-uid
	portID := "port-migrated"
	parentPath := "/subnets/subnet-1"
	reusedPort := &model.VpcSubnetPort{
		Id:         &portID,
		ParentPath: &parentPath,
		Attachment: &model.PortAttachment{
			Id: ptr.To("attach-migrated"),
		},
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("dest-uid")},
		},
	}
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetSubnetPortByAttachmentID",
		func(_ *subnetport.SubnetPortService, attachID string) *model.VpcSubnetPort {
			if attachID == "attach-migrated" {
				return reusedPort
			}
			return nil
		})
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService.SubnetPortStore), "GetByIndex",
		func(_ *subnetport.SubnetPortStore, key string, value string) []*model.VpcSubnetPort {
			if key == servicecommon.TagScopeSubnetPortCRUID && value == "dest-uid" {
				return []*model.VpcSubnetPort{reusedPort}
			}
			return nil
		})

	reqList := []reconcile.Request{}
	patches.ApplyFunc((*SubnetPortReconciler).Reconcile, func(r *SubnetPortReconciler, ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
		reqList = append(reqList, req)
		return common.ResultNormal, nil
	})

	err := r.RestoreReconcile()
	assert.Nil(t, err)

	// Verify only need-restore-vm was restored; source-vm and terminating-vm were excluded!
	assert.Len(t, reqList, 1)
	assert.Equal(t, reconcile.Request{NamespacedName: types.NamespacedName{Name: "need-restore-vm", Namespace: "user-ns"}}, reqList[0])
}

func TestSubnetPortReconciler_RestoreReconcile_PreMigrationTagsRestoresDestination(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	nsxConfig := &config.NSXOperatorConfig{
		K8sConfig: &config.K8sConfig{
			SupportReusePort: true,
		},
	}
	r := &SubnetPortReconciler{
		Client: k8sClient,
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	// NSX store has port with pre-migration source tags (source-uid), dest-uid is NOT in NSX store yet
	patches := gomonkey.ApplyFunc((*servicecommon.ResourceStore).ListIndexFuncValues, func(s *servicecommon.ResourceStore, key string) sets.Set[string] {
		return sets.New[string]("source-uid")
	})
	defer patches.Reset()

	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		subnetportList := list.(*v1alpha1.SubnetPortList)
		subnetportList.Items = []v1alpha1.SubnetPort{
			// Pre-migration source CR
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "source-vm",
					Namespace: "kube-system",
					UID:       "source-uid",
					Labels: map[string]string{
						servicecommon.LabelCPVM: "true",
					},
				},
				Status: v1alpha1.SubnetPortStatus{
					Attachment: v1alpha1.PortAttachment{
						ID: "attach-migrated",
					},
					NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
						IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
							{IPAddress: "10.0.0.3/28", Gateway: "10.0.0.1"},
						},
					},
				},
			},
			// Destination CR reusing source-vm's port, with valid IP status
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-vm",
					Namespace: "user-ns",
					UID:       "dest-uid",
					Labels: map[string]string{
						servicecommon.LabelCPVM: "true",
					},
					Annotations: map[string]string{
						servicecommon.AnnotationReusePort: "kube-system/source-vm",
					},
				},
				Status: v1alpha1.SubnetPortStatus{
					Attachment: v1alpha1.PortAttachment{
						ID: "attach-migrated",
					},
					NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
						IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
							{IPAddress: "10.0.0.3/28", Gateway: "10.0.0.1"},
						},
					},
				},
			},
		}
		return nil
	})

	// Mock GetSubnetPortByAttachmentID to return port with pre-migration source tags
	portID := "port-migrated"
	parentPath := "/subnets/subnet-1"
	preMigrationPort := &model.VpcSubnetPort{
		Id:         &portID,
		ParentPath: &parentPath,
		Attachment: &model.PortAttachment{
			Id: ptr.To("attach-migrated"),
		},
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("source-uid")},
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("source-vm")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("kube-system")},
		},
	}
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetSubnetPortByAttachmentID",
		func(_ *subnetport.SubnetPortService, attachID string) *model.VpcSubnetPort {
			if attachID == "attach-migrated" {
				return preMigrationPort
			}
			return nil
		})

	reqList := []reconcile.Request{}
	patches.ApplyFunc((*SubnetPortReconciler).Reconcile, func(r *SubnetPortReconciler, ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
		reqList = append(reqList, req)
		return common.ResultNormal, nil
	})

	err := r.RestoreReconcile()
	assert.Nil(t, err)

	// Verify new-vm is restored (ownership will be recovered), while source-vm is superseded and excluded!
	assert.Len(t, reqList, 1)
	assert.Equal(t, reconcile.Request{NamespacedName: types.NamespacedName{Name: "new-vm", Namespace: "user-ns"}}, reqList[0])
}

func TestSubnetPortReconciler_Reconcile_ReusePortRetryPreservesProtection(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint: "vmc-enforcementpoint",
		},
		K8sConfig: &config.K8sConfig{
			SupportReusePort: true,
		},
	}
	mockIPAlloc := &mock.MockIPAddressAllocationProvider{}
	mockSubnet := &mock.MockSubnetServiceProvider{}
	mockVPCService := &mock.MockVPCServiceProvider{}
	r := &SubnetPortReconciler{
		Client:                     k8sClient,
		StatusUpdater:              common.NewStatusUpdater(k8sClient, nsxConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort"),
		SubnetService:              mockSubnet,
		VPCService:                 mockVPCService,
		IpAddressAllocationService: mockIPAlloc,
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	destCR := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dest-vm",
			Namespace: "user-ns",
			UID:       types.UID("dest-uid"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
			Annotations: map[string]string{
				servicecommon.AnnotationReusePort: "kube-system/source-vm",
			},
		},
		Spec: v1alpha1.SubnetPortSpec{
			InterfaceIPType: v1alpha1.IPAddressTypeIPv4,
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-1",
			},
		},
	}

	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ client.ObjectKey, obj *v1alpha1.SubnetPort, _ ...client.GetOption) error {
			destCR.DeepCopyInto(obj)
			return nil
		}).AnyTimes()
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	portID := "port-1"
	parentPath := "/orgs/default/projects/default/vpcs/vpc-1/subnets/subnet-1"
	existingPort := &model.VpcSubnetPort{
		Id:         &portID,
		ParentPath: &parentPath,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("dest-uid")},
		},
	}

	// Store already contains existing port with dest-uid (simulating retry after realization check failed on attempt 1)
	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService.SubnetPortStore), "GetVpcSubnetPortByUID",
		func(_ *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
			if uid == "dest-uid" {
				return existingPort, nil
			}
			return nil, nil
		})
	defer patches.Reset()

	nsxSubnet := &model.VpcSubnet{
		Path: ptr.To(parentPath),
	}
	patches.ApplyMethod(reflect.TypeOf(mockSubnet), "GetSubnetByPath",
		func(_ *mock.MockSubnetServiceProvider, path string, inSharedSubnet bool) (*model.VpcSubnet, error) {
			return nsxSubnet, nil
		})

	patches.ApplyMethod(reflect.TypeOf(mockVPCService), "IsRADeactivatedByVPCPath",
		func(_ *mock.MockVPCServiceProvider, path string) (bool, error) {
			return false, nil
		})

	patches.ApplyFunc(common.IsSharedSubnetPath,
		func(_ context.Context, _ client.Client, _ string, _ string) (bool, error) {
			return false, nil
		})

	patches.ApplyPrivateMethod(r, "getParentIPAddressTypeFromCR",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (v1alpha1.IPAddressType, error) {
			return v1alpha1.IPAddressTypeIPv4, nil
		})

	patches.ApplyPrivateMethod(r, "updateSubnetPortIPType",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort, _ v1alpha1.IPAddressType, _ *model.VpcSubnet) error {
			return nil
		})

	patches.ApplyPrivateMethod(r, "updateSubnetStatusOnSubnetPort",
		func(_ *SubnetPortReconciler, _ *v1alpha1.SubnetPort, _ *model.VpcSubnet) error {
			return nil
		})

	patches.ApplyFunc(setAddressBindingStatusBySubnetPort,
		func(_ client.Client, _ context.Context, _ *v1alpha1.SubnetPort, _ *subnetport.SubnetPortService, _ metav1.Time, _ error, _ bool) {
		})

	patches.ApplyMethod(reflect.TypeOf(mockIPAlloc), "CreateIPAddressAllocationForAddressBinding",
		func(_ *mock.MockIPAddressAllocationProvider, _ *v1alpha1.AddressBinding, _ *v1alpha1.SubnetPort, _ bool) error {
			return nil
		})

	patches.ApplyMethod(reflect.TypeOf(mockIPAlloc), "DeleteIPAddressAllocationForAddressBinding",
		func(_ *mock.MockIPAddressAllocationProvider, _ metav1.Object) error {
			return nil
		})

	reusePortCalled := false
	createOrUpdateCalled := false
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "ReuseSubnetPort",
		func(_ *subnetport.SubnetPortService, sp *v1alpha1.SubnetPort, existing *model.VpcSubnetPort, subnet *model.VpcSubnet, labels *map[string]string) (*model.SegmentPortState, error) {
			reusePortCalled = true
			return nil, nsxutil.NewRealizeStateError("realized error on retry", 500000)
		})
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "CreateOrUpdateSubnetPort",
		func(_ *subnetport.SubnetPortService, _ interface{}, _ *model.VpcSubnet, _ string, _ *map[string]string, _ bool, _ bool, _ v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
			createOrUpdateCalled = true
			return nil, nil
		})
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "DeleteSubnetPort",
		func(_ *subnetport.SubnetPortService, _ *model.VpcSubnetPort) error {
			assert.Fail(t, "DeleteSubnetPort should never be called for reused port on retry realization error")
			return nil
		})

	k8sClient.EXPECT().Status().Return(fakeStatusWriter{t: t}).AnyTimes()

	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "user-ns",
			Name:      "dest-vm",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, common.ResultRequeueAfter60sec, res)
	assert.True(t, reusePortCalled, "ReuseSubnetPort must be invoked on retry")
	assert.False(t, createOrUpdateCalled, "CreateOrUpdateSubnetPort must NOT be invoked for reused port")
}

func TestSubnetPortReconciler_Reconcile_Delete_SkipsWhenPortReused(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint: "vmc-enforcementpoint",
		},
		K8sConfig: &config.K8sConfig{
			SupportReusePort: true,
		},
	}
	r := &SubnetPortReconciler{
		Client:        k8sClient,
		StatusUpdater: common.NewStatusUpdater(k8sClient, nsxConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort"),
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	now := metav1.Now()
	sp := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "sp-1",
			Namespace:         "ns-1",
			UID:               types.UID("old-uid"),
			DeletionTimestamp: &now,
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-1",
			},
		},
	}

	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			sp.DeepCopyInto(obj.(*v1alpha1.SubnetPort))
			return nil
		})
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	deletePortCalled := false
	patches := gomonkey.ApplyFunc((*SubnetPortReconciler).isPortReused,
		func(_ *SubnetPortReconciler, _ *v1alpha1.SubnetPort) bool {
			return true
		})
	defer patches.Reset()
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "DeleteSubnetPort",
		func(_ *subnetport.SubnetPortService, _ *model.VpcSubnetPort) error {
			deletePortCalled = true
			return nil
		})

	res, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "ns-1",
			Name:      "sp-1",
		},
	})
	assert.NoError(t, err)
	assert.Equal(t, common.ResultNormal, res)
	assert.False(t, deletePortCalled, "DeleteSubnetPort should NOT be called when deleting a source SubnetPort whose port was reused")
}

func TestSubnetPortReconciler_CollectGarbage_PreservesReusedPort(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint: "vmc-enforcementpoint",
		},
		K8sConfig: &config.K8sConfig{
			SupportReusePort: true,
		},
	}
	mockIPAlloc := &mock.MockIPAddressAllocationProvider{}
	r := &SubnetPortReconciler{
		Client:                     k8sClient,
		StatusUpdater:              common.NewStatusUpdater(k8sClient, nsxConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort"),
		IpAddressAllocationService: mockIPAlloc,
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	// Mock ListNSXSubnetPortIDForCR to return an orphan port in NSX (old cpVM port from kube-system)
	orphanPortID := "old-port-1"
	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "ListNSXSubnetPortIDForCR",
		func(_ *subnetport.SubnetPortService) sets.Set[string] {
			return sets.New[string](orphanPortID)
		})
	defer patches.Reset()

	// Mock ListSubnetPortIDsFromSubnetPorts to return empty set (old CR was deleted from K8s)
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "ListSubnetPortIDsFromSubnetPorts",
		func(_ *subnetport.SubnetPortService, _ []v1alpha1.SubnetPort) sets.Set[string] {
			return sets.New[string]()
		})

	patches.ApplyMethod(reflect.TypeOf(mockIPAlloc), "ListIPAddressAllocationWithAddressBinding",
		func(_ *mock.MockIPAddressAllocationProvider) []*model.VpcIpAddressAllocation {
			return nil
		})

	// The orphan port has no LabelCPVM tag (created by old operator version - upgrade case)
	orphanPort := &model.VpcSubnetPort{
		Id: &orphanPortID,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("old-vm-44")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("kube-system")},
		},
	}
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetSubnetPortByID",
		func(_ *subnetport.SubnetPortService, id string) *model.VpcSubnetPort {
			if id == orphanPortID {
				return orphanPort
			}
			return nil
		})

	// An active cpVM CR in a user namespace references the old port via reuse-port annotation
	activeCRList := &v1alpha1.SubnetPortList{
		Items: []v1alpha1.SubnetPort{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-vm-44",
					Namespace: "user-ns",
					Labels: map[string]string{
						servicecommon.LabelCPVM: "true",
					},
					Annotations: map[string]string{
						servicecommon.AnnotationReusePort: "kube-system/old-vm-44",
					},
				},
			},
		},
	}
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
			if spList, ok := list.(*v1alpha1.SubnetPortList); ok {
				*spList = *activeCRList
			}
			return nil
		}).AnyTimes()

	// DeleteSubnetPortById should NOT be called because the port is preserved
	deleteCalled := false
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "DeleteSubnetPortById",
		func(_ *subnetport.SubnetPortService, id string) error {
			deleteCalled = true
			return nil
		})

	patchesCollectAB := gomonkey.ApplyPrivateMethod(r, "collectAddressBindingGarbage", func(_ *SubnetPortReconciler, _ context.Context, _, _ *string) {})
	defer patchesCollectAB.Reset()

	err := r.CollectGarbage(context.Background())
	assert.NoError(t, err)
	assert.False(t, deleteCalled, "Orphan cpVM SubnetPort should be preserved during GC when referenced by reuse-port")
}

func TestSubnetPortReconciler_deleteSubnetPortByName_PreservesReusedPort(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		K8sConfig: &config.K8sConfig{
			SupportReusePort: true,
		},
	}
	r := &SubnetPortReconciler{
		Client: k8sClient,
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	portID := "old-port-1"
	oldPort := &model.VpcSubnetPort{
		Id: &portID,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("old-vm-44")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("kube-system")},
		},
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "ListSubnetPortByName",
		func(_ *subnetport.SubnetPortService, ns, name string) []*model.VpcSubnetPort {
			if ns == "kube-system" && name == "old-vm-44" {
				return []*model.VpcSubnetPort{oldPort}
			}
			return nil
		})
	defer patches.Reset()

	activeCRList := &v1alpha1.SubnetPortList{
		Items: []v1alpha1.SubnetPort{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-vm-44",
					Namespace: "user-ns",
					Labels: map[string]string{
						servicecommon.LabelCPVM: "true",
					},
					Annotations: map[string]string{
						servicecommon.AnnotationReusePort: "kube-system/old-vm-44",
					},
				},
			},
		},
	}
	k8sClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&v1alpha1.SubnetPortList{})).DoAndReturn(
		func(_ context.Context, list *v1alpha1.SubnetPortList, _ ...client.ListOption) error {
			*list = *activeCRList
			return nil
		})

	deleteCalled := false
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "DeleteSubnetPort",
		func(_ *subnetport.SubnetPortService, port *model.VpcSubnetPort) error {
			deleteCalled = true
			return nil
		})

	err := r.deleteSubnetPortByName(context.Background(), "kube-system", "old-vm-44")
	assert.NoError(t, err)
	assert.False(t, deleteCalled, "Port should be preserved during deleteSubnetPortByName when referenced by reuse-port")
}

func TestSubnetPortReconciler_Reconcile_ReuseSubnetPort(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint: "vmc-enforcementpoint",
		},
		K8sConfig: &config.K8sConfig{
			SupportReusePort: true,
		},
	}
	mockSubnet := &mock.MockSubnetServiceProvider{}
	mockVpc := &mock.MockVPCServiceProvider{}
	mockIPAlloc := &mock.MockIPAddressAllocationProvider{}
	r := &SubnetPortReconciler{
		Client:                     k8sClient,
		StatusUpdater:              common.NewStatusUpdater(k8sClient, nsxConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort"),
		SubnetService:              mockSubnet,
		VPCService:                 mockVpc,
		IpAddressAllocationService: mockIPAlloc,
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	newSP := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "new-vm-44",
			Namespace: "user-ns",
			UID:       types.UID("new-uid-123"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
			Annotations: map[string]string{
				servicecommon.AnnotationReusePort: "kube-system/old-vm-44",
			},
		},
		Spec: v1alpha1.SubnetPortSpec{
			InterfaceIPType: v1alpha1.IPAddressTypeIPv4,
		},
	}

	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ client.ObjectKey, obj *v1alpha1.SubnetPort, _ ...client.GetOption) error {
			*obj = *newSP
			return nil
		}).Times(2)
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	patchesStore := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService.SubnetPortStore), "GetVpcSubnetPortByUID",
		func(_ *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
			return nil, nil
		})
	defer patchesStore.Reset()

	oldPortID := "old-port-1"
	parentPath := "/subnets/subnet-1"
	oldPort := &model.VpcSubnetPort{
		Id:         &oldPortID,
		ParentPath: &parentPath,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("old-vm-44")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("kube-system")},
		},
	}
	patchesListVM := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "ListVMSubnetPortByName",
		func(_ *subnetport.SubnetPortService, ns, name string) []*model.VpcSubnetPort {
			if ns == "kube-system" && name == "old-vm-44" {
				return []*model.VpcSubnetPort{oldPort}
			}
			return nil
		})
	defer patchesListVM.Reset()

	patchesParentIP := gomonkey.ApplyPrivateMethod(r, "getParentIPAddressTypeFromCR",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (v1alpha1.IPAddressType, error) {
			return v1alpha1.IPAddressTypeIPv4, nil
		})
	defer patchesParentIP.Reset()

	ipType := "IPV4"
	nsxSubnet := &model.VpcSubnet{
		Path:          &parentPath,
		IpAddressType: &ipType,
	}
	patchesSubnet := gomonkey.ApplyMethod(reflect.TypeOf(mockSubnet), "GetSubnetByPath",
		func(_ *mock.MockSubnetServiceProvider, path string, shared bool) (*model.VpcSubnet, error) {
			return nsxSubnet, nil
		})
	defer patchesSubnet.Reset()

	patchesShared := gomonkey.ApplyFunc(common.IsSharedSubnetPath,
		func(_ context.Context, _ client.Client, _ string, _ string) (bool, error) {
			return false, nil
		})
	defer patchesShared.Reset()

	patchesIsRADeactivated := gomonkey.ApplyMethod(reflect.TypeOf(mockVpc), "IsRADeactivatedByVPCPath",
		func(_ *mock.MockVPCServiceProvider, _ string) (bool, error) {
			return false, nil
		})
	defer patchesIsRADeactivated.Reset()

	patchesVM := gomonkey.ApplyFunc((*SubnetPortReconciler).getVirtualMachine,
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort, _ bool) (*vmv1alpha1.VirtualMachine, string, error) {
			return &vmv1alpha1.VirtualMachine{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"role": "control-plane",
					},
				},
			}, "eth0", nil
		})
	defer patchesVM.Reset()

	portState := &model.SegmentPortState{
		Attachment: &model.SegmentPortAttachmentState{
			Id: ptr.To("attach-123"),
		},
	}
	reuseSubnetPortCalled := false
	patchesReuse := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "ReuseSubnetPort",
		func(_ *subnetport.SubnetPortService, sp *v1alpha1.SubnetPort, p *model.VpcSubnetPort, s *model.VpcSubnet, labels *map[string]string) (*model.SegmentPortState, error) {
			reuseSubnetPortCalled = true
			assert.NotNil(t, labels)
			assert.Equal(t, "control-plane", (*labels)["role"])
			return portState, nil
		})
	defer patchesReuse.Reset()

	patchesUpdateIPType := gomonkey.ApplyPrivateMethod(r, "updateSubnetPortIPType",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort, _ v1alpha1.IPAddressType, _ *model.VpcSubnet) error {
			return nil
		})
	defer patchesUpdateIPType.Reset()

	patchesUpdateSubnetStatusOnSubnetPort := gomonkey.ApplyPrivateMethod(r, "updateSubnetStatusOnSubnetPort",
		func(_ *SubnetPortReconciler, _ *v1alpha1.SubnetPort, _ *model.VpcSubnet) error {
			return nil
		})
	defer patchesUpdateSubnetStatusOnSubnetPort.Reset()

	patchesSetAddressBindingStatus := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort,
		func(_ client.Client, _ context.Context, _ *v1alpha1.SubnetPort, _ *subnetport.SubnetPortService, _ metav1.Time, _ error, _ bool) {
		})
	defer patchesSetAddressBindingStatus.Reset()

	patchesCreateIPAlloc := gomonkey.ApplyMethod(reflect.TypeOf(mockIPAlloc), "CreateIPAddressAllocationForAddressBinding",
		func(_ *mock.MockIPAddressAllocationProvider, _ *v1alpha1.AddressBinding, _ *v1alpha1.SubnetPort, _ bool) error {
			return nil
		})
	defer patchesCreateIPAlloc.Reset()
	patchesDeleteIPAlloc := gomonkey.ApplyMethod(reflect.TypeOf(mockIPAlloc), "DeleteIPAddressAllocationForAddressBinding",
		func(_ *mock.MockIPAddressAllocationProvider, _ metav1.Object) error {
			return nil
		})
	defer patchesDeleteIPAlloc.Reset()

	k8sClient.EXPECT().Status().Return(fakeStatusWriter{t: t})

	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "user-ns",
			Name:      "new-vm-44",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, common.ResultNormal, res)
	assert.True(t, reuseSubnetPortCalled, "ReuseSubnetPort should have been called in Reconcile")
}

func TestSubnetPortReconciler_CollectGarbage_PreservesPortWithOriginUID(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint: "vmc-enforcementpoint",
		},
		K8sConfig: &config.K8sConfig{
			SupportReusePort: true,
		},
	}
	mockIPAlloc := &mock.MockIPAddressAllocationProvider{}
	r := &SubnetPortReconciler{
		Client:                     k8sClient,
		StatusUpdater:              common.NewStatusUpdater(k8sClient, nsxConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort"),
		IpAddressAllocationService: mockIPAlloc,
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	portID := "port-migrated-1"
	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "ListNSXSubnetPortIDForCR",
		func(_ *subnetport.SubnetPortService) sets.Set[string] {
			return sets.New[string](portID)
		})
	defer patches.Reset()

	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "ListSubnetPortIDsFromSubnetPorts",
		func(_ *subnetport.SubnetPortService, _ []v1alpha1.SubnetPort) sets.Set[string] {
			return sets.New[string]()
		})

	patches.ApplyMethod(reflect.TypeOf(mockIPAlloc), "ListIPAddressAllocationWithAddressBinding",
		func(_ *mock.MockIPAddressAllocationProvider) []*model.VpcIpAddressAllocation {
			return nil
		})

	migratedPort := &model.VpcSubnetPort{
		Id: &portID,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("new-vm")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("user-ns")},
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("new-uid")},
			{Scope: ptr.To(servicecommon.TagScopeOriginCRUID), Tag: ptr.To("origin-uid-123")},
			{Scope: ptr.To(servicecommon.LabelCPVM), Tag: ptr.To("true")},
		},
	}
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetSubnetPortByID",
		func(_ *subnetport.SubnetPortService, id string) *model.VpcSubnetPort {
			if id == portID {
				return migratedPort
			}
			return nil
		})

	// Restored pre-upgrade origin CR exists in cluster
	restoredCRList := &v1alpha1.SubnetPortList{
		Items: []v1alpha1.SubnetPort{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "vm-44",
					Namespace: "kube-system",
					UID:       "origin-uid-123",
					Labels: map[string]string{
						servicecommon.LabelCPVM: "true",
					},
				},
			},
		},
	}
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
			if spList, ok := list.(*v1alpha1.SubnetPortList); ok {
				*spList = *restoredCRList
			}
			return nil
		}).AnyTimes()

	deleteCalled := false
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "DeleteSubnetPortById",
		func(_ *subnetport.SubnetPortService, id string) error {
			deleteCalled = true
			return nil
		})

	patchesCollectAB := gomonkey.ApplyPrivateMethod(r, "collectAddressBindingGarbage", func(_ *SubnetPortReconciler, _ context.Context, _, _ *string) {})
	defer patchesCollectAB.Reset()

	err := r.CollectGarbage(context.Background())
	assert.NoError(t, err)
	assert.False(t, deleteCalled, "Port with origin_cr_uid must be preserved during GC when origin CR exists in cluster")
}

func TestSubnetPortReconciler_isPortReused_RestoreScenario(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		K8sConfig: &config.K8sConfig{
			SupportReusePort: true,
		},
	}
	r := &SubnetPortReconciler{
		Client: k8sClient,
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	originSP := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vm-44",
			Namespace: "kube-system",
			UID:       types.UID("origin-uid-123"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-123",
			},
		},
	}

	portID := "port-1"
	parentPath := "/subnets/subnet-1"
	migratedPort := &model.VpcSubnetPort{
		Id:         &portID,
		ParentPath: &parentPath,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("new-uid")},
			{Scope: ptr.To(servicecommon.TagScopeOriginCRUID), Tag: ptr.To("origin-uid-123")},
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("new-vm")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("user-ns")},
		},
	}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetSubnetPortByAttachmentID",
		func(_ *subnetport.SubnetPortService, attachID string) *model.VpcSubnetPort {
			if attachID == "attach-123" {
				return migratedPort
			}
			return nil
		})
	defer patches.Reset()

	// Case 1: Post-upgrade CR is NOT found in K8s (restore scenario) -> isPortReused returns false
	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "user-ns", Name: "new-vm"}, gomock.Any()).
		Return(apierrors.NewNotFound(v1alpha1.Resource("subnetport"), "new-vm"))

	assert.False(t, r.isPortReused(originSP), "isPortReused should return false in restore scenario when post-upgrade CR is gone")

	// Case 2: Post-upgrade CR IS found in K8s with matching UID -> isPortReused returns true
	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "user-ns", Name: "new-vm"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj *v1alpha1.SubnetPort, _ ...client.GetOption) error {
			obj.Name = "new-vm"
			obj.Namespace = "user-ns"
			obj.UID = "new-uid"
			return nil
		})

	assert.True(t, r.isPortReused(originSP), "isPortReused should return true when post-upgrade CR is active in cluster")
}

func TestSubnetPortReconciler_Reconcile_RestoreScenarioRealignsTags(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint: "vmc-enforcementpoint",
		},
		K8sConfig: &config.K8sConfig{
			SupportReusePort: true,
		},
	}
	mockIPAlloc := &mock.MockIPAddressAllocationProvider{}
	r := &SubnetPortReconciler{
		Client:                     k8sClient,
		StatusUpdater:              common.NewStatusUpdater(k8sClient, nsxConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort"),
		IpAddressAllocationService: mockIPAlloc,
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	// Restored pre-upgrade CR in kube-system without reuse-port annotation
	restoredCR := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vm-44",
			Namespace: "kube-system",
			UID:       types.UID("origin-uid-123"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-123",
			},
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{IPAddress: "10.0.0.5/24", Gateway: "10.0.0.1"},
				},
			},
		},
	}

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "kube-system", Name: "vm-44"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj *v1alpha1.SubnetPort, _ ...client.GetOption) error {
			restoredCR.DeepCopyInto(obj)
			return nil
		}).AnyTimes()
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	// When Reconcile checks if post-upgrade CR exists, it returns NotFound
	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "user-ns", Name: "new-vm"}, gomock.Any()).
		Return(apierrors.NewNotFound(v1alpha1.Resource("subnetport"), "new-vm")).AnyTimes()

	portID := "port-migrated-1"
	parentPath := "/orgs/default/projects/default/vpcs/vpc-1/subnets/subnet-1"
	existingPort := &model.VpcSubnetPort{
		Id:         &portID,
		ParentPath: &parentPath,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("new-uid")},
			{Scope: ptr.To(servicecommon.TagScopeOriginCRUID), Tag: ptr.To("origin-uid-123")},
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("new-vm")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("user-ns")},
			{Scope: ptr.To(servicecommon.LabelCPVM), Tag: ptr.To("true")},
		},
		Attachment: &model.PortAttachment{
			Id: ptr.To("attach-123"),
		},
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetSubnetPortByOriginUID",
		func(_ *subnetport.SubnetPortService, uid types.UID) *model.VpcSubnetPort {
			if uid == "origin-uid-123" {
				return existingPort
			}
			return nil
		})
	defer patches.Reset()

	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetSubnetPortByAttachmentID",
		func(_ *subnetport.SubnetPortService, attachID string) *model.VpcSubnetPort {
			if attachID == "attach-123" {
				return existingPort
			}
			return nil
		})

	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService.SubnetPortStore), "GetVpcSubnetPortByUID",
		func(_ *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
			return nil, nil
		})

	nsxSubnet := &model.VpcSubnet{
		Path: &parentPath,
	}
	mockSubnet := &mock.MockSubnetServiceProvider{}
	r.SubnetService = mockSubnet
	patches.ApplyMethod(reflect.TypeOf(mockSubnet), "GetSubnetByPath",
		func(_ *mock.MockSubnetServiceProvider, path string, inSharedSubnet bool) (*model.VpcSubnet, error) {
			return nsxSubnet, nil
		})

	mockVPCService := &mock.MockVPCServiceProvider{}
	r.VPCService = mockVPCService
	patches.ApplyMethod(reflect.TypeOf(mockVPCService), "IsRADeactivatedByVPCPath",
		func(_ *mock.MockVPCServiceProvider, path string) (bool, error) {
			return false, nil
		})

	patches.ApplyFunc(common.IsSharedSubnetPath,
		func(_ context.Context, _ client.Client, _ string, _ string) (bool, error) {
			return false, nil
		})

	patches.ApplyPrivateMethod(r, "getParentIPAddressTypeFromCR",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (v1alpha1.IPAddressType, error) {
			return v1alpha1.IPAddressTypeIPv4, nil
		})

	portState := &model.SegmentPortState{
		Attachment: &model.SegmentPortAttachmentState{
			Id: ptr.To("attach-123"),
		},
	}
	reuseSubnetPortCalled := false
	patchesReuse := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "ReuseSubnetPort",
		func(_ *subnetport.SubnetPortService, sp *v1alpha1.SubnetPort, p *model.VpcSubnetPort, s *model.VpcSubnet, labels *map[string]string) (*model.SegmentPortState, error) {
			reuseSubnetPortCalled = true
			assert.Equal(t, "origin-uid-123", string(sp.UID))
			assert.Equal(t, portID, *p.Id)
			return portState, nil
		})
	defer patchesReuse.Reset()

	patchesUpdateIPType := gomonkey.ApplyPrivateMethod(r, "updateSubnetPortIPType",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort, _ v1alpha1.IPAddressType, _ *model.VpcSubnet) error {
			return nil
		})
	defer patchesUpdateIPType.Reset()

	patchesUpdateSubnetStatusOnSubnetPort := gomonkey.ApplyPrivateMethod(r, "updateSubnetStatusOnSubnetPort",
		func(_ *SubnetPortReconciler, _ *v1alpha1.SubnetPort, _ *model.VpcSubnet) error {
			return nil
		})
	defer patchesUpdateSubnetStatusOnSubnetPort.Reset()

	patchesSetAddressBindingStatus := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort,
		func(_ client.Client, _ context.Context, _ *v1alpha1.SubnetPort, _ *subnetport.SubnetPortService, _ metav1.Time, _ error, _ bool) {
		})
	defer patchesSetAddressBindingStatus.Reset()

	patchesCreateIPAlloc := gomonkey.ApplyMethod(reflect.TypeOf(mockIPAlloc), "CreateIPAddressAllocationForAddressBinding",
		func(_ *mock.MockIPAddressAllocationProvider, _ *v1alpha1.AddressBinding, _ *v1alpha1.SubnetPort, _ bool) error {
			return nil
		})
	defer patchesCreateIPAlloc.Reset()

	patchesDeleteIPAlloc := gomonkey.ApplyMethod(reflect.TypeOf(mockIPAlloc), "DeleteIPAddressAllocationForAddressBinding",
		func(_ *mock.MockIPAddressAllocationProvider, _ metav1.Object) error {
			return nil
		})
	defer patchesDeleteIPAlloc.Reset()

	k8sClient.EXPECT().Status().Return(fakeStatusWriter{t: t}).AnyTimes()

	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "kube-system",
			Name:      "vm-44",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, common.ResultNormal, res)
	assert.True(t, reuseSubnetPortCalled, "ReuseSubnetPort should have been called in Reconcile to realign port tags in restore scenario")
}

func TestSubnetPortReconciler_Reconcile_RestoreScenarioRealignsTags_WhenFlagDisabled(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint: "vmc-enforcementpoint",
		},
		K8sConfig: &config.K8sConfig{
			SupportReusePort: false, // Flag disabled during rollback!
		},
	}
	mockIPAlloc := &mock.MockIPAddressAllocationProvider{}
	r := &SubnetPortReconciler{
		Client:                     k8sClient,
		StatusUpdater:              common.NewStatusUpdater(k8sClient, nsxConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort"),
		IpAddressAllocationService: mockIPAlloc,
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	// Restored pre-upgrade CR in kube-system without reuse-port annotation
	restoredCR := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vm-44",
			Namespace: "kube-system",
			UID:       types.UID("origin-uid-123"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-123",
			},
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{IPAddress: "10.0.0.5/24", Gateway: "10.0.0.1"},
				},
			},
		},
	}

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "kube-system", Name: "vm-44"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj *v1alpha1.SubnetPort, _ ...client.GetOption) error {
			restoredCR.DeepCopyInto(obj)
			return nil
		}).AnyTimes()
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	// When Reconcile checks if post-upgrade CR exists, it returns NotFound
	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "user-ns", Name: "new-vm"}, gomock.Any()).
		Return(apierrors.NewNotFound(v1alpha1.Resource("subnetport"), "new-vm")).AnyTimes()

	portID := "port-migrated-1"
	parentPath := "/orgs/default/projects/default/vpcs/vpc-1/subnets/subnet-1"
	existingPort := &model.VpcSubnetPort{
		Id:         &portID,
		ParentPath: &parentPath,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("new-uid")},
			{Scope: ptr.To(servicecommon.TagScopeOriginCRUID), Tag: ptr.To("origin-uid-123")},
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("new-vm")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("user-ns")},
			{Scope: ptr.To(servicecommon.LabelCPVM), Tag: ptr.To("true")},
		},
		Attachment: &model.PortAttachment{
			Id: ptr.To("attach-123"),
		},
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetSubnetPortByOriginUID",
		func(_ *subnetport.SubnetPortService, uid types.UID) *model.VpcSubnetPort {
			if uid == "origin-uid-123" {
				return existingPort
			}
			return nil
		})
	defer patches.Reset()

	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetSubnetPortByAttachmentID",
		func(_ *subnetport.SubnetPortService, attachID string) *model.VpcSubnetPort {
			if attachID == "attach-123" {
				return existingPort
			}
			return nil
		})

	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService.SubnetPortStore), "GetVpcSubnetPortByUID",
		func(_ *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
			return nil, nil
		})

	nsxSubnet := &model.VpcSubnet{
		Path: &parentPath,
	}
	mockSubnet := &mock.MockSubnetServiceProvider{}
	r.SubnetService = mockSubnet
	patches.ApplyMethod(reflect.TypeOf(mockSubnet), "GetSubnetByPath",
		func(_ *mock.MockSubnetServiceProvider, path string, inSharedSubnet bool) (*model.VpcSubnet, error) {
			return nsxSubnet, nil
		})

	mockVPCService := &mock.MockVPCServiceProvider{}
	r.VPCService = mockVPCService
	patches.ApplyMethod(reflect.TypeOf(mockVPCService), "IsRADeactivatedByVPCPath",
		func(_ *mock.MockVPCServiceProvider, path string) (bool, error) {
			return false, nil
		})

	patches.ApplyFunc(common.IsSharedSubnetPath,
		func(_ context.Context, _ client.Client, _ string, _ string) (bool, error) {
			return false, nil
		})

	patches.ApplyPrivateMethod(r, "getParentIPAddressTypeFromCR",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (v1alpha1.IPAddressType, error) {
			return v1alpha1.IPAddressTypeIPv4, nil
		})

	portState := &model.SegmentPortState{
		Attachment: &model.SegmentPortAttachmentState{
			Id: ptr.To("attach-123"),
		},
	}
	reuseSubnetPortCalled := false
	patchesReuse := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "ReuseSubnetPort",
		func(_ *subnetport.SubnetPortService, sp *v1alpha1.SubnetPort, p *model.VpcSubnetPort, s *model.VpcSubnet, labels *map[string]string) (*model.SegmentPortState, error) {
			reuseSubnetPortCalled = true
			assert.Equal(t, "origin-uid-123", string(sp.UID))
			assert.Equal(t, portID, *p.Id)
			return portState, nil
		})
	defer patchesReuse.Reset()

	patchesUpdateIPType := gomonkey.ApplyPrivateMethod(r, "updateSubnetPortIPType",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort, _ v1alpha1.IPAddressType, _ *model.VpcSubnet) error {
			return nil
		})
	defer patchesUpdateIPType.Reset()

	patchesUpdateSubnetStatusOnSubnetPort := gomonkey.ApplyPrivateMethod(r, "updateSubnetStatusOnSubnetPort",
		func(_ *SubnetPortReconciler, _ *v1alpha1.SubnetPort, _ *model.VpcSubnet) error {
			return nil
		})
	defer patchesUpdateSubnetStatusOnSubnetPort.Reset()

	patchesSetAddressBindingStatus := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort,
		func(_ client.Client, _ context.Context, _ *v1alpha1.SubnetPort, _ *subnetport.SubnetPortService, _ metav1.Time, _ error, _ bool) {
		})
	defer patchesSetAddressBindingStatus.Reset()

	patchesCreateIPAlloc := gomonkey.ApplyMethod(reflect.TypeOf(mockIPAlloc), "CreateIPAddressAllocationForAddressBinding",
		func(_ *mock.MockIPAddressAllocationProvider, _ *v1alpha1.AddressBinding, _ *v1alpha1.SubnetPort, _ bool) error {
			return nil
		})
	defer patchesCreateIPAlloc.Reset()

	patchesDeleteIPAlloc := gomonkey.ApplyMethod(reflect.TypeOf(mockIPAlloc), "DeleteIPAddressAllocationForAddressBinding",
		func(_ *mock.MockIPAddressAllocationProvider, _ metav1.Object) error {
			return nil
		})
	defer patchesDeleteIPAlloc.Reset()

	k8sClient.EXPECT().Status().Return(fakeStatusWriter{t: t}).AnyTimes()

	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "kube-system",
			Name:      "vm-44",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, common.ResultNormal, res)
	assert.True(t, reuseSubnetPortCalled, "ReuseSubnetPort should have been called in Reconcile to realign port tags in restore scenario even when SupportReusePort is false")
}

func TestSubnetPortReconciler_Reconcile_RestoreMode_RecreatesMissingMigratedPort(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint: "vmc-enforcementpoint",
		},
		K8sConfig: &config.K8sConfig{
			SupportReusePort: true,
		},
	}
	mockIPAlloc := &mock.MockIPAddressAllocationProvider{}
	r := &SubnetPortReconciler{
		Client:                     k8sClient,
		APIReader:                  k8sClient,
		StatusUpdater:              common.NewStatusUpdater(k8sClient, nsxConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort"),
		IpAddressAllocationService: mockIPAlloc,
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
		restoreMode: true,
	}

	destCR := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "new-vm",
			Namespace: "user-ns",
			UID:       types.UID("dest-uid"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
			Annotations: map[string]string{
				servicecommon.AnnotationReusePort: "kube-system/source-vm",
			},
		},
		Spec: v1alpha1.SubnetPortSpec{
			InterfaceIPType: v1alpha1.IPAddressTypeIPv4,
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-123",
			},
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{IPAddress: "10.0.0.5/24", Gateway: "10.0.0.1"},
				},
			},
		},
	}

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "user-ns", Name: "new-vm"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj *v1alpha1.SubnetPort, _ ...client.GetOption) error {
			destCR.DeepCopyInto(obj)
			return nil
		}).AnyTimes()
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	parentPath := "/orgs/default/projects/default/vpcs/vpc-1/subnets/subnet-1"
	nsxSubnet := &model.VpcSubnet{
		Path: &parentPath,
	}
	mockSubnet := &mock.MockSubnetServiceProvider{}
	r.SubnetService = mockSubnet
	patches := gomonkey.ApplyMethod(reflect.TypeOf(mockSubnet), "GetSubnetByPath",
		func(_ *mock.MockSubnetServiceProvider, path string, inSharedSubnet bool) (*model.VpcSubnet, error) {
			return nsxSubnet, nil
		})
	defer patches.Reset()

	mockVPCService := &mock.MockVPCServiceProvider{}
	r.VPCService = mockVPCService
	patches.ApplyMethod(reflect.TypeOf(mockVPCService), "IsRADeactivatedByVPCPath",
		func(_ *mock.MockVPCServiceProvider, path string) (bool, error) {
			return false, nil
		})

	patches.ApplyFunc(common.IsSharedSubnetPath,
		func(_ context.Context, _ client.Client, _ string, _ string) (bool, error) {
			return false, nil
		})

	patches.ApplyPrivateMethod(r, "getParentIPAddressTypeFromCR",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (v1alpha1.IPAddressType, error) {
			return v1alpha1.IPAddressTypeIPv4, nil
		})

	patchesCheckPath := gomonkey.ApplyMethod(reflect.TypeOf(r), "CheckAndGetSubnetPathForSubnetPort",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
			return true, false, parentPath, nil, nil, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeNone, nil
		})
	defer patchesCheckPath.Reset()

	createOrUpdateCalled := false
	portState := &model.SegmentPortState{
		Attachment: &model.SegmentPortAttachmentState{
			Id: ptr.To("attach-123"),
		},
	}
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "CreateOrUpdateSubnetPort",
		func(_ *subnetport.SubnetPortService, _ interface{}, _ *model.VpcSubnet, _ string, _ *map[string]string, _ bool, restoreMode bool, _ v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
			createOrUpdateCalled = true
			assert.True(t, restoreMode, "CreateOrUpdateSubnetPort should run with restoreMode=true")
			return portState, nil
		})

	patchesUpdateIPType := gomonkey.ApplyPrivateMethod(r, "updateSubnetPortIPType",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort, _ v1alpha1.IPAddressType, _ *model.VpcSubnet) error {
			return nil
		})
	defer patchesUpdateIPType.Reset()

	patchesUpdateSubnetStatusOnSubnetPort := gomonkey.ApplyPrivateMethod(r, "updateSubnetStatusOnSubnetPort",
		func(_ *SubnetPortReconciler, _ *v1alpha1.SubnetPort, _ *model.VpcSubnet) error {
			return nil
		})
	defer patchesUpdateSubnetStatusOnSubnetPort.Reset()

	patchesSetAddressBindingStatus := gomonkey.ApplyFunc(setAddressBindingStatusBySubnetPort,
		func(_ client.Client, _ context.Context, _ *v1alpha1.SubnetPort, _ *subnetport.SubnetPortService, _ metav1.Time, _ error, _ bool) {
		})
	defer patchesSetAddressBindingStatus.Reset()

	patchesCreateIPAlloc := gomonkey.ApplyMethod(reflect.TypeOf(mockIPAlloc), "CreateIPAddressAllocationForAddressBinding",
		func(_ *mock.MockIPAddressAllocationProvider, _ *v1alpha1.AddressBinding, _ *v1alpha1.SubnetPort, _ bool) error {
			return nil
		})
	defer patchesCreateIPAlloc.Reset()

	patchesDeleteIPAlloc := gomonkey.ApplyMethod(reflect.TypeOf(mockIPAlloc), "DeleteIPAddressAllocationForAddressBinding",
		func(_ *mock.MockIPAddressAllocationProvider, _ metav1.Object) error {
			return nil
		})
	defer patchesDeleteIPAlloc.Reset()

	k8sClient.EXPECT().Status().Return(fakeStatusWriter{t: t}).AnyTimes()
	k8sClient.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "user-ns",
			Name:      "new-vm",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, common.ResultNormal, res)
	assert.True(t, createOrUpdateCalled, "CreateOrUpdateSubnetPort should have been called in restoreMode when port was missing from store")
}

func TestSubnetPortReconciler_Reconcile_RollbackRetryPreservesProtection(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint: "vmc-enforcementpoint",
		},
		K8sConfig: &config.K8sConfig{
			SupportReusePort: true,
		},
	}
	mockIPAlloc := &mock.MockIPAddressAllocationProvider{}
	mockSubnet := &mock.MockSubnetServiceProvider{}
	mockVPCService := &mock.MockVPCServiceProvider{}
	r := &SubnetPortReconciler{
		Client:                     k8sClient,
		StatusUpdater:              common.NewStatusUpdater(k8sClient, nsxConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort"),
		SubnetService:              mockSubnet,
		VPCService:                 mockVPCService,
		IpAddressAllocationService: mockIPAlloc,
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	// Restored pre-upgrade origin CR in kube-system without reuse-port annotation
	originCR := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vm-44",
			Namespace: "kube-system",
			UID:       types.UID("origin-uid-123"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
		},
		Spec: v1alpha1.SubnetPortSpec{
			InterfaceIPType: v1alpha1.IPAddressTypeIPv4,
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-123",
			},
		},
	}

	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ client.ObjectKey, obj *v1alpha1.SubnetPort, _ ...client.GetOption) error {
			originCR.DeepCopyInto(obj)
			return nil
		}).AnyTimes()
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	parentPath := "/orgs/default/projects/default/vpcs/vpc-1/subnets/subnet-1"
	portID := "port-migrated-1"
	// Simulating attempt 2 of rollback: store already holds port with origin-uid-123 as owner AND origin_cr_uid
	reclaimedPort := &model.VpcSubnetPort{
		Id:         &portID,
		ParentPath: &parentPath,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("origin-uid-123")},
			{Scope: ptr.To(servicecommon.TagScopeOriginCRUID), Tag: ptr.To("origin-uid-123")},
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("vm-44")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("kube-system")},
			{Scope: ptr.To(servicecommon.LabelCPVM), Tag: ptr.To("true")},
		},
		Attachment: &model.PortAttachment{
			Id: ptr.To("attach-123"),
		},
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService.SubnetPortStore), "GetVpcSubnetPortByUID",
		func(_ *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
			if uid == "origin-uid-123" {
				return reclaimedPort, nil
			}
			return nil, nil
		})
	defer patches.Reset()

	nsxSubnet := &model.VpcSubnet{
		Path: ptr.To(parentPath),
	}
	patches.ApplyMethod(reflect.TypeOf(mockSubnet), "GetSubnetByPath",
		func(_ *mock.MockSubnetServiceProvider, path string, inSharedSubnet bool) (*model.VpcSubnet, error) {
			return nsxSubnet, nil
		})

	patches.ApplyMethod(reflect.TypeOf(mockVPCService), "IsRADeactivatedByVPCPath",
		func(_ *mock.MockVPCServiceProvider, path string) (bool, error) {
			return false, nil
		})

	patches.ApplyFunc(common.IsSharedSubnetPath,
		func(_ context.Context, _ client.Client, _ string, _ string) (bool, error) {
			return false, nil
		})

	patches.ApplyPrivateMethod(r, "getParentIPAddressTypeFromCR",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (v1alpha1.IPAddressType, error) {
			return v1alpha1.IPAddressTypeIPv4, nil
		})

	patches.ApplyPrivateMethod(r, "updateSubnetPortIPType",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort, _ v1alpha1.IPAddressType, _ *model.VpcSubnet) error {
			return nil
		})

	patches.ApplyPrivateMethod(r, "updateSubnetStatusOnSubnetPort",
		func(_ *SubnetPortReconciler, _ *v1alpha1.SubnetPort, _ *model.VpcSubnet) error {
			return nil
		})

	patches.ApplyFunc(setAddressBindingStatusBySubnetPort,
		func(_ client.Client, _ context.Context, _ *v1alpha1.SubnetPort, _ *subnetport.SubnetPortService, _ metav1.Time, _ error, _ bool) {
		})

	patches.ApplyMethod(reflect.TypeOf(mockIPAlloc), "CreateIPAddressAllocationForAddressBinding",
		func(_ *mock.MockIPAddressAllocationProvider, _ *v1alpha1.AddressBinding, _ *v1alpha1.SubnetPort, _ bool) error {
			return nil
		})

	patches.ApplyMethod(reflect.TypeOf(mockIPAlloc), "DeleteIPAddressAllocationForAddressBinding",
		func(_ *mock.MockIPAddressAllocationProvider, _ metav1.Object) error {
			return nil
		})

	createOrUpdateCalled := false
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "CreateOrUpdateSubnetPort",
		func(_ *subnetport.SubnetPortService, _ interface{}, _ *model.VpcSubnet, _ string, _ *map[string]string, _ bool, _ bool, _ v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
			createOrUpdateCalled = true
			return nil, nsxutil.NewRealizeStateError("realized error on retry", 500000)
		})

	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "DeleteSubnetPort",
		func(_ *subnetport.SubnetPortService, _ *model.VpcSubnetPort) error {
			assert.Fail(t, "DeleteSubnetPort should never be called for rollback-reclaimed port on retry realization error")
			return nil
		})

	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "DeleteSubnetPortById",
		func(_ *subnetport.SubnetPortService, _ string) error {
			assert.Fail(t, "DeleteSubnetPortById should never be called for rollback-reclaimed port on retry realization error")
			return nil
		})

	k8sClient.EXPECT().Status().Return(fakeStatusWriter{t: t}).AnyTimes()

	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "kube-system",
			Name:      "vm-44",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, common.ResultRequeueAfter60sec, res)
	assert.True(t, createOrUpdateCalled, "CreateOrUpdateSubnetPort must be invoked on retry for reclaimed port")
}

func TestSubnetPortReconciler_Reconcile_SourceCRSupersededAfterDestinationRecreated(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint: "vmc-enforcementpoint",
		},
		K8sConfig: &config.K8sConfig{
			SupportReusePort: true,
		},
	}
	r := &SubnetPortReconciler{
		Client:        k8sClient,
		StatusUpdater: common.NewStatusUpdater(k8sClient, nsxConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort"),
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	// Retained source CR with old attachment ID
	sourceCR := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vm-44",
			Namespace: "kube-system",
			UID:       types.UID("origin-uid-123"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
		},
		Spec: v1alpha1.SubnetPortSpec{
			InterfaceIPType: v1alpha1.IPAddressTypeIPv4,
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "old-attach-id",
			},
		},
	}

	// Recreated destination CR with new attachment ID and active reuse-port annotation referencing source
	destCR := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dest-vm",
			Namespace: "user-ns",
			UID:       types.UID("dest-uid-456"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
			Annotations: map[string]string{
				servicecommon.AnnotationReusePort: "kube-system/vm-44",
			},
		},
		Spec: v1alpha1.SubnetPortSpec{
			InterfaceIPType: v1alpha1.IPAddressTypeIPv4,
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "new-attach-id",
			},
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{IPAddress: "10.0.0.5/24", Gateway: "10.0.0.1"},
				},
			},
		},
	}

	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, key types.NamespacedName, obj *v1alpha1.SubnetPort, _ ...client.GetOption) error {
			if key.Namespace == "kube-system" && key.Name == "vm-44" {
				sourceCR.DeepCopyInto(obj)
				return nil
			}
			if key.Namespace == "user-ns" && key.Name == "dest-vm" {
				destCR.DeepCopyInto(obj)
				return nil
			}
			return apierrors.NewNotFound(v1alpha1.Resource("subnetport"), key.Name)
		}).AnyTimes()

	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
			spList := list.(*v1alpha1.SubnetPortList)
			spList.Items = []v1alpha1.SubnetPort{*sourceCR, *destCR}
			return nil
		}).AnyTimes()

	portDestID := "port-dest"
	parentPath := "/subnets/subnet-1"
	destPort := &model.VpcSubnetPort{
		Id:         &portDestID,
		ParentPath: &parentPath,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("dest-uid-456")},
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("dest-vm")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("user-ns")},
			{Scope: ptr.To(servicecommon.LabelCPVM), Tag: ptr.To("true")},
		},
		Attachment: &model.PortAttachment{
			Id: ptr.To("new-attach-id"),
		},
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetSubnetPortByAttachmentID",
		func(_ *subnetport.SubnetPortService, attachID string) *model.VpcSubnetPort {
			if attachID == "new-attach-id" {
				return destPort
			}
			return nil
		})
	defer patches.Reset()

	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService.SubnetPortStore), "GetVpcSubnetPortByUID",
		func(_ *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
			if uid == "dest-uid-456" {
				return destPort, nil
			}
			return nil, nil
		})

	patches.ApplyMethod(reflect.TypeOf(r), "CheckAndGetSubnetPathForSubnetPort",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
			assert.Fail(t, "CheckAndGetSubnetPathForSubnetPort should never be reached for superseded source CR")
			return false, false, "", nil, nil, "", "", nil
		})
	defer patches.Reset()

	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "kube-system",
			Name:      "vm-44",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, common.ResultNormal, res, "Reconcile should return ResultNormal for superseded source CR")

	// Even when SupportReusePort is false (e.g. restore scenario where flag is disabled),
	// the source CR must remain superseded by the recreated destination CR that has the migrated IP.
	r.SubnetPortService.NSXConfig.K8sConfig.SupportReusePort = false
	res, err = r.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, common.ResultNormal, res, "Reconcile should return ResultNormal for superseded source CR even with SupportReusePort disabled")
}

func TestSubnetPortReconciler_Reconcile_ReusePortAnnotationDisabled(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint: "vmc-enforcementpoint",
		},
		K8sConfig: &config.K8sConfig{
			SupportReusePort: false,
		},
	}
	r := &SubnetPortReconciler{
		Client:        k8sClient,
		StatusUpdater: common.NewStatusUpdater(k8sClient, nsxConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort"),
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	destCR := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "new-vm",
			Namespace: "user-ns",
			UID:       types.UID("dest-uid"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
			Annotations: map[string]string{
				servicecommon.AnnotationReusePort: "kube-system/old-vm",
			},
		},
		Spec: v1alpha1.SubnetPortSpec{
			InterfaceIPType: v1alpha1.IPAddressTypeIPv4,
		},
	}

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "user-ns", Name: "new-vm"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj *v1alpha1.SubnetPort, _ ...client.GetOption) error {
			destCR.DeepCopyInto(obj)
			return nil
		}).AnyTimes()
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	k8sClient.EXPECT().Status().Return(fakeStatusWriter{t: t}).AnyTimes()

	reusePortCalled := false
	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "ReuseSubnetPort",
		func(_ *subnetport.SubnetPortService, _ *v1alpha1.SubnetPort, _ *model.VpcSubnetPort, _ *model.VpcSubnet, _ *map[string]string) (*model.SegmentPortState, error) {
			reusePortCalled = true
			return nil, nil
		})
	defer patches.Reset()

	checkSubnetPathCalled := false
	patches.ApplyMethod(reflect.TypeOf(r), "CheckAndGetSubnetPathForSubnetPort",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
			checkSubnetPathCalled = true
			return false, false, "", nil, nil, "", "", errors.New("expected stop at port creation")
		})

	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "user-ns",
			Name:      "new-vm",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.Error(t, err)
	assert.Equal(t, common.ResultRequeue, res)
	assert.False(t, reusePortCalled, "ReuseSubnetPort should NOT be called when SupportReusePort is disabled")
	assert.True(t, checkSubnetPathCalled, "Reconcile should fall through to normal port creation path when SupportReusePort is disabled")
}

func TestSubnetPortReconciler_Reconcile_NormalUpdate_SupportReusePortDisabled_SuppressesDeletion(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint: "vmc-enforcementpoint",
		},
		K8sConfig: &config.K8sConfig{
			SupportReusePort: false, // Flag disabled after previous migration!
		},
	}
	mockIPAlloc := &mock.MockIPAddressAllocationProvider{}
	mockSubnet := &mock.MockSubnetServiceProvider{}
	mockVPCService := &mock.MockVPCServiceProvider{}
	r := &SubnetPortReconciler{
		Client:                     k8sClient,
		StatusUpdater:              common.NewStatusUpdater(k8sClient, nsxConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort"),
		SubnetService:              mockSubnet,
		VPCService:                 mockVPCService,
		IpAddressAllocationService: mockIPAlloc,
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	destCR := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dest-vm",
			Namespace: "user-ns",
			UID:       types.UID("dest-uid"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
		},
		Spec: v1alpha1.SubnetPortSpec{
			InterfaceIPType: v1alpha1.IPAddressTypeIPv4,
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-1",
			},
		},
	}

	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ client.ObjectKey, obj *v1alpha1.SubnetPort, _ ...client.GetOption) error {
			destCR.DeepCopyInto(obj)
			return nil
		}).AnyTimes()
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	portID := "port-1"
	parentPath := "/orgs/default/projects/default/vpcs/vpc-1/subnets/subnet-1"
	migratedPort := &model.VpcSubnetPort{
		Id:         &portID,
		ParentPath: &parentPath,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("dest-uid")},
			{Scope: ptr.To(servicecommon.TagScopeOriginCRUID), Tag: ptr.To("origin-uid")},
			{Scope: ptr.To(servicecommon.LabelCPVM), Tag: ptr.To("true")},
		},
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService.SubnetPortStore), "GetVpcSubnetPortByUID",
		func(_ *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
			if uid == "dest-uid" {
				return migratedPort, nil
			}
			return nil, nil
		})
	defer patches.Reset()

	nsxSubnet := &model.VpcSubnet{
		Path: ptr.To(parentPath),
	}
	patches.ApplyMethod(reflect.TypeOf(mockSubnet), "GetSubnetByPath",
		func(_ *mock.MockSubnetServiceProvider, path string, inSharedSubnet bool) (*model.VpcSubnet, error) {
			return nsxSubnet, nil
		})

	patches.ApplyMethod(reflect.TypeOf(mockVPCService), "IsRADeactivatedByVPCPath",
		func(_ *mock.MockVPCServiceProvider, path string) (bool, error) {
			return false, nil
		})

	patches.ApplyFunc(common.IsSharedSubnetPath,
		func(_ context.Context, _ client.Client, _ string, _ string) (bool, error) {
			return false, nil
		})

	patches.ApplyPrivateMethod(r, "getParentIPAddressTypeFromCR",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (v1alpha1.IPAddressType, error) {
			return v1alpha1.IPAddressTypeIPv4, nil
		})

	patches.ApplyPrivateMethod(r, "updateSubnetPortIPType",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort, _ v1alpha1.IPAddressType, _ *model.VpcSubnet) error {
			return nil
		})

	patches.ApplyPrivateMethod(r, "updateSubnetStatusOnSubnetPort",
		func(_ *SubnetPortReconciler, _ *v1alpha1.SubnetPort, _ *model.VpcSubnet) error {
			return nil
		})

	patches.ApplyFunc(setAddressBindingStatusBySubnetPort,
		func(_ client.Client, _ context.Context, _ *v1alpha1.SubnetPort, _ *subnetport.SubnetPortService, _ metav1.Time, _ error, _ bool) {
		})

	patches.ApplyMethod(reflect.TypeOf(mockIPAlloc), "CreateIPAddressAllocationForAddressBinding",
		func(_ *mock.MockIPAddressAllocationProvider, _ *v1alpha1.AddressBinding, _ *v1alpha1.SubnetPort, _ bool) error {
			return nil
		})

	patches.ApplyMethod(reflect.TypeOf(mockIPAlloc), "DeleteIPAddressAllocationForAddressBinding",
		func(_ *mock.MockIPAddressAllocationProvider, _ metav1.Object) error {
			return nil
		})

	createOrUpdateCalled := false
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "CreateOrUpdateSubnetPort",
		func(_ *subnetport.SubnetPortService, _ interface{}, _ *model.VpcSubnet, _ string, _ *map[string]string, _ bool, _ bool, _ v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
			createOrUpdateCalled = true
			return nil, nsxutil.NewRealizeStateError("realized error on retry", 500000)
		})

	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "DeleteSubnetPort",
		func(_ *subnetport.SubnetPortService, _ *model.VpcSubnetPort) error {
			assert.Fail(t, "DeleteSubnetPort should never be called when support_reuse_port is false for already migrated port")
			return nil
		})

	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "DeleteSubnetPortById",
		func(_ *subnetport.SubnetPortService, _ string) error {
			assert.Fail(t, "DeleteSubnetPortById should never be called when support_reuse_port is false for already migrated port")
			return nil
		})

	k8sClient.EXPECT().Status().Return(fakeStatusWriter{t: t}).AnyTimes()

	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "user-ns",
			Name:      "dest-vm",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, common.ResultRequeueAfter60sec, res)
	assert.True(t, createOrUpdateCalled, "CreateOrUpdateSubnetPort must be invoked")
}

func TestSubnetPortReconciler_Reconcile_SupportReusePortDisabled_BothPortsCached_SourceNotSuperseded(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint: "vmc-enforcementpoint",
		},
		K8sConfig: &config.K8sConfig{
			SupportReusePort: false,
		},
	}
	r := &SubnetPortReconciler{
		Client:        k8sClient,
		StatusUpdater: common.NewStatusUpdater(k8sClient, nsxConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort"),
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	sourceCR := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "old-vm",
			Namespace: "kube-system",
			UID:       types.UID("source-uid"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
		},
		Spec: v1alpha1.SubnetPortSpec{
			InterfaceIPType: v1alpha1.IPAddressTypeIPv4,
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-source",
			},
		},
	}

	destCR := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "new-vm",
			Namespace: "user-ns",
			UID:       types.UID("dest-uid"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
			Annotations: map[string]string{
				servicecommon.AnnotationReusePort: "kube-system/old-vm",
			},
		},
		Spec: v1alpha1.SubnetPortSpec{
			InterfaceIPType: v1alpha1.IPAddressTypeIPv4,
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-dest",
			},
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{IPAddress: "10.0.0.3/28", Gateway: "10.0.0.1"},
				},
			},
		},
	}

	// Both CRs are present in K8s
	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "kube-system", Name: "old-vm"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj *v1alpha1.SubnetPort, _ ...client.GetOption) error {
			sourceCR.DeepCopyInto(obj)
			return nil
		}).AnyTimes()

	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
			spList := list.(*v1alpha1.SubnetPortList)
			spList.Items = []v1alpha1.SubnetPort{*sourceCR, *destCR}
			return nil
		}).AnyTimes()

	k8sClient.EXPECT().Status().Return(fakeStatusWriter{t: t}).AnyTimes()

	// Two separately owned ports/attachment IDs cached
	portSourceID := "port-source"
	parentPath := "/subnets/subnet-1"
	sourcePort := &model.VpcSubnetPort{
		Id:         &portSourceID,
		ParentPath: &parentPath,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("source-uid")},
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("old-vm")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("kube-system")},
			{Scope: ptr.To(servicecommon.LabelCPVM), Tag: ptr.To("true")},
		},
		Attachment: &model.PortAttachment{
			Id: ptr.To("attach-source"),
		},
	}

	portDestID := "port-dest"
	destPort := &model.VpcSubnetPort{
		Id:         &portDestID,
		ParentPath: &parentPath,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("dest-uid")},
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("new-vm")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("user-ns")},
			{Scope: ptr.To(servicecommon.LabelCPVM), Tag: ptr.To("true")},
		},
		Attachment: &model.PortAttachment{
			Id: ptr.To("attach-dest"),
		},
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetSubnetPortByAttachmentID",
		func(_ *subnetport.SubnetPortService, attachID string) *model.VpcSubnetPort {
			if attachID == "attach-source" {
				return sourcePort
			}
			if attachID == "attach-dest" {
				return destPort
			}
			return nil
		})
	defer patches.Reset()

	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService.SubnetPortStore), "GetVpcSubnetPortByUID",
		func(_ *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
			if uid == "source-uid" {
				return sourcePort, nil
			}
			if uid == "dest-uid" {
				return destPort, nil
			}
			return nil, nil
		})

	// Verify isPortSuperseded returns false directly
	isSuperseded, err := r.isPortSuperseded(sourceCR, []v1alpha1.SubnetPort{*sourceCR, *destCR})
	assert.NoError(t, err)
	assert.False(t, isSuperseded, "sourceCR must NOT be marked superseded when support_reuse_port is false and both CRs own distinct ports")

	// Verify Reconcile does not skip reconciliation, but reaches port processing
	checkSubnetPathCalled := false
	patches.ApplyMethod(reflect.TypeOf(r), "CheckAndGetSubnetPathForSubnetPort",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
			checkSubnetPathCalled = true
			return false, false, "", nil, nil, "", "", errors.New("expected stop at subnet check")
		})

	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "kube-system",
			Name:      "old-vm",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.Error(t, err)
	assert.Equal(t, common.ResultRequeue, res)
	assert.True(t, checkSubnetPathCalled, "Reconcile must NOT skip sourceCR when support_reuse_port is false and both ports are cached")
}

func TestSubnetPortReconciler_Reconcile_Delete_PreservesPortWhenReferencedByDestinationAwaitingTakeover(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint: "vmc-enforcementpoint",
		},
		K8sConfig: &config.K8sConfig{
			SupportReusePort: true,
		},
	}
	r := &SubnetPortReconciler{
		Client:        k8sClient,
		StatusUpdater: common.NewStatusUpdater(k8sClient, nsxConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort"),
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	now := metav1.Now()
	sourceCR := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "source-vm",
			Namespace:         "kube-system",
			UID:               types.UID("source-uid"),
			DeletionTimestamp: &now,
			Finalizers:        []string{"nsx.vmware.com/subnetport"},
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
		},
		Spec: v1alpha1.SubnetPortSpec{
			Subnet: "subnet-1",
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-source",
			},
		},
	}

	// Destination CR actively references sourceCR but has NOT completed takeover yet (len(IPAddresses) == 0)
	destCR := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dest-vm",
			Namespace: "user-ns",
			UID:       types.UID("dest-uid"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
			Annotations: map[string]string{
				servicecommon.AnnotationReusePort: "kube-system/source-vm",
			},
		},
		Spec: v1alpha1.SubnetPortSpec{
			InterfaceIPType: v1alpha1.IPAddressTypeIPv4,
		},
	}

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "kube-system", Name: "source-vm"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj *v1alpha1.SubnetPort, _ ...client.GetOption) error {
			sourceCR.DeepCopyInto(obj)
			return nil
		}).AnyTimes()

	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
			spList := list.(*v1alpha1.SubnetPortList)
			spList.Items = []v1alpha1.SubnetPort{*sourceCR, *destCR}
			return nil
		}).AnyTimes()

	portSourceID := "port-source"
	parentPath := "/subnets/subnet-1"
	sourcePort := &model.VpcSubnetPort{
		Id:         &portSourceID,
		ParentPath: &parentPath,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("source-uid")},
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("source-vm")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("kube-system")},
			{Scope: ptr.To(servicecommon.LabelCPVM), Tag: ptr.To("true")},
		},
		Attachment: &model.PortAttachment{
			Id: ptr.To("attach-source"),
		},
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService.SubnetPortStore), "GetVpcSubnetPortByUID",
		func(_ *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
			if uid == "source-uid" {
				return sourcePort, nil
			}
			return nil, nil
		})
	defer patches.Reset()

	deleteSubnetPortCalled := false
	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "DeleteSubnetPort",
		func(_ *subnetport.SubnetPortService, _ *model.VpcSubnetPort) error {
			deleteSubnetPortCalled = true
			return nil
		})

	deleteSuccessCalled := false
	patches.ApplyMethod(reflect.TypeOf(&r.StatusUpdater), "DeleteSuccess",
		func(_ *common.StatusUpdater, _ types.NamespacedName, _ client.Object) {
			deleteSuccessCalled = true
		})

	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "kube-system",
			Name:      "source-vm",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, common.ResultNormal, res)
	assert.False(t, deleteSubnetPortCalled, "DeleteSubnetPort must NOT be called while destination CR is awaiting takeover")
	assert.True(t, deleteSuccessCalled, "DeleteSuccess must be called to allow source CR finalizer removal")
}

func TestSubnetPortReconciler_Reconcile_SupportReusePortDisabled_SourceBackendDisappeared_NotSuperseded(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	nsxConfig := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint: "vmc-enforcementpoint",
		},
		K8sConfig: &config.K8sConfig{
			SupportReusePort: false,
		},
	}
	r := &SubnetPortReconciler{
		Client:        k8sClient,
		StatusUpdater: common.NewStatusUpdater(k8sClient, nsxConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort"),
		SubnetPortService: &subnetport.SubnetPortService{
			Service: servicecommon.Service{
				NSXConfig: nsxConfig,
			},
			SubnetPortStore: &subnetport.SubnetPortStore{},
		},
	}

	sourceCR := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "old-vm",
			Namespace: "kube-system",
			UID:       types.UID("source-uid"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
		},
		Spec: v1alpha1.SubnetPortSpec{
			InterfaceIPType: v1alpha1.IPAddressTypeIPv4,
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-source",
			},
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{IPAddress: "10.0.0.2/28", Gateway: "10.0.0.1"},
				},
			},
		},
	}

	// Destination CR has separate port with IP status
	destCR := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "new-vm",
			Namespace: "user-ns",
			UID:       types.UID("dest-uid"),
			Labels: map[string]string{
				servicecommon.LabelCPVM: "true",
			},
			Annotations: map[string]string{
				servicecommon.AnnotationReusePort: "kube-system/old-vm",
			},
		},
		Spec: v1alpha1.SubnetPortSpec{
			InterfaceIPType: v1alpha1.IPAddressTypeIPv4,
		},
		Status: v1alpha1.SubnetPortStatus{
			Attachment: v1alpha1.PortAttachment{
				ID: "attach-dest",
			},
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
					{IPAddress: "10.0.0.8/28", Gateway: "10.0.0.1"},
				},
			},
		},
	}

	k8sClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "kube-system", Name: "old-vm"}, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj *v1alpha1.SubnetPort, _ ...client.GetOption) error {
			sourceCR.DeepCopyInto(obj)
			return nil
		}).AnyTimes()

	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
			spList := list.(*v1alpha1.SubnetPortList)
			spList.Items = []v1alpha1.SubnetPort{*sourceCR, *destCR}
			return nil
		}).AnyTimes()

	k8sClient.EXPECT().Status().Return(fakeStatusWriter{t: t}).AnyTimes()

	// Destination port exists, but source backend port has disappeared (nil)
	portDestID := "port-dest"
	parentPath := "/subnets/subnet-1"
	destPort := &model.VpcSubnetPort{
		Id:         &portDestID,
		ParentPath: &parentPath,
		Tags: []model.Tag{
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRUID), Tag: ptr.To("dest-uid")},
			{Scope: ptr.To(servicecommon.TagScopeSubnetPortCRName), Tag: ptr.To("new-vm")},
			{Scope: ptr.To(servicecommon.TagScopeVMNamespace), Tag: ptr.To("user-ns")},
			{Scope: ptr.To(servicecommon.LabelCPVM), Tag: ptr.To("true")},
		},
		Attachment: &model.PortAttachment{
			Id: ptr.To("attach-dest"),
		},
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetPortService), "GetSubnetPortByAttachmentID",
		func(_ *subnetport.SubnetPortService, attachID string) *model.VpcSubnetPort {
			if attachID == "attach-dest" {
				return destPort
			}
			return nil
		})
	defer patches.Reset()

	patches.ApplyMethod(reflect.TypeOf(r.SubnetPortService.SubnetPortStore), "GetVpcSubnetPortByUID",
		func(_ *subnetport.SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
			if uid == "dest-uid" {
				return destPort, nil
			}
			return nil, nil
		})

	// Source port backend disappeared, but destPort is distinct. Source CR must NOT be superseded.
	isSuperseded, err := r.isPortSuperseded(sourceCR, []v1alpha1.SubnetPort{*sourceCR, *destCR})
	assert.NoError(t, err)
	assert.False(t, isSuperseded, "sourceCR must NOT be superseded when source backend disappeared and reuse is disabled")

	checkSubnetPathCalled := false
	patches.ApplyMethod(reflect.TypeOf(r), "CheckAndGetSubnetPathForSubnetPort",
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
			checkSubnetPathCalled = true
			return false, false, "", nil, nil, "", "", errors.New("expected stop at subnet check")
		})

	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "kube-system",
			Name:      "old-vm",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.Error(t, err)
	assert.Equal(t, common.ResultRequeue, res)
	assert.True(t, checkSubnetPathCalled, "Reconcile must proceed to recreate/reconcile sourceCR when backend disappeared with reuse disabled")
}
