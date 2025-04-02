package inventory

import (
	"net/http"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/mock"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mockClient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	commonservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/inventory"
)

func TestHandleEndpoint(t *testing.T) {
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}
	queue := MockObjectQueue[any]{}
	t.Run("NormalEndpoint", func(t *testing.T) {
		inventoryService, k8sClient := createService(t)
		controller := &InventoryController{
			Client:               k8sClient,
			service:              inventoryService,
			keyBuffer:            sets.New[inventory.InventoryKey](),
			cf:                   cfg,
			inventoryObjectQueue: &queue}

		k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		testEndpoint := &v1.Endpoints{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "deleted-ns",
				Name:      "deleted-endpoint",
				UID:       "deleted-uid",
			},
		}
		deletedObj := cache.DeletedFinalStateUnknown{Obj: testEndpoint}
		queue.On("Add", mock.Anything).Return().Once()
		controller.handleEndpoint(deletedObj)
		queue.AssertExpectations(t)
	})
}

func createService(t *testing.T) (*inventory.InventoryService, *mockClient.MockClient) {
	config2 := nsx.NewConfig("localhost", "1", "1", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{"127.0.0.1"})

	cluster, _ := nsx.NewCluster(config2)
	rc := cluster.NewRestConnector()

	mockCtrl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtrl)
	httpClient := http.DefaultClient
	cf := &config.NSXOperatorConfig{
		CoeConfig: &config.CoeConfig{
			Cluster: "k8scl-one:test",
		},
		NsxConfig: &config.NsxConfig{NsxApiManagers: []string{"127.0.0.1"}},
	}
	nsxApiClient, _ := nsx.CreateNsxtApiClient(cf, httpClient)
	cs := commonservice.Service{
		Client: k8sClient,
		NSXClient: &nsx.Client{
			RestConnector: rc,
			NsxConfig:     cf,
			NsxApiClient:  nsxApiClient,
		},
		NSXConfig: &config.NSXOperatorConfig{
			CoeConfig: &config.CoeConfig{
				Cluster: "k8scl-one:test",
			},
		},
	}

	service, _ := inventory.InitializeService(cs)
	return service, k8sClient
}
