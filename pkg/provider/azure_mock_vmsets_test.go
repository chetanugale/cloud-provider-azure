/*
Copyright 2020 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package provider

import (
	context "context"
	reflect "reflect"

	compute "github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2020-12-01/compute"
	network "github.com/Azure/azure-sdk-for-go/services/network/mgmt/2021-02-01/network"
	azure "github.com/Azure/go-autorest/autorest/azure"
	gomock "github.com/golang/mock/gomock"
	v1 "k8s.io/api/core/v1"
	types "k8s.io/apimachinery/pkg/types"
	cloud_provider "k8s.io/cloud-provider"
	cache "sigs.k8s.io/cloud-provider-azure/pkg/cache"
)

// MockVMSet is a mock of VMSet interface.
type MockVMSet struct {
	ctrl     *gomock.Controller
	recorder *MockVMSetMockRecorder
}

// MockVMSetMockRecorder is the mock recorder for MockVMSet.
type MockVMSetMockRecorder struct {
	mock *MockVMSet
}

// NewMockVMSet creates a new mock instance.
func NewMockVMSet(ctrl *gomock.Controller) *MockVMSet {
	mock := &MockVMSet{ctrl: ctrl}
	mock.recorder = &MockVMSetMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockVMSet) EXPECT() *MockVMSetMockRecorder {
	return m.recorder
}

// AttachDisk mocks base method.
func (m *MockVMSet) AttachDisk(nodeName types.NodeName, diskMap map[string]*AttachDiskOptions) (*azure.Future, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "AttachDisk", nodeName, diskMap)
	ret0, _ := ret[0].(*azure.Future)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// AttachDisk indicates an expected call of AttachDisk.
func (mr *MockVMSetMockRecorder) AttachDisk(nodeName, diskMap interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AttachDisk", reflect.TypeOf((*MockVMSet)(nil).AttachDisk), nodeName, diskMap)
}

// DetachDisk mocks base method.
func (m *MockVMSet) DetachDisk(nodeName types.NodeName, diskMap map[string]string) (*azure.Future, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DetachDisk", nodeName, diskMap)
	ret0, _ := ret[0].(*azure.Future)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// DetachDisk indicates an expected call of DetachDisk.
func (mr *MockVMSetMockRecorder) DetachDisk(nodeName, diskMap interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DetachDisk", reflect.TypeOf((*MockVMSet)(nil).DetachDisk), nodeName, diskMap)
}

// EnsureBackendPoolDeleted mocks base method.
func (m *MockVMSet) EnsureBackendPoolDeleted(service *v1.Service, backendPoolID, vmSetName string, backendAddressPools *[]network.BackendAddressPool) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "EnsureBackendPoolDeleted", service, backendPoolID, vmSetName, backendAddressPools)
	ret0, _ := ret[0].(error)
	return ret0
}

// EnsureBackendPoolDeleted indicates an expected call of EnsureBackendPoolDeleted.
func (mr *MockVMSetMockRecorder) EnsureBackendPoolDeleted(service, backendPoolID, vmSetName, backendAddressPools interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "EnsureBackendPoolDeleted", reflect.TypeOf((*MockVMSet)(nil).EnsureBackendPoolDeleted), service, backendPoolID, vmSetName, backendAddressPools)
}

// EnsureBackendPoolDeletedFromVMSets mocks base method.
func (m *MockVMSet) EnsureBackendPoolDeletedFromVMSets(vmSetNamesMap map[string]bool, backendPoolID string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "EnsureBackendPoolDeletedFromVMSets", vmSetNamesMap, backendPoolID)
	ret0, _ := ret[0].(error)
	return ret0
}

// EnsureBackendPoolDeletedFromVMSets indicates an expected call of EnsureBackendPoolDeletedFromVMSets.
func (mr *MockVMSetMockRecorder) EnsureBackendPoolDeletedFromVMSets(vmSetNamesMap, backendPoolID interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "EnsureBackendPoolDeletedFromVMSets", reflect.TypeOf((*MockVMSet)(nil).EnsureBackendPoolDeletedFromVMSets), vmSetNamesMap, backendPoolID)
}

// EnsureHostInPool mocks base method.
func (m *MockVMSet) EnsureHostInPool(service *v1.Service, nodeName types.NodeName, backendPoolID, vmSetName string, isInternal bool) (string, string, string, *compute.VirtualMachineScaleSetVM, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "EnsureHostInPool", service, nodeName, backendPoolID, vmSetName, isInternal)
	ret0, _ := ret[0].(string)
	ret1, _ := ret[1].(string)
	ret2, _ := ret[2].(string)
	ret3, _ := ret[3].(*compute.VirtualMachineScaleSetVM)
	ret4, _ := ret[4].(error)
	return ret0, ret1, ret2, ret3, ret4
}

// EnsureHostInPool indicates an expected call of EnsureHostInPool.
func (mr *MockVMSetMockRecorder) EnsureHostInPool(service, nodeName, backendPoolID, vmSetName, isInternal interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "EnsureHostInPool", reflect.TypeOf((*MockVMSet)(nil).EnsureHostInPool), service, nodeName, backendPoolID, vmSetName, isInternal)
}

// EnsureHostsInPool mocks base method.
func (m *MockVMSet) EnsureHostsInPool(service *v1.Service, nodes []*v1.Node, backendPoolID, vmSetName string, isInternal bool) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "EnsureHostsInPool", service, nodes, backendPoolID, vmSetName, isInternal)
	ret0, _ := ret[0].(error)
	return ret0
}

// EnsureHostsInPool indicates an expected call of EnsureHostsInPool.
func (mr *MockVMSetMockRecorder) EnsureHostsInPool(service, nodes, backendPoolID, vmSetName, isInternal interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "EnsureHostsInPool", reflect.TypeOf((*MockVMSet)(nil).EnsureHostsInPool), service, nodes, backendPoolID, vmSetName, isInternal)
}

// GetAgentPoolVMSetNames mocks base method.
func (m *MockVMSet) GetAgentPoolVMSetNames(nodes []*v1.Node) (*[]string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetAgentPoolVMSetNames", nodes)
	ret0, _ := ret[0].(*[]string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetAgentPoolVMSetNames indicates an expected call of GetAgentPoolVMSetNames.
func (mr *MockVMSetMockRecorder) GetAgentPoolVMSetNames(nodes interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetAgentPoolVMSetNames", reflect.TypeOf((*MockVMSet)(nil).GetAgentPoolVMSetNames), nodes)
}

// GetDataDisks mocks base method.
func (m *MockVMSet) GetDataDisks(nodeName types.NodeName, crt cache.AzureCacheReadType) ([]compute.DataDisk, *string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetDataDisks", nodeName, crt)
	ret0, _ := ret[0].([]compute.DataDisk)
	ret1, _ := ret[1].(*string)
	ret2, _ := ret[2].(error)
	return ret0, ret1, ret2
}

// GetDataDisks indicates an expected call of GetDataDisks.
func (mr *MockVMSetMockRecorder) GetDataDisks(nodeName, crt interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetDataDisks", reflect.TypeOf((*MockVMSet)(nil).GetDataDisks), nodeName, crt)
}

// GetIPByNodeName mocks base method.
func (m *MockVMSet) GetIPByNodeName(name string) (string, string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetIPByNodeName", name)
	ret0, _ := ret[0].(string)
	ret1, _ := ret[1].(string)
	ret2, _ := ret[2].(error)
	return ret0, ret1, ret2
}

// GetIPByNodeName indicates an expected call of GetIPByNodeName.
func (mr *MockVMSetMockRecorder) GetIPByNodeName(name interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetIPByNodeName", reflect.TypeOf((*MockVMSet)(nil).GetIPByNodeName), name)
}

// GetInstanceIDByNodeName mocks base method.
func (m *MockVMSet) GetInstanceIDByNodeName(name string) (string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetInstanceIDByNodeName", name)
	ret0, _ := ret[0].(string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetInstanceIDByNodeName indicates an expected call of GetInstanceIDByNodeName.
func (mr *MockVMSetMockRecorder) GetInstanceIDByNodeName(name interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetInstanceIDByNodeName", reflect.TypeOf((*MockVMSet)(nil).GetInstanceIDByNodeName), name)
}

// GetInstanceTypeByNodeName mocks base method.
func (m *MockVMSet) GetInstanceTypeByNodeName(name string) (string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetInstanceTypeByNodeName", name)
	ret0, _ := ret[0].(string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetInstanceTypeByNodeName indicates an expected call of GetInstanceTypeByNodeName.
func (mr *MockVMSetMockRecorder) GetInstanceTypeByNodeName(name interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetInstanceTypeByNodeName", reflect.TypeOf((*MockVMSet)(nil).GetInstanceTypeByNodeName), name)
}

// GetNodeCIDRMasksByProviderID mocks base method.
func (m *MockVMSet) GetNodeCIDRMasksByProviderID(providerID string) (int, int, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetNodeCIDRMasksByProviderID", providerID)
	ret0, _ := ret[0].(int)
	ret1, _ := ret[1].(int)
	ret2, _ := ret[2].(error)
	return ret0, ret1, ret2
}

// GetNodeCIDRMasksByProviderID indicates an expected call of GetNodeCIDRMasksByProviderID.
func (mr *MockVMSetMockRecorder) GetNodeCIDRMasksByProviderID(providerID interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetNodeCIDRMasksByProviderID", reflect.TypeOf((*MockVMSet)(nil).GetNodeCIDRMasksByProviderID), providerID)
}

// GetNodeNameByIPConfigurationID mocks base method.
func (m *MockVMSet) GetNodeNameByIPConfigurationID(ipConfigurationID string) (string, string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetNodeNameByIPConfigurationID", ipConfigurationID)
	ret0, _ := ret[0].(string)
	ret1, _ := ret[1].(string)
	ret2, _ := ret[2].(error)
	return ret0, ret1, ret2
}

// GetNodeNameByIPConfigurationID indicates an expected call of GetNodeNameByIPConfigurationID.
func (mr *MockVMSetMockRecorder) GetNodeNameByIPConfigurationID(ipConfigurationID interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetNodeNameByIPConfigurationID", reflect.TypeOf((*MockVMSet)(nil).GetNodeNameByIPConfigurationID), ipConfigurationID)
}

// GetNodeNameByProviderID mocks base method.
func (m *MockVMSet) GetNodeNameByProviderID(providerID string) (types.NodeName, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetNodeNameByProviderID", providerID)
	ret0, _ := ret[0].(types.NodeName)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetNodeNameByProviderID indicates an expected call of GetNodeNameByProviderID.
func (mr *MockVMSetMockRecorder) GetNodeNameByProviderID(providerID interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetNodeNameByProviderID", reflect.TypeOf((*MockVMSet)(nil).GetNodeNameByProviderID), providerID)
}

// GetPowerStatusByNodeName mocks base method.
func (m *MockVMSet) GetPowerStatusByNodeName(name string) (string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetPowerStatusByNodeName", name)
	ret0, _ := ret[0].(string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetPowerStatusByNodeName indicates an expected call of GetPowerStatusByNodeName.
func (mr *MockVMSetMockRecorder) GetPowerStatusByNodeName(name interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetPowerStatusByNodeName", reflect.TypeOf((*MockVMSet)(nil).GetPowerStatusByNodeName), name)
}

// GetPrimaryInterface mocks base method.
func (m *MockVMSet) GetPrimaryInterface(nodeName string) (network.Interface, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetPrimaryInterface", nodeName)
	ret0, _ := ret[0].(network.Interface)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetPrimaryInterface indicates an expected call of GetPrimaryInterface.
func (mr *MockVMSetMockRecorder) GetPrimaryInterface(nodeName interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetPrimaryInterface", reflect.TypeOf((*MockVMSet)(nil).GetPrimaryInterface), nodeName)
}

// GetPrimaryVMSetName mocks base method.
func (m *MockVMSet) GetPrimaryVMSetName() string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetPrimaryVMSetName")
	ret0, _ := ret[0].(string)
	return ret0
}

// GetPrimaryVMSetName indicates an expected call of GetPrimaryVMSetName.
func (mr *MockVMSetMockRecorder) GetPrimaryVMSetName() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetPrimaryVMSetName", reflect.TypeOf((*MockVMSet)(nil).GetPrimaryVMSetName))
}

// GetPrivateIPsByNodeName mocks base method.
func (m *MockVMSet) GetPrivateIPsByNodeName(name string) ([]string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetPrivateIPsByNodeName", name)
	ret0, _ := ret[0].([]string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetPrivateIPsByNodeName indicates an expected call of GetPrivateIPsByNodeName.
func (mr *MockVMSetMockRecorder) GetPrivateIPsByNodeName(name interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetPrivateIPsByNodeName", reflect.TypeOf((*MockVMSet)(nil).GetPrivateIPsByNodeName), name)
}

// GetProvisioningStateByNodeName mocks base method.
func (m *MockVMSet) GetProvisioningStateByNodeName(name string) (string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetProvisioningStateByNodeName", name)
	ret0, _ := ret[0].(string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetProvisioningStateByNodeName indicates an expected call of GetProvisioningStateByNodeName.
func (mr *MockVMSetMockRecorder) GetProvisioningStateByNodeName(name interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetProvisioningStateByNodeName", reflect.TypeOf((*MockVMSet)(nil).GetProvisioningStateByNodeName), name)
}

// GetVMSetNames mocks base method.
func (m *MockVMSet) GetVMSetNames(service *v1.Service, nodes []*v1.Node) (*[]string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetVMSetNames", service, nodes)
	ret0, _ := ret[0].(*[]string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetVMSetNames indicates an expected call of GetVMSetNames.
func (mr *MockVMSetMockRecorder) GetVMSetNames(service, nodes interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetVMSetNames", reflect.TypeOf((*MockVMSet)(nil).GetVMSetNames), service, nodes)
}

// GetZoneByNodeName mocks base method.
func (m *MockVMSet) GetZoneByNodeName(name string) (cloud_provider.Zone, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetZoneByNodeName", name)
	ret0, _ := ret[0].(cloud_provider.Zone)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetZoneByNodeName indicates an expected call of GetZoneByNodeName.
func (mr *MockVMSetMockRecorder) GetZoneByNodeName(name interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetZoneByNodeName", reflect.TypeOf((*MockVMSet)(nil).GetZoneByNodeName), name)
}

// UpdateVM mocks base method.
func (m *MockVMSet) UpdateVM(nodeName types.NodeName) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UpdateVM", nodeName)
	ret0, _ := ret[0].(error)
	return ret0
}

// UpdateVM indicates an expected call of UpdateVM.
func (mr *MockVMSetMockRecorder) UpdateVM(nodeName interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UpdateVM", reflect.TypeOf((*MockVMSet)(nil).UpdateVM), nodeName)
}

// WaitForUpdateResult mocks base method.
func (m *MockVMSet) WaitForUpdateResult(ctx context.Context, future *azure.Future, resourceGroupName, source string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "WaitForUpdateResult", ctx, future, resourceGroupName, source)
	ret0, _ := ret[0].(error)
	return ret0
}

// WaitForUpdateResult indicates an expected call of WaitForUpdateResult.
func (mr *MockVMSetMockRecorder) WaitForUpdateResult(ctx, future, resourceGroupName, source interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "WaitForUpdateResult", reflect.TypeOf((*MockVMSet)(nil).WaitForUpdateResult), ctx, future, resourceGroupName, source)
}
