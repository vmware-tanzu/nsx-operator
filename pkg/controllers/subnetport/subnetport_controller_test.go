package subnetport

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/crd.nsx.vmware.com/v1alpha1"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type fakeRecorder struct {
}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
}
func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}
func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}

func TestSubnetPortReconciler_Reconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	service := &subnetport.SubnetPortService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
	}
	subnetService := &subnet.SubnetService{
		Service: common.Service{
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
	ctx := context.Background()
	req := controllerruntime.Request{NamespacedName: types.NamespacedName{Namespace: "dummy", Name: "dummy"}}
	patchesGetSubnetByPath := gomonkey.ApplyFunc((*subnet.SubnetService).GetSubnetByPath,
		func(s *subnet.SubnetService, nsxSubnetPath string) (*model.VpcSubnet, error) {
			nsxSubnet := &model.VpcSubnet{}
			return nsxSubnet, nil
		})
	defer patchesGetSubnetByPath.Reset()

	// common.GcOnce do nothing
	var once sync.Once
	pat := gomonkey.ApplyMethod(reflect.TypeOf(&once), "Do", func(_ *sync.Once, _ func()) {})
	defer pat.Reset()

	// not found
	errNotFound := errors.New("not found")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errNotFound)
	_, err := r.Reconcile(ctx, req)
	assert.Equal(t, err, errNotFound)

	// update fails
	sp := &v1alpha1.SubnetPort{}
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			v1sp := obj.(*v1alpha1.SubnetPort)
			v1sp.Spec.Subnet = "subnet1"
			return nil
		})
	err = errors.New("Update failed")
	k8sClient.EXPECT().Update(ctx, gomock.Any()).Return(err)
	patchesSuccess := gomonkey.ApplyFunc(updateSuccess,
		func(r *SubnetPortReconciler, c *context.Context, o *v1alpha1.SubnetPort) {
		})
	defer patchesSuccess.Reset()
	patchesUpdateFail := gomonkey.ApplyFunc(updateFail,
		func(r *SubnetPortReconciler, c *context.Context, o *v1alpha1.SubnetPort, e *error) {
		})
	defer patchesUpdateFail.Reset()
	_, ret := r.Reconcile(ctx, req)
	assert.Equal(t, err, ret)

	// both subnet and subnetset are configured
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			v1sp := obj.(*v1alpha1.SubnetPort)
			v1sp.Spec.Subnet = "subnet1"
			v1sp.Spec.SubnetSet = "subnetset2"
			return nil
		})
	err = errors.New("subnet and subnetset should not be configured at the same time")
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, err, ret)

	// CreateOrUpdateSubnetPort fails
	patchesGetLabelsFromVirtualMachine := gomonkey.ApplyFunc((*SubnetPortReconciler).getLabelsFromVirtualMachine,
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
	k8sClient.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			v1sp := obj.(*v1alpha1.SubnetPort)
			v1sp.Spec.Subnet = "subnet1"
			return nil
		})
	err = errors.New("CreateOrUpdateSubnetPort failed")
	patchesGetSubnetPathForSubnetPort := gomonkey.ApplyFunc((*SubnetPortReconciler).GetSubnetPathForSubnetPort,
		func(r *SubnetPortReconciler, ctx context.Context, obj *v1alpha1.SubnetPort) (string, error) {
			return "", nil
		})
	defer patchesGetSubnetPathForSubnetPort.Reset()
	patchesCreateOrUpdateSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
		func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string) (*model.SegmentPortState, error) {
			return nil, err
		})
	defer patchesCreateOrUpdateSubnetPort.Reset()
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, err, ret)
	// happy path
	k8sClient.EXPECT().Update(ctx, gomock.Any()).Return(nil)
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
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, nil, ret)

	// handle deletion event - delete NSX subnet port failed
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			v1sp := obj.(*v1alpha1.SubnetPort)
			v1sp.Spec.Subnet = "subnet1"
			time := metav1.Now()
			v1sp.ObjectMeta.DeletionTimestamp = &time
			v1sp.Finalizers = []string{common.SubnetPortFinalizerName}
			return nil
		})
	err = errors.New("DeleteSubnetPort failed")
	patchesDeleteSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPort,
		func(s *subnetport.SubnetPortService, uid string) error {
			return err
		})
	defer patchesDeleteSubnetPort.Reset()
	patchesCreateOrUpdateSubnetPort = gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort,
		func(s *subnetport.SubnetPortService, obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string) (*model.SegmentPortState, error) {
			assert.FailNow(t, "should not be called")
			return nil, nil
		})
	defer patchesCreateOrUpdateSubnetPort.Reset()
	patchesDeleteFail := gomonkey.ApplyFunc(deleteFail,
		func(r *SubnetPortReconciler, c *context.Context, o *v1alpha1.SubnetPort, e *error) {
		})
	defer patchesDeleteFail.Reset()
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, err, ret)

	// handle deletion event - update subnetport failed in deletion event
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			v1sp := obj.(*v1alpha1.SubnetPort)
			v1sp.Spec.Subnet = "subnet1"
			time := metav1.Now()
			v1sp.ObjectMeta.DeletionTimestamp = &time
			v1sp.Finalizers = []string{common.SubnetPortFinalizerName}
			return nil
		})
	err = errors.New("Update failed")
	k8sClient.EXPECT().Update(ctx, gomock.Any()).Return(err)
	patchesDeleteSubnetPort = gomonkey.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPort,
		func(s *subnetport.SubnetPortService, uid string) error {
			return nil
		})
	defer patchesDeleteSubnetPort.Reset()
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, err, ret)

	// handle deletion event - successfully deleted
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			v1sp := obj.(*v1alpha1.SubnetPort)
			v1sp.Spec.Subnet = "subnet1"
			time := metav1.Now()
			v1sp.ObjectMeta.DeletionTimestamp = &time
			v1sp.Finalizers = []string{common.SubnetPortFinalizerName}
			return nil
		})
	k8sClient.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	patchesDeleteSubnetPort = gomonkey.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPort,
		func(s *subnetport.SubnetPortService, uid string) error {
			return nil
		})
	defer patchesDeleteSubnetPort.Reset()
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, nil, ret)

	// handle deletion event - unknown finalizers
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			v1sp := obj.(*v1alpha1.SubnetPort)
			v1sp.Spec.Subnet = "subnet1"
			time := metav1.Now()
			v1sp.ObjectMeta.DeletionTimestamp = &time
			return nil
		})
	patchesDeleteSubnetPort = gomonkey.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPort,
		func(s *subnetport.SubnetPortService, uid string) error {
			assert.FailNow(t, "should not be called")
			return nil
		})
	defer patchesDeleteSubnetPort.Reset()
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, nil, ret)
}

func TestSubnetPortReconciler_GarbageCollector(t *testing.T) {
	// gc collect item "2345", local store has more item than k8s cache
	service := &subnetport.SubnetPortService{
		Service: common.Service{
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
	patchesDeleteSubnetPort := gomonkey.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPort,
		func(s *subnetport.SubnetPortService, uid string) error {
			return nil
		})
	defer patchesDeleteSubnetPort.Reset()
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	r := &SubnetPortReconciler{
		Client:            k8sClient,
		Scheme:            nil,
		SubnetPortService: service,
	}
	subnetPortList := &v1alpha1.SubnetPortList{}
	k8sClient.EXPECT().List(gomock.Any(), subnetPortList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.SubnetPortList)
		a.Items = append(a.Items, v1alpha1.SubnetPort{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "1234"
		a.Items[0].Name = "subnetPort1"
		return nil
	})
	r.CollectGarbage(context.Background())
}
