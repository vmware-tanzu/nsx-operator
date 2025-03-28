// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search (interfaces: QueryClient)

// Package mocks is a generated GoMock package.
package mocks

import (
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"
	model "github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

// MockQueryClient is a mock of QueryClient interface.
type MockQueryClient struct {
	ctrl     *gomock.Controller
	recorder *MockQueryClientMockRecorder
}

// MockQueryClientMockRecorder is the mock recorder for MockQueryClient.
type MockQueryClientMockRecorder struct {
	mock *MockQueryClient
}

// NewMockQueryClient creates a new mock instance.
func NewMockQueryClient(ctrl *gomock.Controller) *MockQueryClient {
	mock := &MockQueryClient{ctrl: ctrl}
	mock.recorder = &MockQueryClientMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockQueryClient) EXPECT() *MockQueryClientMockRecorder {
	return m.recorder
}

// List mocks base method.
func (m *MockQueryClient) List(arg0 string, arg1, arg2 *string, arg3 *int64, arg4 *bool, arg5 *string) (model.SearchResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "List", arg0, arg1, arg2, arg3, arg4, arg5)
	ret0, _ := ret[0].(model.SearchResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// List indicates an expected call of List.
func (mr *MockQueryClientMockRecorder) List(arg0, arg1, arg2, arg3, arg4, arg5 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "List", reflect.TypeOf((*MockQueryClient)(nil).List), arg0, arg1, arg2, arg3, arg4, arg5)
}
