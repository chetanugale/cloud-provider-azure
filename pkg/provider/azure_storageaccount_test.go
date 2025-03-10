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
	"errors"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2021-02-01/network"
	"github.com/Azure/azure-sdk-for-go/services/storage/mgmt/2021-02-01/storage"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/privatednsclient/mockprivatednsclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/privatednszonegroupclient/mockprivatednszonegroupclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/privateendpointclient/mockprivateendpointclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/storageaccountclient/mockstorageaccountclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/subnetclient/mocksubnetclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/virtualnetworklinksclient/mockvirtualnetworklinksclient"
)

const TestLocation = "testLocation"

func TestGetStorageAccessKeys(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cloud := &Cloud{}
	value := "foo bar"

	tests := []struct {
		results     storage.AccountListKeysResult
		expectedKey string
		expectErr   bool
		err         error
	}{
		{storage.AccountListKeysResult{}, "", true, nil},
		{
			storage.AccountListKeysResult{
				Keys: &[]storage.AccountKey{
					{Value: &value},
				},
			},
			"bar",
			false,
			nil,
		},
		{
			storage.AccountListKeysResult{
				Keys: &[]storage.AccountKey{
					{},
					{Value: &value},
				},
			},
			"bar",
			false,
			nil,
		},
		{storage.AccountListKeysResult{}, "", true, fmt.Errorf("test error")},
	}

	for _, test := range tests {
		mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
		cloud.StorageAccountClient = mockStorageAccountsClient
		mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), "rg", gomock.Any()).Return(test.results, nil).AnyTimes()
		key, err := cloud.GetStorageAccesskey("acct", "rg")
		if test.expectErr && err == nil {
			t.Errorf("Unexpected non-error")
			continue
		}
		if !test.expectErr && err != nil {
			t.Errorf("Unexpected error: %v", err)
			continue
		}
		if key != test.expectedKey {
			t.Errorf("expected: %s, saw %s", test.expectedKey, key)
		}
	}
}

func TestGetStorageAccount(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cloud := &Cloud{}

	name := "testAccount"
	location := TestLocation
	networkID := "networkID"
	accountProperties := storage.AccountProperties{
		NetworkRuleSet: &storage.NetworkRuleSet{
			VirtualNetworkRules: &[]storage.VirtualNetworkRule{
				{
					VirtualNetworkResourceID: &networkID,
					Action:                   storage.ActionAllow,
					State:                    "state",
				},
			},
		}}

	account := storage.Account{
		Sku: &storage.Sku{
			Name: "testSku",
			Tier: "testSkuTier",
		},
		Kind:              "testKind",
		Location:          &location,
		Name:              &name,
		AccountProperties: &accountProperties,
	}

	testResourceGroups := []storage.Account{account}

	accountOptions := &AccountOptions{
		ResourceGroup:             "rg",
		VirtualNetworkResourceIDs: []string{networkID},
	}

	mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
	cloud.StorageAccountClient = mockStorageAccountsClient

	mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), "rg").Return(testResourceGroups, nil).Times(1)

	accountsWithLocations, err := cloud.getStorageAccounts(accountOptions)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if accountsWithLocations == nil {
		t.Error("unexpected error as returned accounts are nil")
	}

	if len(accountsWithLocations) == 0 {
		t.Error("unexpected error as returned accounts slice is empty")
	}

	expectedAccountWithLocation := accountWithLocation{
		Name:        "testAccount",
		StorageType: "testSku",
		Location:    TestLocation,
	}

	accountWithLocation := accountsWithLocations[0]
	if accountWithLocation.Name != expectedAccountWithLocation.Name {
		t.Errorf("expected %s, but was %s", accountWithLocation.Name, expectedAccountWithLocation.Name)
	}

	if accountWithLocation.StorageType != expectedAccountWithLocation.StorageType {
		t.Errorf("expected %s, but was %s", accountWithLocation.StorageType, expectedAccountWithLocation.StorageType)
	}

	if accountWithLocation.Location != expectedAccountWithLocation.Location {
		t.Errorf("expected %s, but was %s", accountWithLocation.Location, expectedAccountWithLocation.Location)
	}
}

