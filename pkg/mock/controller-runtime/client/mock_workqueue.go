package mock_client

import (
	reflect "reflect"
	"time"

	gomock "github.com/golang/mock/gomock"
)

type MockInterface struct {
	ctrl     *gomock.Controller
	recorder *MockInterfaceMockRecorder
}

func (m *MockInterface) ShutDownWithDrain() {
}

func (m *MockInterface) AddAfter(item interface{}, duration time.Duration) {
}

func (m *MockInterface) AddRateLimited(item interface{}) {
}

func (m *MockInterface) Forget(item interface{}) {
}

func (m *MockInterface) NumRequeues(item interface{}) int {
	return 0
}

// MockInterfaceMockRecorder is the mock recorder for MockInterface.
type MockInterfaceMockRecorder struct {
	mock *MockInterface
}

// NewMockInterface creates a new mock instance.
func NewMockInterface(ctrl *gomock.Controller) *MockInterface {
	mock := &MockInterface{ctrl: ctrl}
	mock.recorder = &MockInterfaceMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockInterface) EXPECT() *MockInterfaceMockRecorder {
	return m.recorder
}

// Add mocks base method.
func (m *MockInterface) Add(arg0 interface{}) {
	m.ctrl.T.Helper()
}

// Add indicates an expected call of Add.
func (mr *MockInterfaceMockRecorder) Add(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Add", reflect.TypeOf((*MockInterface)(nil).Add), arg0)
}

// Done mocks base method.
func (m *MockInterface) Done(arg0 interface{}) {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "Done", arg0)
}

// Done indicates an expected call of Done.
func (mr *MockInterfaceMockRecorder) Done(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Done", reflect.TypeOf((*MockInterface)(nil).Done), arg0)
}

// Get mocks base method.
func (m *MockInterface) Get() (interface{}, bool) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Get")
	ret0, _ := ret[0].(interface{})
	ret1, _ := ret[1].(bool)
	return ret0, ret1
}

// Get indicates an expected call of Get.
func (mr *MockInterfaceMockRecorder) Get() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Get", reflect.TypeOf((*MockInterface)(nil).Get))
}

// Len mocks base method.
func (m *MockInterface) Len() int {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Len")
	ret0, _ := ret[0].(int)
	return ret0
}

// Len indicates an expected call of Len.
func (mr *MockInterfaceMockRecorder) Len() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Len", reflect.TypeOf((*MockInterface)(nil).Len))
}

// ShutDown mocks base method.
func (m *MockInterface) ShutDown() {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "ShutDown")
}

// ShutDown indicates an expected call of ShutDown.
func (mr *MockInterfaceMockRecorder) ShutDown() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ShutDown", reflect.TypeOf((*MockInterface)(nil).ShutDown))
}

// ShuttingDown mocks base method.
func (m *MockInterface) ShuttingDown() bool {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ShuttingDown")
	ret0, _ := ret[0].(bool)
	return ret0
}

// ShuttingDown indicates an expected call of ShuttingDown.
func (mr *MockInterfaceMockRecorder) ShuttingDown() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ShuttingDown", reflect.TypeOf((*MockInterface)(nil).ShuttingDown))
}