func TestGetStorageAccountEdgeCases(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cloud := &Cloud{}

	// default account with name, location, sku, kind
	name := "testAccount"
	location := TestLocation
	sku := &storage.Sku{
		Name: "testSku",
		Tier: "testSkuTier",
	}
	account := storage.Account{
		Sku:      sku,
		Kind:     "testKind",
		Location: &location,
		Name:     &name,
	}

	accountPropertiesWithoutNetworkRuleSet := storage.AccountProperties{NetworkRuleSet: nil}
	accountPropertiesWithoutVirtualNetworkRules := storage.AccountProperties{
		NetworkRuleSet: &storage.NetworkRuleSet{
			VirtualNetworkRules: nil,
		}}

	tests := []struct {
		testCase           string
		testAccountOptions *AccountOptions
		testResourceGroups []storage.Account
		expectedResult     []accountWithLocation
		expectedError      error
	}{
		{
			testCase: "account name is nil",
			testAccountOptions: &AccountOptions{
				ResourceGroup: "rg",
			},
			testResourceGroups: []storage.Account{},
			expectedResult:     []accountWithLocation{},
			expectedError:      nil,
		},
		{
			testCase: "account location is nil",
			testAccountOptions: &AccountOptions{
				ResourceGroup: "rg",
			},
			testResourceGroups: []storage.Account{{Name: &name}},
			expectedResult:     []accountWithLocation{},
			expectedError:      nil,
		},
		{
			testCase: "account sku is nil",
			testAccountOptions: &AccountOptions{
				ResourceGroup: "rg",
			},
			testResourceGroups: []storage.Account{{Name: &name, Location: &location}},
			expectedResult:     []accountWithLocation{},
			expectedError:      nil,
		},
		{
			testCase: "account options type is not empty and not equal account storage type",
			testAccountOptions: &AccountOptions{
				ResourceGroup: "rg",
				Type:          "testAccountOptionsType",
			},
			testResourceGroups: []storage.Account{account},
			expectedResult:     []accountWithLocation{},
			expectedError:      nil,
		},
		{
			testCase: "account options kind is not empty and not equal account type",
			testAccountOptions: &AccountOptions{
				ResourceGroup: "rg",
				Kind:          "testAccountOptionsKind",
			},
			testResourceGroups: []storage.Account{account},
			expectedResult:     []accountWithLocation{},
			expectedError:      nil,
		},
		{
			testCase: "account options location is not empty and not equal account location",
			testAccountOptions: &AccountOptions{
				ResourceGroup: "rg",
				Location:      "testAccountOptionsLocation",
			},
			testResourceGroups: []storage.Account{account},
			expectedResult:     []accountWithLocation{},
			expectedError:      nil,
		},
		{
			testCase: "account options account properties are nil",
			testAccountOptions: &AccountOptions{
				ResourceGroup:             "rg",
				VirtualNetworkResourceIDs: []string{"id"},
			},
			testResourceGroups: []storage.Account{},
			expectedResult:     []accountWithLocation{},
			expectedError:      nil,
		},
		{
			testCase: "account options account properties network rule set is nil",
			testAccountOptions: &AccountOptions{
				ResourceGroup:             "rg",
				VirtualNetworkResourceIDs: []string{"id"},
			},
			testResourceGroups: []storage.Account{{Name: &name, Kind: "kind", Location: &location, Sku: sku, AccountProperties: &accountPropertiesWithoutNetworkRuleSet}},
			expectedResult:     []accountWithLocation{},
			expectedError:      nil,
		},
		{
			testCase: "account options account properties virtual network rule is nil",
			testAccountOptions: &AccountOptions{
				ResourceGroup:             "rg",
				VirtualNetworkResourceIDs: []string{"id"},
			},
			testResourceGroups: []storage.Account{{Name: &name, Kind: "kind", Location: &location, Sku: sku, AccountProperties: &accountPropertiesWithoutVirtualNetworkRules}},
			expectedResult:     []accountWithLocation{},
			expectedError:      nil,
		},
		{
			testCase: "account options CreatePrivateEndpoint is true and no private endpoint exists",
			testAccountOptions: &AccountOptions{
				ResourceGroup:         "rg",
				CreatePrivateEndpoint: true,
			},
			testResourceGroups: []storage.Account{{Name: &name, Kind: "kind", Location: &location, Sku: sku, AccountProperties: &storage.AccountProperties{}}},
			expectedResult:     []accountWithLocation{},
			expectedError:      nil,
		},
	}

	for _, test := range tests {
		t.Logf("running test case: %s", test.testCase)
		mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
		cloud.StorageAccountClient = mockStorageAccountsClient

		mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), "rg").Return(test.testResourceGroups, nil).AnyTimes()

		accountsWithLocations, err := cloud.getStorageAccounts(test.testAccountOptions)
		if !errors.Is(err, test.expectedError) {
			t.Errorf("unexpected error: %v", err)
		}

		if len(accountsWithLocations) != len(test.expectedResult) {
			t.Error("unexpected error as returned accounts slice is not empty")
		}
	}
}

func TestEnsureStorageAccountWithPrivateEndpoint(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	resourceGroup := "ResourceGroup"
	vnetName := "VnetName"
	vnetResourceGroup := "VnetResourceGroup"
	subnetName := "SubnetName"
	location := TestLocation

	cloud := &Cloud{}
	cloud.ResourceGroup = resourceGroup
	cloud.VnetResourceGroup = vnetResourceGroup
	cloud.VnetName = vnetName
	cloud.SubnetName = subnetName
	cloud.Location = location
	cloud.SubscriptionID = "testSub"

	name := "testStorageAccount"
	sku := &storage.Sku{
		Name: "testSku",
		Tier: "testSkuTier",
	}
	testStorageAccounts :=
		[]storage.Account{
			{Name: &name, Kind: "kind", Location: &location, Sku: sku, AccountProperties: &storage.AccountProperties{NetworkRuleSet: &storage.NetworkRuleSet{}}}}

	value := "foo bar"
	storageAccountListKeys := storage.AccountListKeysResult{
		Keys: &[]storage.AccountKey{
			{Value: &value},
		},
	}

	mockStorageAccountsClient := mockstorageaccountclient.NewMockInterface(ctrl)
	mockStorageAccountsClient.EXPECT().ListByResourceGroup(gomock.Any(), gomock.Any()).Return(testStorageAccounts, nil).AnyTimes()
	mockStorageAccountsClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mockStorageAccountsClient.EXPECT().GetProperties(gomock.Any(), gomock.Any(), gomock.Any()).Return(testStorageAccounts[0], nil).AnyTimes()
	mockStorageAccountsClient.EXPECT().ListKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(storageAccountListKeys, nil).AnyTimes()
	cloud.StorageAccountClient = mockStorageAccountsClient

	subnet := network.Subnet{SubnetPropertiesFormat: &network.SubnetPropertiesFormat{}}
	mockSubnetsClient := mocksubnetclient.NewMockInterface(ctrl)
	mockSubnetsClient.EXPECT().Get(gomock.Any(), vnetResourceGroup, vnetName, subnetName, gomock.Any()).Return(subnet, nil).Times(1)
	mockSubnetsClient.EXPECT().CreateOrUpdate(gomock.Any(), vnetResourceGroup, vnetName, subnetName, gomock.Any()).Return(nil).Times(1)
	cloud.SubnetsClient = mockSubnetsClient

	mockPrivateDNSClient := mockprivatednsclient.NewMockInterface(ctrl)
	mockPrivateDNSClient.EXPECT().CreateOrUpdate(gomock.Any(), vnetResourceGroup, gomock.Any(), gomock.Any(), true).Return(nil).Times(1)
	cloud.privatednsclient = mockPrivateDNSClient

	mockPrivateDNSZoneGroup := mockprivatednszonegroupclient.NewMockInterface(ctrl)
	mockPrivateDNSZoneGroup.EXPECT().CreateOrUpdate(gomock.Any(), vnetResourceGroup, gomock.Any(), gomock.Any(), gomock.Any(), false).Return(nil).Times(1)
	cloud.privatednszonegroupclient = mockPrivateDNSZoneGroup

	mockPrivateEndpointClient := mockprivateendpointclient.NewMockInterface(ctrl)
	mockPrivateEndpointClient.EXPECT().CreateOrUpdate(gomock.Any(), vnetResourceGroup, gomock.Any(), gomock.Any(), true).Return(nil).Times(1)
	cloud.privateendpointclient = mockPrivateEndpointClient

	mockVirtualNetworkLinksClient := mockvirtualnetworklinksclient.NewMockInterface(ctrl)
	mockVirtualNetworkLinksClient.EXPECT().CreateOrUpdate(gomock.Any(), vnetResourceGroup, gomock.Any(), gomock.Any(), gomock.Any(), false).Return(nil).Times(1)
	cloud.virtualNetworkLinksClient = mockVirtualNetworkLinksClient

	testAccountOptions := &AccountOptions{
		ResourceGroup:         "rg",
		CreatePrivateEndpoint: true,
	}

	if _, _, err := cloud.EnsureStorageAccount(testAccountOptions, "test"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestIsPrivateEndpointAsExpected(t *testing.T) {
	tests := []struct {
		account        storage.Account
		accountOptions *AccountOptions
		expectedResult bool
	}{
		{
			account: storage.Account{
				AccountProperties: &storage.AccountProperties{
					PrivateEndpointConnections: &[]storage.PrivateEndpointConnection{{}},
				},
			},
			accountOptions: &AccountOptions{
				CreatePrivateEndpoint: true,
			},
			expectedResult: true,
		},
		{
			account: storage.Account{
				AccountProperties: &storage.AccountProperties{
					PrivateEndpointConnections: nil,
				},
			},
			accountOptions: &AccountOptions{
				CreatePrivateEndpoint: false,
			},
			expectedResult: true,
		},
		{
			account: storage.Account{
				AccountProperties: &storage.AccountProperties{
					PrivateEndpointConnections: &[]storage.PrivateEndpointConnection{{}},
				},
			},
			accountOptions: &AccountOptions{
				CreatePrivateEndpoint: false,
			},
			expectedResult: false,
		},
		{
			account: storage.Account{
				AccountProperties: &storage.AccountProperties{
					PrivateEndpointConnections: nil,
				},
			},
			accountOptions: &AccountOptions{
				CreatePrivateEndpoint: true,
			},
			expectedResult: false,
		},
	}

	for _, test := range tests {
		result := isPrivateEndpointAsExpected(test.account, test.accountOptions)
		assert.Equal(t, result, test.expectedResult)
	}
}
