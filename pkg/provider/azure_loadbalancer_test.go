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
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2020-12-01/compute"
	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2021-02-01/network"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/interfaceclient/mockinterfaceclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/loadbalancerclient/mockloadbalancerclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/publicipclient/mockpublicipclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/securitygroupclient/mocksecuritygroupclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/subnetclient/mocksubnetclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/vmclient/mockvmclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/vmssclient/mockvmssclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/consts"
	"sigs.k8s.io/cloud-provider-azure/pkg/retry"
)

// LBInUseRawError is the LoadBalancerInUseByVirtualMachineScaleSet raw error
const LBInUseRawError = `{
	"error": {
    	"code": "LoadBalancerInUseByVirtualMachineScaleSet",
    	"message": "Cannot delete load balancer /subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/lb since its child resources lb are in use by virtual machine scale set /subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachineScaleSets/vmss.",
    	"details": []
  	}
}`

func TestFindProbe(t *testing.T) {
	tests := []struct {
		msg           string
		existingProbe []network.Probe
		curProbe      network.Probe
		expected      bool
	}{
		{
			msg:      "empty existing probes should return false",
			expected: false,
		},
		{
			msg: "probe names match while ports don't should return false",
			existingProbe: []network.Probe{
				{
					Name: to.StringPtr("httpProbe"),
					ProbePropertiesFormat: &network.ProbePropertiesFormat{
						Port: to.Int32Ptr(1),
					},
				},
			},
			curProbe: network.Probe{
				Name: to.StringPtr("httpProbe"),
				ProbePropertiesFormat: &network.ProbePropertiesFormat{
					Port: to.Int32Ptr(2),
				},
			},
			expected: false,
		},
		{
			msg: "probe ports match while names don't should return false",
			existingProbe: []network.Probe{
				{
					Name: to.StringPtr("probe1"),
					ProbePropertiesFormat: &network.ProbePropertiesFormat{
						Port: to.Int32Ptr(1),
					},
				},
			},
			curProbe: network.Probe{
				Name: to.StringPtr("probe2"),
				ProbePropertiesFormat: &network.ProbePropertiesFormat{
					Port: to.Int32Ptr(1),
				},
			},
			expected: false,
		},
		{
			msg: "both probe ports and names match should return true",
			existingProbe: []network.Probe{
				{
					Name: to.StringPtr("matchName"),
					ProbePropertiesFormat: &network.ProbePropertiesFormat{
						Port: to.Int32Ptr(1),
					},
				},
			},
			curProbe: network.Probe{
				Name: to.StringPtr("matchName"),
				ProbePropertiesFormat: &network.ProbePropertiesFormat{
					Port: to.Int32Ptr(1),
				},
			},
			expected: true,
		},
	}

	for i, test := range tests {
		findResult := findProbe(test.existingProbe, test.curProbe)
		assert.Equal(t, test.expected, findResult, fmt.Sprintf("TestCase[%d]: %s", i, test.msg))
	}
}

func TestFindRule(t *testing.T) {
	tests := []struct {
		msg          string
		existingRule []network.LoadBalancingRule
		curRule      network.LoadBalancingRule
		expected     bool
	}{
		{
			msg:      "empty existing rules should return false",
			expected: false,
		},
		{
			msg: "rule names don't match should return false",
			existingRule: []network.LoadBalancingRule{
				{
					Name: to.StringPtr("httpProbe1"),
					LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
						FrontendPort: to.Int32Ptr(1),
					},
				},
			},
			curRule: network.LoadBalancingRule{
				Name: to.StringPtr("httpProbe2"),
				LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
					FrontendPort: to.Int32Ptr(1),
				},
			},
			expected: false,
		},
		{
			msg: "rule names match while frontend ports don't should return false",
			existingRule: []network.LoadBalancingRule{
				{
					Name: to.StringPtr("httpProbe"),
					LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
						FrontendPort: to.Int32Ptr(1),
					},
				},
			},
			curRule: network.LoadBalancingRule{
				Name: to.StringPtr("httpProbe"),
				LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
					FrontendPort: to.Int32Ptr(2),
				},
			},
			expected: false,
		},
		{
			msg: "rule names match while backend ports don't should return false",
			existingRule: []network.LoadBalancingRule{
				{
					Name: to.StringPtr("httpProbe"),
					LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
						BackendPort: to.Int32Ptr(1),
					},
				},
			},
			curRule: network.LoadBalancingRule{
				Name: to.StringPtr("httpProbe"),
				LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
					BackendPort: to.Int32Ptr(2),
				},
			},
			expected: false,
		},
		{
			msg: "rule names match while idletimeout don't should return false",
			existingRule: []network.LoadBalancingRule{
				{
					Name: to.StringPtr("httpRule"),
					LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
						IdleTimeoutInMinutes: to.Int32Ptr(1),
					},
				},
			},
			curRule: network.LoadBalancingRule{
				Name: to.StringPtr("httpRule"),
				LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
					IdleTimeoutInMinutes: to.Int32Ptr(2),
				},
			},
			expected: false,
		},
		{
			msg: "rule names match while idletimeout nil should return true",
			existingRule: []network.LoadBalancingRule{
				{
					Name:                              to.StringPtr("httpRule"),
					LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{},
				},
			},
			curRule: network.LoadBalancingRule{
				Name: to.StringPtr("httpRule"),
				LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
					IdleTimeoutInMinutes: to.Int32Ptr(2),
				},
			},
			expected: true,
		},
		{
			msg: "rule names match while LoadDistribution don't should return false",
			existingRule: []network.LoadBalancingRule{
				{
					Name: to.StringPtr("probe1"),
					LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
						LoadDistribution: network.LoadDistributionSourceIP,
					},
				},
			},
			curRule: network.LoadBalancingRule{
				Name: to.StringPtr("probe2"),
				LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
					LoadDistribution: network.LoadDistributionSourceIP,
				},
			},
			expected: false,
		},
		{
			msg: "both rule names and LoadBalancingRulePropertiesFormats match should return true",
			existingRule: []network.LoadBalancingRule{
				{
					Name: to.StringPtr("matchName"),
					LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
						BackendPort:      to.Int32Ptr(2),
						FrontendPort:     to.Int32Ptr(2),
						LoadDistribution: network.LoadDistributionSourceIP,
					},
				},
			},
			curRule: network.LoadBalancingRule{
				Name: to.StringPtr("matchName"),
				LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
					BackendPort:      to.Int32Ptr(2),
					FrontendPort:     to.Int32Ptr(2),
					LoadDistribution: network.LoadDistributionSourceIP,
				},
			},
			expected: true,
		},
	}

	for i, test := range tests {
		findResult := findRule(test.existingRule, test.curRule, true)
		assert.Equal(t, test.expected, findResult, fmt.Sprintf("TestCase[%d]: %s", i, test.msg))
	}
}

func TestGetIdleTimeout(t *testing.T) {
	for _, c := range []struct {
		desc        string
		annotations map[string]string
		i           *int32
		err         bool
	}{
		{desc: "no annotation"},
		{desc: "annotation empty value", annotations: map[string]string{consts.ServiceAnnotationLoadBalancerIdleTimeout: ""}, err: true},
		{desc: "annotation not a number", annotations: map[string]string{consts.ServiceAnnotationLoadBalancerIdleTimeout: "cookies"}, err: true},
		{desc: "annotation negative value", annotations: map[string]string{consts.ServiceAnnotationLoadBalancerIdleTimeout: "-6"}, err: true},
		{desc: "annotation zero value", annotations: map[string]string{consts.ServiceAnnotationLoadBalancerIdleTimeout: "0"}, err: true},
		{desc: "annotation too low value", annotations: map[string]string{consts.ServiceAnnotationLoadBalancerIdleTimeout: "3"}, err: true},
		{desc: "annotation too high value", annotations: map[string]string{consts.ServiceAnnotationLoadBalancerIdleTimeout: "31"}, err: true},
		{desc: "annotation good value", annotations: map[string]string{consts.ServiceAnnotationLoadBalancerIdleTimeout: "24"}, i: to.Int32Ptr(24)},
	} {
		t.Run(c.desc, func(t *testing.T) {
			s := &v1.Service{}
			s.Annotations = c.annotations
			i, err := getIdleTimeout(s)

			if !reflect.DeepEqual(c.i, i) {
				t.Fatalf("got unexpected value: %d", to.Int32(i))
			}
			if (err != nil) != c.err {
				t.Fatalf("expected error=%v, got %v", c.err, err)
			}
		})
	}
}

func TestSubnet(t *testing.T) {
	for i, c := range []struct {
		desc     string
		service  *v1.Service
		expected *string
	}{
		{
			desc:     "No annotation should return nil",
			service:  &v1.Service{},
			expected: nil,
		},
		{
			desc: "annotation with subnet but no ILB should return nil",
			service: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						consts.ServiceAnnotationLoadBalancerInternalSubnet: "subnet",
					},
				},
			},
			expected: nil,
		},
		{
			desc: "annotation with subnet but ILB=false should return nil",
			service: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						consts.ServiceAnnotationLoadBalancerInternalSubnet: "subnet",
						consts.ServiceAnnotationLoadBalancerInternal:       "false",
					},
				},
			},
			expected: nil,
		},
		{
			desc: "annotation with empty subnet should return nil",
			service: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						consts.ServiceAnnotationLoadBalancerInternalSubnet: "",
						consts.ServiceAnnotationLoadBalancerInternal:       "true",
					},
				},
			},
			expected: nil,
		},
		{
			desc: "annotation with subnet and ILB should return subnet",
			service: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						consts.ServiceAnnotationLoadBalancerInternalSubnet: "subnet",
						consts.ServiceAnnotationLoadBalancerInternal:       "true",
					},
				},
			},
			expected: to.StringPtr("subnet"),
		},
	} {
		real := subnet(c.service)
		assert.Equal(t, c.expected, real, fmt.Sprintf("TestCase[%d]: %s", i, c.desc))
	}
}

func TestEnsureLoadBalancerDeleted(t *testing.T) {
	const vmCount = 8
	const availabilitySetCount = 4

	tests := []struct {
		desc              string
		service           v1.Service
		isInternalSvc     bool
		expectCreateError bool
		wrongRGAtDelete   bool
	}{
		{
			desc:    "external service should be created and deleted successfully",
			service: getTestService("service1", v1.ProtocolTCP, nil, false, 80),
		},
		{
			desc:          "internal service should be created and deleted successfully",
			service:       getInternalTestService("service2", 80),
			isInternalSvc: true,
		},
		{
			desc:    "annotated service with same resourceGroup should be created and deleted successfully",
			service: getResourceGroupTestService("service3", "rg", "", 80),
		},
		{
			desc:              "annotated service with different resourceGroup shouldn't be created but should be deleted successfully",
			service:           getResourceGroupTestService("service4", "random-rg", "1.2.3.4", 80),
			expectCreateError: true,
		},
		{
			desc:              "annotated service with different resourceGroup shouldn't be created but should be deleted successfully",
			service:           getResourceGroupTestService("service5", "random-rg", "", 80),
			expectCreateError: true,
			wrongRGAtDelete:   true,
		},
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	az := GetTestCloud(ctrl)

	clusterResources, expectedInterfaces, expectedVirtualMachines := getClusterResources(az, vmCount, availabilitySetCount)
	setMockEnv(az, ctrl, expectedInterfaces, expectedVirtualMachines, 4)

	for i, c := range tests {

		if c.service.Annotations[consts.ServiceAnnotationLoadBalancerInternal] == "true" {
			validateTestSubnet(t, az, &c.service)
		}

		expectedLBs := make([]network.LoadBalancer, 0)
		setMockLBs(az, ctrl, &expectedLBs, "service", 1, i+1, c.isInternalSvc)

		// create the service first.
		lbStatus, err := az.EnsureLoadBalancer(context.TODO(), testClusterName, &c.service, clusterResources.nodes)
		if c.expectCreateError {
			assert.NotNil(t, err, "TestCase[%d]: %s", i, c.desc)
		} else {
			assert.Nil(t, err, "TestCase[%d]: %s", i, c.desc)
			assert.NotNil(t, lbStatus, "TestCase[%d]: %s", i, c.desc)
			result, rerr := az.LoadBalancerClient.List(context.TODO(), az.Config.ResourceGroup)
			assert.Nil(t, rerr, "TestCase[%d]: %s", i, c.desc)
			assert.Equal(t, len(result), 1, "TestCase[%d]: %s", i, c.desc)
			assert.Equal(t, len(*result[0].LoadBalancingRules), 1, "TestCase[%d]: %s", i, c.desc)
		}

		expectedLBs = make([]network.LoadBalancer, 0)
		setMockLBs(az, ctrl, &expectedLBs, "service", 1, i+1, c.isInternalSvc)
		// finally, delete it.
		if c.wrongRGAtDelete {
			az.LoadBalancerResourceGroup = "nil"
		}
		err = az.EnsureLoadBalancerDeleted(context.TODO(), testClusterName, &c.service)
		expectedLBs = make([]network.LoadBalancer, 0)
		mockLBsClient := mockloadbalancerclient.NewMockInterface(ctrl)
		mockLBsClient.EXPECT().List(gomock.Any(), az.Config.ResourceGroup).Return(expectedLBs, nil)
		az.LoadBalancerClient = mockLBsClient
		assert.Nil(t, err, "TestCase[%d]: %s", i, c.desc)
		result, rerr := az.LoadBalancerClient.List(context.Background(), az.Config.ResourceGroup)
		assert.Nil(t, rerr, "TestCase[%d]: %s", i, c.desc)
		assert.Equal(t, 0, len(result), "TestCase[%d]: %s", i, c.desc)
	}
}

func TestServiceOwnsPublicIP(t *testing.T) {
	tests := []struct {
		desc                    string
		pip                     *network.PublicIPAddress
		clusterName             string
		serviceName             string
		serviceLBIP             string
		expectedOwns            bool
		expectedUserAssignedPIP bool
	}{
		{
			desc:         "false should be returned when pip is nil",
			clusterName:  "kubernetes",
			serviceName:  "nginx",
			expectedOwns: false,
		},
		{
			desc: "false should be returned when service name tag doesn't match",
			pip: &network.PublicIPAddress{
				Tags: map[string]*string{
					consts.ServiceTagKey: to.StringPtr("default/nginx"),
				},
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					IPAddress: to.StringPtr("1.2.3.4"),
				},
			},
			serviceName:  "web",
			expectedOwns: false,
		},
		{
			desc: "true should be returned when service name tag matches and cluster name tag is not set",
			pip: &network.PublicIPAddress{
				Tags: map[string]*string{
					consts.ServiceTagKey: to.StringPtr("default/nginx"),
				},
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					IPAddress: to.StringPtr("1.2.3.4"),
				},
			},
			clusterName:  "kubernetes",
			serviceName:  "nginx",
			expectedOwns: true,
		},
		{
			desc: "false should be returned when cluster name doesn't match",
			pip: &network.PublicIPAddress{
				Tags: map[string]*string{
					consts.ServiceTagKey:  to.StringPtr("default/nginx"),
					consts.ClusterNameKey: to.StringPtr("kubernetes"),
				},
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					IPAddress: to.StringPtr("1.2.3.4"),
				},
			},
			clusterName:  "k8s",
			serviceName:  "nginx",
			expectedOwns: false,
		},
		{
			desc: "false should be returned when cluster name matches while service name doesn't match",
			pip: &network.PublicIPAddress{
				Tags: map[string]*string{
					consts.ServiceTagKey:  to.StringPtr("default/web"),
					consts.ClusterNameKey: to.StringPtr("kubernetes"),
				},
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					IPAddress: to.StringPtr("1.2.3.4"),
				},
			},
			clusterName:  "kubernetes",
			serviceName:  "nginx",
			expectedOwns: false,
		},
		{
			desc: "true should be returned when both service name tag and cluster name match",
			pip: &network.PublicIPAddress{
				Tags: map[string]*string{
					consts.ServiceTagKey:  to.StringPtr("default/nginx"),
					consts.ClusterNameKey: to.StringPtr("kubernetes"),
				},
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					IPAddress: to.StringPtr("1.2.3.4"),
				},
			},
			clusterName:  "kubernetes",
			serviceName:  "nginx",
			expectedOwns: true,
		},
		{
			desc: "false should be returned when the tag is empty",
			pip: &network.PublicIPAddress{
				Tags: map[string]*string{
					consts.ServiceTagKey:  to.StringPtr(""),
					consts.ClusterNameKey: to.StringPtr("kubernetes"),
				},
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					IPAddress: to.StringPtr("1.2.3.4"),
				},
			},
			clusterName:             "kubernetes",
			serviceName:             "nginx",
			expectedOwns:            false,
			expectedUserAssignedPIP: true,
		},
		{
			desc: "true should be returned if there is a match among a multi-service tag",
			pip: &network.PublicIPAddress{
				Tags: map[string]*string{
					consts.ServiceTagKey:  to.StringPtr("default/nginx1,default/nginx2"),
					consts.ClusterNameKey: to.StringPtr("kubernetes"),
				},
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					IPAddress: to.StringPtr("1.2.3.4"),
				},
			},
			clusterName:  "kubernetes",
			serviceName:  "nginx1",
			expectedOwns: true,
		},
		{
			desc: "false should be returned if there is not a match among a multi-service tag",
			pip: &network.PublicIPAddress{
				Tags: map[string]*string{
					consts.ServiceTagKey:  to.StringPtr("default/nginx1,default/nginx2"),
					consts.ClusterNameKey: to.StringPtr("kubernetes"),
				},
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					IPAddress: to.StringPtr("1.2.3.4"),
				},
			},
			clusterName:  "kubernetes",
			serviceName:  "nginx3",
			expectedOwns: false,
		},
		{
			desc: "true should be returned if the load balancer IP is matched even if the svc name is not included in the tag",
			pip: &network.PublicIPAddress{
				Tags: map[string]*string{
					consts.ServiceTagKey:  to.StringPtr(""),
					consts.ClusterNameKey: to.StringPtr("kubernetes"),
				},
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					IPAddress: to.StringPtr("1.2.3.4"),
				},
			},
			clusterName:             "kubernetes",
			serviceName:             "nginx3",
			serviceLBIP:             "1.2.3.4",
			expectedOwns:            true,
			expectedUserAssignedPIP: true,
		},
		{
			desc: "true should be returned if the load balancer IP is not matched but the svc name is included in the tag",
			pip: &network.PublicIPAddress{
				Tags: map[string]*string{
					consts.ServiceTagKey:  to.StringPtr("default/nginx1,default/nginx2"),
					consts.ClusterNameKey: to.StringPtr("kubernetes"),
				},
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					IPAddress: to.StringPtr("1.2.3.4"),
				},
			},
			clusterName:  "kubernetes",
			serviceName:  "nginx1",
			serviceLBIP:  "1.1.1.1",
			expectedOwns: true,
		},
	}

	for i, c := range tests {
		t.Run(c.desc, func(t *testing.T) {
			service := getTestService(c.serviceName, v1.ProtocolTCP, nil, false, 80)
			if c.serviceLBIP != "" {
				service.Spec.LoadBalancerIP = c.serviceLBIP
			}
			owns, isUserAssignedPIP := serviceOwnsPublicIP(&service, c.pip, c.clusterName)
			assert.Equal(t, c.expectedOwns, owns, "TestCase[%d]: %s", i, c.desc)
			assert.Equal(t, c.expectedUserAssignedPIP, isUserAssignedPIP, "TestCase[%d]: %s", i, c.desc)
		})
	}
}

func TestGetPublicIPAddressResourceGroup(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	az := GetTestCloud(ctrl)

	for i, c := range []struct {
		desc        string
		annotations map[string]string
		expected    string
	}{
		{
			desc:     "no annotation",
			expected: "rg",
		},
		{
			desc:        "annotation with empty string resource group",
			annotations: map[string]string{consts.ServiceAnnotationLoadBalancerResourceGroup: ""},
			expected:    "rg",
		},
		{
			desc:        "annotation with non-empty resource group ",
			annotations: map[string]string{consts.ServiceAnnotationLoadBalancerResourceGroup: "rg2"},
			expected:    "rg2",
		},
	} {
		t.Run(c.desc, func(t *testing.T) {
			s := &v1.Service{}
			s.Annotations = c.annotations
			real := az.getPublicIPAddressResourceGroup(s)
			assert.Equal(t, c.expected, real, "TestCase[%d]: %s", i, c.desc)
		})
	}
}

func TestShouldReleaseExistingOwnedPublicIP(t *testing.T) {
	existingPipWithTag := network.PublicIPAddress{
		ID:   to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/publicIPAddresses/testPIP"),
		Name: to.StringPtr("testPIP"),
		PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
			PublicIPAddressVersion:   network.IPVersionIPv4,
			PublicIPAllocationMethod: network.IPAllocationMethodStatic,
			IPTags: &[]network.IPTag{
				{
					IPTagType: to.StringPtr("tag1"),
					Tag:       to.StringPtr("tag1value"),
				},
			},
		},
	}

	existingPipWithNoPublicIPAddressFormatProperties := network.PublicIPAddress{
		ID:                              to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/publicIPAddresses/testPIP"),
		Name:                            to.StringPtr("testPIP"),
		Tags:                            map[string]*string{"service": to.StringPtr("default/test2")},
		PublicIPAddressPropertiesFormat: nil,
	}

	tests := []struct {
		desc                  string
		desiredPipName        string
		existingPip           network.PublicIPAddress
		ipTagRequest          serviceIPTagRequest
		tags                  map[string]*string
		lbShouldExist         bool
		lbIsInternal          bool
		isUserAssignedPIP     bool
		expectedShouldRelease bool
	}{
		{
			desc:           "Everything matches, no release",
			existingPip:    existingPipWithTag,
			lbShouldExist:  true,
			lbIsInternal:   false,
			desiredPipName: *existingPipWithTag.Name,
			ipTagRequest: serviceIPTagRequest{
				IPTagsRequestedByAnnotation: true,
				IPTags:                      existingPipWithTag.PublicIPAddressPropertiesFormat.IPTags,
			},
			expectedShouldRelease: false,
		},
		{
			desc:           "nil tags (none-specified by annotation, some are present on object), no release",
			existingPip:    existingPipWithTag,
			lbShouldExist:  true,
			lbIsInternal:   false,
			desiredPipName: *existingPipWithTag.Name,
			ipTagRequest: serviceIPTagRequest{
				IPTagsRequestedByAnnotation: false,
				IPTags:                      nil,
			},
			expectedShouldRelease: false,
		},
		{
			desc:           "existing public ip with no format properties (unit test only?), tags required by annotation, no release",
			existingPip:    existingPipWithNoPublicIPAddressFormatProperties,
			lbShouldExist:  true,
			lbIsInternal:   false,
			desiredPipName: *existingPipWithTag.Name,
			ipTagRequest: serviceIPTagRequest{
				IPTagsRequestedByAnnotation: true,
				IPTags:                      existingPipWithTag.PublicIPAddressPropertiesFormat.IPTags,
			},
			expectedShouldRelease: true,
		},
		{
			desc:           "LB no longer desired, expect release",
			existingPip:    existingPipWithTag,
			lbShouldExist:  false,
			lbIsInternal:   false,
			desiredPipName: *existingPipWithTag.Name,
			ipTagRequest: serviceIPTagRequest{
				IPTagsRequestedByAnnotation: true,
				IPTags:                      existingPipWithTag.PublicIPAddressPropertiesFormat.IPTags,
			},
			expectedShouldRelease: true,
		},
		{
			desc:           "LB now internal, expect release",
			existingPip:    existingPipWithTag,
			lbShouldExist:  true,
			lbIsInternal:   true,
			desiredPipName: *existingPipWithTag.Name,
			ipTagRequest: serviceIPTagRequest{
				IPTagsRequestedByAnnotation: true,
				IPTags:                      existingPipWithTag.PublicIPAddressPropertiesFormat.IPTags,
			},
			expectedShouldRelease: true,
		},
		{
			desc:           "Alternate desired name, expect release",
			existingPip:    existingPipWithTag,
			lbShouldExist:  true,
			lbIsInternal:   false,
			desiredPipName: "otherName",
			ipTagRequest: serviceIPTagRequest{
				IPTagsRequestedByAnnotation: true,
				IPTags:                      existingPipWithTag.PublicIPAddressPropertiesFormat.IPTags,
			},
			expectedShouldRelease: true,
		},
		{
			desc:           "mismatching, expect release",
			existingPip:    existingPipWithTag,
			lbShouldExist:  true,
			lbIsInternal:   false,
			desiredPipName: *existingPipWithTag.Name,
			ipTagRequest: serviceIPTagRequest{
				IPTagsRequestedByAnnotation: true,
				IPTags: &[]network.IPTag{
					{
						IPTagType: to.StringPtr("tag2"),
						Tag:       to.StringPtr("tag2value"),
					},
				},
			},
			expectedShouldRelease: true,
		},
		{
			desc:           "should delete orphaned managed public IP",
			existingPip:    existingPipWithTag,
			lbShouldExist:  false,
			lbIsInternal:   false,
			desiredPipName: *existingPipWithTag.Name,
			tags:           map[string]*string{consts.ServiceTagKey: to.StringPtr("")},
			ipTagRequest: serviceIPTagRequest{
				IPTagsRequestedByAnnotation: true,
				IPTags:                      existingPipWithTag.PublicIPAddressPropertiesFormat.IPTags,
			},
			expectedShouldRelease: true,
		},
		{
			desc:           "should not delete managed public IP which has references",
			existingPip:    existingPipWithTag,
			lbShouldExist:  false,
			lbIsInternal:   false,
			desiredPipName: *existingPipWithTag.Name,
			tags:           map[string]*string{consts.ServiceTagKey: to.StringPtr("svc1")},
			ipTagRequest: serviceIPTagRequest{
				IPTagsRequestedByAnnotation: true,
				IPTags:                      existingPipWithTag.PublicIPAddressPropertiesFormat.IPTags,
			},
		},
		{
			desc:           "should not delete orphaned unmanaged public IP",
			existingPip:    existingPipWithTag,
			lbShouldExist:  false,
			lbIsInternal:   false,
			desiredPipName: *existingPipWithTag.Name,
			tags:           map[string]*string{consts.ServiceTagKey: to.StringPtr("")},
			ipTagRequest: serviceIPTagRequest{
				IPTagsRequestedByAnnotation: true,
				IPTags:                      existingPipWithTag.PublicIPAddressPropertiesFormat.IPTags,
			},
			isUserAssignedPIP: true,
		},
	}

	for i, c := range tests {
		if c.tags != nil {
			c.existingPip.Tags = c.tags
		}
		actualShouldRelease := shouldReleaseExistingOwnedPublicIP(&c.existingPip, c.lbShouldExist, c.lbIsInternal, c.isUserAssignedPIP, c.desiredPipName, c.ipTagRequest)
		assert.Equal(t, c.expectedShouldRelease, actualShouldRelease, "TestCase[%d]: %s", i, c.desc)
	}
}

func TestGetIPTagMap(t *testing.T) {
	tests := []struct {
		desc     string
		input    string
		expected map[string]string
	}{
		{
			desc:     "empty map should be returned when service has blank annotations",
			input:    "",
			expected: map[string]string{},
		},
		{
			desc:  "a single tag should be returned when service has set one tag pair in the annotation",
			input: "tag1=tagvalue1",
			expected: map[string]string{
				"tag1": "tagvalue1",
			},
		},
		{
			desc:  "a single tag should be returned when service has set one tag pair in the annotation (and spaces are trimmed)",
			input: " tag1 = tagvalue1 ",
			expected: map[string]string{
				"tag1": "tagvalue1",
			},
		},
		{
			desc:  "a single tag should be returned when service has set two tag pairs in the annotation with the same key (last write wins - according to appearance order in the string)",
			input: "tag1=tagvalue1,tag1=tagvalue1new",
			expected: map[string]string{
				"tag1": "tagvalue1new",
			},
		},
		{
			desc:  "two tags should be returned when service has set two tag pairs in the annotation",
			input: "tag1=tagvalue1,tag2=tagvalue2",
			expected: map[string]string{
				"tag1": "tagvalue1",
				"tag2": "tagvalue2",
			},
		},
		{
			desc:  "two tags should be returned when service has set two tag pairs (and one malformation) in the annotation",
			input: "tag1=tagvalue1,tag2=tagvalue2,tag3malformed",
			expected: map[string]string{
				"tag1": "tagvalue1",
				"tag2": "tagvalue2",
			},
		},
		{
			// We may later decide not to support blank values.  The Azure contract is not entirely clear here.
			desc:  "two tags should be returned when service has set two tag pairs (and one has a blank value) in the annotation",
			input: "tag1=tagvalue1,tag2=",
			expected: map[string]string{
				"tag1": "tagvalue1",
				"tag2": "",
			},
		},
		{
			// We may later decide not to support blank keys.  The Azure contract is not entirely clear here.
			desc:  "two tags should be returned when service has set two tag pairs (and one has a blank key) in the annotation",
			input: "tag1=tagvalue1,=tag2value",
			expected: map[string]string{
				"tag1": "tagvalue1",
				"":     "tag2value",
			},
		},
	}

	for i, c := range tests {
		actual := getIPTagMap(c.input)
		assert.Equal(t, c.expected, actual, "TestCase[%d]: %s", i, c.desc)
	}
}

func TestConvertIPTagMapToSlice(t *testing.T) {
	tests := []struct {
		desc     string
		input    map[string]string
		expected *[]network.IPTag
	}{
		{
			desc:     "nil slice should be returned when the map is nil",
			input:    nil,
			expected: nil,
		},
		{
			desc:     "empty slice should be returned when the map is empty",
			input:    map[string]string{},
			expected: &[]network.IPTag{},
		},
		{
			desc: "one tag should be returned when the map has one tag",
			input: map[string]string{
				"tag1": "tag1value",
			},
			expected: &[]network.IPTag{
				{
					IPTagType: to.StringPtr("tag1"),
					Tag:       to.StringPtr("tag1value"),
				},
			},
		},
		{
			desc: "two tags should be returned when the map has two tags",
			input: map[string]string{
				"tag1": "tag1value",
				"tag2": "tag2value",
			},
			expected: &[]network.IPTag{
				{
					IPTagType: to.StringPtr("tag1"),
					Tag:       to.StringPtr("tag1value"),
				},
				{
					IPTagType: to.StringPtr("tag2"),
					Tag:       to.StringPtr("tag2value"),
				},
			},
		},
	}

	for i, c := range tests {
		actual := convertIPTagMapToSlice(c.input)

		// Sort output to provide stability of return from map for test comparison
		// The order doesn't matter at runtime.
		if actual != nil {
			sort.Slice(*actual, func(i, j int) bool {
				ipTagSlice := *actual
				return to.String(ipTagSlice[i].IPTagType) < to.String(ipTagSlice[j].IPTagType)
			})
		}
		if c.expected != nil {
			sort.Slice(*c.expected, func(i, j int) bool {
				ipTagSlice := *c.expected
				return to.String(ipTagSlice[i].IPTagType) < to.String(ipTagSlice[j].IPTagType)
			})

		}

		assert.Equal(t, c.expected, actual, "TestCase[%d]: %s", i, c.desc)
	}
}

func TestGetserviceIPTagRequestForPublicIP(t *testing.T) {
	tests := []struct {
		desc     string
		input    *v1.Service
		expected serviceIPTagRequest
	}{
		{
			desc:  "Annotation should be false when service is absent",
			input: nil,
			expected: serviceIPTagRequest{
				IPTagsRequestedByAnnotation: false,
				IPTags:                      nil,
			},
		},
		{
			desc: "Annotation should be false when service is present, without annotation",
			input: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			expected: serviceIPTagRequest{
				IPTagsRequestedByAnnotation: false,
				IPTags:                      nil,
			},
		},
		{
			desc: "Annotation should be true, tags slice empty, when annotation blank",
			input: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						consts.ServiceAnnotationIPTagsForPublicIP: "",
					},
				},
			},
			expected: serviceIPTagRequest{
				IPTagsRequestedByAnnotation: true,
				IPTags:                      &[]network.IPTag{},
			},
		},
		{
			desc: "two tags should be returned when service has set two tag pairs (and one malformation) in the annotation",
			input: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						consts.ServiceAnnotationIPTagsForPublicIP: "tag1=tag1value,tag2=tag2value,tag3malformed",
					},
				},
			},
			expected: serviceIPTagRequest{
				IPTagsRequestedByAnnotation: true,
				IPTags: &[]network.IPTag{
					{
						IPTagType: to.StringPtr("tag1"),
						Tag:       to.StringPtr("tag1value"),
					},
					{
						IPTagType: to.StringPtr("tag2"),
						Tag:       to.StringPtr("tag2value"),
					},
				},
			},
		},
	}
	for i, c := range tests {
		actual := getServiceIPTagRequestForPublicIP(c.input)

		// Sort output to provide stability of return from map for test comparison
		// The order doesn't matter at runtime.
		if actual.IPTags != nil {
			sort.Slice(*actual.IPTags, func(i, j int) bool {
				ipTagSlice := *actual.IPTags
				return to.String(ipTagSlice[i].IPTagType) < to.String(ipTagSlice[j].IPTagType)
			})
		}
		if c.expected.IPTags != nil {
			sort.Slice(*c.expected.IPTags, func(i, j int) bool {
				ipTagSlice := *c.expected.IPTags
				return to.String(ipTagSlice[i].IPTagType) < to.String(ipTagSlice[j].IPTagType)
			})

		}

		assert.Equal(t, actual, c.expected, "TestCase[%d]: %s", i, c.desc)
	}
}

func TestAreIpTagsEquivalent(t *testing.T) {
	tests := []struct {
		desc     string
		input1   *[]network.IPTag
		input2   *[]network.IPTag
		expected bool
	}{
		{
			desc:     "nils should be considered equal",
			input1:   nil,
			input2:   nil,
			expected: true,
		},
		{
			desc:     "nils should be considered to empty arrays (case 1)",
			input1:   nil,
			input2:   &[]network.IPTag{},
			expected: true,
		},
		{
			desc:     "nils should be considered to empty arrays (case 1)",
			input1:   &[]network.IPTag{},
			input2:   nil,
			expected: true,
		},
		{
			desc: "nil should not be considered equal to anything (case 1)",
			input1: &[]network.IPTag{
				{
					IPTagType: to.StringPtr("tag1"),
					Tag:       to.StringPtr("tag1value"),
				},
				{
					IPTagType: to.StringPtr("tag2"),
					Tag:       to.StringPtr("tag2value"),
				},
			},
			input2:   nil,
			expected: false,
		},
		{
			desc: "nil should not be considered equal to anything (case 2)",
			input2: &[]network.IPTag{
				{
					IPTagType: to.StringPtr("tag1"),
					Tag:       to.StringPtr("tag1value"),
				},
				{
					IPTagType: to.StringPtr("tag2"),
					Tag:       to.StringPtr("tag2value"),
				},
			},
			input1:   nil,
			expected: false,
		},
		{
			desc: "exactly equal should be treated as equal",
			input1: &[]network.IPTag{
				{
					IPTagType: to.StringPtr("tag1"),
					Tag:       to.StringPtr("tag1value"),
				},
				{
					IPTagType: to.StringPtr("tag2"),
					Tag:       to.StringPtr("tag2value"),
				},
			},
			input2: &[]network.IPTag{
				{
					IPTagType: to.StringPtr("tag1"),
					Tag:       to.StringPtr("tag1value"),
				},
				{
					IPTagType: to.StringPtr("tag2"),
					Tag:       to.StringPtr("tag2value"),
				},
			},
			expected: true,
		},
		{
			desc: "equal but out of order should be treated as equal",
			input1: &[]network.IPTag{
				{
					IPTagType: to.StringPtr("tag1"),
					Tag:       to.StringPtr("tag1value"),
				},
				{
					IPTagType: to.StringPtr("tag2"),
					Tag:       to.StringPtr("tag2value"),
				},
			},
			input2: &[]network.IPTag{
				{
					IPTagType: to.StringPtr("tag2"),
					Tag:       to.StringPtr("tag2value"),
				},
				{
					IPTagType: to.StringPtr("tag1"),
					Tag:       to.StringPtr("tag1value"),
				},
			},
			expected: true,
		},
	}
	for i, c := range tests {
		actual := areIPTagsEquivalent(c.input1, c.input2)
		assert.Equal(t, actual, c.expected, "TestCase[%d]: %s", i, c.desc)
	}
}

func TestGetServiceTags(t *testing.T) {
	tests := []struct {
		desc     string
		service  *v1.Service
		expected []string
	}{
		{
			desc:     "nil should be returned when service is nil",
			service:  nil,
			expected: nil,
		},
		{
			desc:     "nil should be returned when service has no annotations",
			service:  &v1.Service{},
			expected: nil,
		},
		{
			desc: "single tag should be returned when service has set one annotations",
			service: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						consts.ServiceAnnotationAllowedServiceTag: "tag1",
					},
				},
			},
			expected: []string{"tag1"},
		},
		{
			desc: "multiple tags should be returned when service has set multi-annotations",
			service: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						consts.ServiceAnnotationAllowedServiceTag: "tag1, tag2",
					},
				},
			},
			expected: []string{"tag1", "tag2"},
		},
		{
			desc: "correct tags should be returned when comma or spaces are included in the annotations",
			service: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						consts.ServiceAnnotationAllowedServiceTag: ", tag1, ",
					},
				},
			},
			expected: []string{"tag1"},
		},
	}

	for i, c := range tests {
		tags := getServiceTags(c.service)
		assert.Equal(t, tags, c.expected, "TestCase[%d]: %s", i, c.desc)
	}
}

func TestGetServiceLoadBalancer(t *testing.T) {
	testCases := []struct {
		desc           string
		sku            string
		existingLBs    []network.LoadBalancer
		service        v1.Service
		annotations    map[string]string
		expectedLB     *network.LoadBalancer
		expectedStatus *v1.LoadBalancerStatus
		wantLB         bool
		expectedExists bool
		expectedError  bool
	}{
		{
			desc: "getServiceLoadBalancer shall return corresponding lb, status, exists if there are existed lbs",
			existingLBs: []network.LoadBalancer{
				{
					Name: to.StringPtr("testCluster"),
					LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
						FrontendIPConfigurations: &[]network.FrontendIPConfiguration{
							{
								Name: to.StringPtr("aservice1"),
								FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
									PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-aservice1")},
								},
							},
						},
					},
				},
			},
			service: getTestService("service1", v1.ProtocolTCP, nil, false, 80),
			wantLB:  false,
			expectedLB: &network.LoadBalancer{
				Name: to.StringPtr("testCluster"),
				LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
					FrontendIPConfigurations: &[]network.FrontendIPConfiguration{
						{
							Name: to.StringPtr("aservice1"),
							FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
								PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-aservice1")},
							},
						},
					},
				},
			},
			expectedStatus: &v1.LoadBalancerStatus{Ingress: []v1.LoadBalancerIngress{{IP: "1.2.3.4", Hostname: ""}}},
			expectedExists: true,
			expectedError:  false,
		},
		{
			desc: "getServiceLoadBalancer shall select the lb with minimum lb rules if wantLb is true, the sku is " +
				"not standard and there are existing lbs already",
			existingLBs: []network.LoadBalancer{
				{
					Name: to.StringPtr("testCluster"),
					LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
						LoadBalancingRules: &[]network.LoadBalancingRule{
							{Name: to.StringPtr("rule1")},
						},
					},
				},
				{
					Name: to.StringPtr("as-1"),
					LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
						LoadBalancingRules: &[]network.LoadBalancingRule{
							{Name: to.StringPtr("rule1")},
							{Name: to.StringPtr("rule2")},
						},
					},
				},
				{
					Name: to.StringPtr("as-2"),
					LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
						LoadBalancingRules: &[]network.LoadBalancingRule{
							{Name: to.StringPtr("rule1")},
							{Name: to.StringPtr("rule2")},
							{Name: to.StringPtr("rule3")},
						},
					},
				},
			},
			service:     getTestService("service1", v1.ProtocolTCP, nil, false, 80),
			annotations: map[string]string{consts.ServiceAnnotationLoadBalancerMode: "__auto__"},
			wantLB:      true,
			expectedLB: &network.LoadBalancer{
				Name: to.StringPtr("testCluster"),
				LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
					LoadBalancingRules: &[]network.LoadBalancingRule{
						{Name: to.StringPtr("rule1")},
					},
				},
			},
			expectedExists: false,
			expectedError:  false,
		},
		{
			desc:    "getServiceLoadBalancer shall create a new lb otherwise",
			service: getTestService("service1", v1.ProtocolTCP, nil, false, 80),
			expectedLB: &network.LoadBalancer{
				Name:                         to.StringPtr("testCluster"),
				Location:                     to.StringPtr("westus"),
				LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{},
			},
			expectedExists: false,
			expectedError:  false,
		},
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	for i, test := range testCases {
		az := GetTestCloud(ctrl)
		clusterResources, expectedInterfaces, expectedVirtualMachines := getClusterResources(az, 3, 3)
		setMockEnv(az, ctrl, expectedInterfaces, expectedVirtualMachines, 1)

		mockLBsClient := mockloadbalancerclient.NewMockInterface(ctrl)
		mockLBsClient.EXPECT().CreateOrUpdate(gomock.Any(), "rg", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		mockLBsClient.EXPECT().List(gomock.Any(), "rg").Return(test.existingLBs, nil)
		az.LoadBalancerClient = mockLBsClient

		for _, existingLB := range test.existingLBs {
			err := az.LoadBalancerClient.CreateOrUpdate(context.TODO(), "rg", *existingLB.Name, existingLB, "")
			if err != nil {
				t.Fatalf("TestCase[%d] meets unexpected error: %v", i, err)
			}
		}
		test.service.Annotations = test.annotations
		az.LoadBalancerSku = test.sku
		lb, status, exists, err := az.getServiceLoadBalancer(&test.service, testClusterName,
			clusterResources.nodes, test.wantLB, []network.LoadBalancer{})
		assert.Equal(t, test.expectedLB, lb, "TestCase[%d]: %s", i, test.desc)
		assert.Equal(t, test.expectedStatus, status, "TestCase[%d]: %s", i, test.desc)
		assert.Equal(t, test.expectedExists, exists, "TestCase[%d]: %s", i, test.desc)
		assert.Equal(t, test.expectedError, err != nil, "TestCase[%d]: %s", i, test.desc)
	}
}

func TestGetServiceLoadBalancerWithExtendedLocation(t *testing.T) {
	service := getTestService("service1", v1.ProtocolTCP, nil, false, 80)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	az := GetTestCloudWithExtendedLocation(ctrl)
	clusterResources, expectedInterfaces, expectedVirtualMachines := getClusterResources(az, 3, 3)
	setMockEnv(az, ctrl, expectedInterfaces, expectedVirtualMachines, 1)

	// Test with wantLB=false
	expectedLB := &network.LoadBalancer{
		Name:     to.StringPtr("testCluster"),
		Location: to.StringPtr("westus"),
		ExtendedLocation: &network.ExtendedLocation{
			Name: to.StringPtr("microsoftlosangeles1"),
			Type: network.ExtendedLocationTypesEdgeZone,
		},
		LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{},
	}
	mockLBsClient := mockloadbalancerclient.NewMockInterface(ctrl)
	mockLBsClient.EXPECT().List(gomock.Any(), "rg").Return(nil, nil)
	az.LoadBalancerClient = mockLBsClient

	lb, status, exists, err := az.getServiceLoadBalancer(&service, testClusterName,
		clusterResources.nodes, false, []network.LoadBalancer{})
	assert.Equal(t, expectedLB, lb, "GetServiceLoadBalancer shall return a default LB with expected location.")
	assert.Nil(t, status, "GetServiceLoadBalancer: Status should be nil for default LB.")
	assert.Equal(t, false, exists, "GetServiceLoadBalancer: Default LB should not exist.")
	assert.NoError(t, err, "GetServiceLoadBalancer: No error should be thrown when returning default LB.")

	// Test with wantLB=true
	expectedLB = &network.LoadBalancer{
		Name:     to.StringPtr("testCluster"),
		Location: to.StringPtr("westus"),
		ExtendedLocation: &network.ExtendedLocation{
			Name: to.StringPtr("microsoftlosangeles1"),
			Type: network.ExtendedLocationTypesEdgeZone,
		},
		LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{},
		Sku: &network.LoadBalancerSku{
			Name: network.LoadBalancerSkuName("Basic"),
			Tier: network.LoadBalancerSkuTier(""),
		},
	}
	mockLBsClient = mockloadbalancerclient.NewMockInterface(ctrl)
	mockLBsClient.EXPECT().List(gomock.Any(), "rg").Return(nil, nil)
	az.LoadBalancerClient = mockLBsClient

	lb, status, exists, err = az.getServiceLoadBalancer(&service, testClusterName,
		clusterResources.nodes, true, []network.LoadBalancer{})
	assert.Equal(t, expectedLB, lb, "GetServiceLoadBalancer shall return a new LB with expected location.")
	assert.Nil(t, status, "GetServiceLoadBalancer: Status should be nil for new LB.")
	assert.Equal(t, false, exists, "GetServiceLoadBalancer: LB should not exist before hand.")
	assert.NoError(t, err, "GetServiceLoadBalancer: No error should be thrown when returning new LB.")
}

func TestIsFrontendIPChanged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testCases := []struct {
		desc                   string
		config                 network.FrontendIPConfiguration
		service                v1.Service
		lbFrontendIPConfigName string
		annotations            string
		loadBalancerIP         string
		existingSubnet         network.Subnet
		existingPIPs           []network.PublicIPAddress
		expectedFlag           bool
		expectedError          bool
	}{
		{
			desc: "isFrontendIPChanged shall return true if config.Name has a prefix of lb's name and " +
				"config.Name != lbFrontendIPConfigName",
			config:                 network.FrontendIPConfiguration{Name: to.StringPtr("atest1-name")},
			service:                getInternalTestService("test1", 80),
			lbFrontendIPConfigName: "configName",
			expectedFlag:           true,
			expectedError:          false,
		},
		{
			desc: "isFrontendIPChanged shall return false if config.Name doesn't have a prefix of lb's name " +
				"and config.Name != lbFrontendIPConfigName",
			config:                 network.FrontendIPConfiguration{Name: to.StringPtr("btest1-name")},
			service:                getInternalTestService("test1", 80),
			lbFrontendIPConfigName: "configName",
			expectedFlag:           false,
			expectedError:          false,
		},
		{
			desc: "isFrontendIPChanged shall return false if the service is internal, no loadBalancerIP is given, " +
				"subnetName == nil and config.PrivateIPAllocationMethod == network.Static",
			config: network.FrontendIPConfiguration{
				Name: to.StringPtr("atest1-name"),
				FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
					PrivateIPAllocationMethod: network.IPAllocationMethod("static"),
				},
			},
			service:       getInternalTestService("test1", 80),
			expectedFlag:  true,
			expectedError: false,
		},
		{
			desc: "isFrontendIPChanged shall return false if the service is internal, no loadBalancerIP is given, " +
				"subnetName == nil and config.PrivateIPAllocationMethod != network.Static",
			config: network.FrontendIPConfiguration{
				Name: to.StringPtr("btest1-name"),
				FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
					PrivateIPAllocationMethod: network.IPAllocationMethod("dynamic"),
				},
			},
			lbFrontendIPConfigName: "btest1-name",
			service:                getInternalTestService("test1", 80),
			expectedFlag:           false,
			expectedError:          false,
		},
		{
			desc: "isFrontendIPChanged shall return true if the service is internal and " +
				"config.Subnet.Name == subnet.Name",
			config: network.FrontendIPConfiguration{
				Name: to.StringPtr("btest1-name"),
				FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
					Subnet: &network.Subnet{Name: to.StringPtr("testSubnet")},
				},
			},
			lbFrontendIPConfigName: "btest1-name",
			service:                getInternalTestService("test1", 80),
			annotations:            "testSubnet",
			existingSubnet:         network.Subnet{Name: to.StringPtr("testSubnet1")},
			expectedFlag:           true,
			expectedError:          false,
		},
		{
			desc: "isFrontendIPChanged shall return true if the service is internal, subnet == nil, " +
				"loadBalancerIP != '' and config.PrivateIPAllocationMethod != 'static'",
			config: network.FrontendIPConfiguration{
				Name: to.StringPtr("btest1-name"),
				FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
					PrivateIPAllocationMethod: network.IPAllocationMethod("dynamic"),
				},
			},
			lbFrontendIPConfigName: "btest1-name",
			service:                getInternalTestService("test1", 80),
			loadBalancerIP:         "1.1.1.1",
			expectedFlag:           true,
			expectedError:          false,
		},
		{
			desc: "isFrontendIPChanged shall return true if the service is internal, subnet == nil and " +
				"loadBalancerIP != config.PrivateIPAddress",
			config: network.FrontendIPConfiguration{
				Name: to.StringPtr("btest1-name"),
				FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
					PrivateIPAllocationMethod: network.IPAllocationMethod("static"),
					PrivateIPAddress:          to.StringPtr("1.1.1.2"),
				},
			},
			lbFrontendIPConfigName: "btest1-name",
			service:                getInternalTestService("test1", 80),
			loadBalancerIP:         "1.1.1.1",
			expectedFlag:           true,
			expectedError:          false,
		},
		{
			desc: "isFrontendIPChanged shall return false if config.PublicIPAddress == nil",
			config: network.FrontendIPConfiguration{
				Name: to.StringPtr("btest1-name"),
				FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
					PublicIPAddress: &network.PublicIPAddress{
						ID: to.StringPtr("pip"),
					},
				},
			},
			lbFrontendIPConfigName: "btest1-name",
			service:                getTestService("test1", v1.ProtocolTCP, nil, false, 80),
			loadBalancerIP:         "1.1.1.1",
			existingPIPs: []network.PublicIPAddress{
				{
					Name: to.StringPtr("pipName"),
					ID:   to.StringPtr("pip"),
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						IPAddress: to.StringPtr("1.1.1.1"),
					},
				},
			},
			expectedFlag:  false,
			expectedError: false,
		},
		{
			desc: "isFrontendIPChanged shall return false if pip.ID == config.PublicIPAddress.ID",
			config: network.FrontendIPConfiguration{
				Name: to.StringPtr("btest1-name"),
				FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
					PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("/subscriptions/subscription" +
						"/resourceGroups/rg/providers/Microsoft.Network/publicIPAddresses/pipName")},
				},
			},
			lbFrontendIPConfigName: "btest1-name",
			service:                getTestService("test1", v1.ProtocolTCP, nil, false, 80),
			loadBalancerIP:         "1.1.1.1",
			existingPIPs: []network.PublicIPAddress{
				{
					Name: to.StringPtr("pipName"),
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						IPAddress: to.StringPtr("1.1.1.1"),
					},
					ID: to.StringPtr("/subscriptions/subscription" +
						"/resourceGroups/rg/providers/Microsoft.Network/publicIPAddresses/pipName"),
				},
			},
			expectedFlag:  false,
			expectedError: false,
		},
		{
			desc: "isFrontendIPChanged shall return true if pip.ID != config.PublicIPAddress.ID",
			config: network.FrontendIPConfiguration{
				Name: to.StringPtr("btest1-name"),
				FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
					PublicIPAddress: &network.PublicIPAddress{
						ID: to.StringPtr("/subscriptions/subscription" +
							"/resourceGroups/rg/providers/Microsoft.Network/publicIPAddresses/pipName1"),
					},
				},
			},
			lbFrontendIPConfigName: "btest1-name",
			service:                getTestService("test1", v1.ProtocolTCP, nil, false, 80),
			loadBalancerIP:         "1.1.1.1",
			existingPIPs: []network.PublicIPAddress{
				{
					Name: to.StringPtr("pipName"),
					ID: to.StringPtr("/subscriptions/subscription" +
						"/resourceGroups/rg/providers/Microsoft.Network/publicIPAddresses/pipName2"),
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						IPAddress: to.StringPtr("1.1.1.1"),
					},
				},
			},
			expectedFlag:  true,
			expectedError: false,
		},
	}

	for i, test := range testCases {
		az := GetTestCloud(ctrl)
		mockSubnetsClient := az.SubnetsClient.(*mocksubnetclient.MockInterface)
		mockSubnetsClient.EXPECT().Get(gomock.Any(), "rg", "vnet", "testSubnet", "").Return(test.existingSubnet, nil).AnyTimes()
		mockSubnetsClient.EXPECT().CreateOrUpdate(gomock.Any(), "rg", "vnet", "testSubnet", test.existingSubnet).Return(nil)
		err := az.SubnetsClient.CreateOrUpdate(context.TODO(), "rg", "vnet", "testSubnet", test.existingSubnet)
		if err != nil {
			t.Fatalf("TestCase[%d] meets unexpected error: %v", i, err)
		}

		mockPIPsClient := az.PublicIPAddressesClient.(*mockpublicipclient.MockInterface)
		mockPIPsClient.EXPECT().List(gomock.Any(), "rg").Return(test.existingPIPs, nil).AnyTimes()
		mockPIPsClient.EXPECT().CreateOrUpdate(gomock.Any(), "rg", gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		for _, existingPIP := range test.existingPIPs {
			mockPIPsClient.EXPECT().Get(gomock.Any(), "rg", *existingPIP.Name, gomock.Any()).Return(existingPIP, nil).AnyTimes()
			err := az.PublicIPAddressesClient.CreateOrUpdate(context.TODO(), "rg", *existingPIP.Name, existingPIP)
			if err != nil {
				t.Fatalf("TestCase[%d] meets unexpected error: %v", i, err)
			}
		}
		test.service.Spec.LoadBalancerIP = test.loadBalancerIP
		test.service.Annotations[consts.ServiceAnnotationLoadBalancerInternalSubnet] = test.annotations
		flag, rerr := az.isFrontendIPChanged("testCluster", test.config,
			&test.service, test.lbFrontendIPConfigName)
		if rerr != nil {
			fmt.Println(rerr.Error())
		}
		assert.Equal(t, test.expectedFlag, flag, "TestCase[%d]: %s", i, test.desc)
		assert.Equal(t, test.expectedError, rerr != nil, "TestCase[%d]: %s", i, test.desc)
	}
}

func TestDeterminePublicIPName(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testCases := []struct {
		desc           string
		loadBalancerIP string
		existingPIPs   []network.PublicIPAddress
		expectedIP     string
		expectedError  bool
	}{
		{
			desc: "determinePublicIpName shall get public IP from az.getPublicIPName if no specific " +
				"loadBalancerIP is given",
			expectedIP:    "testCluster-atest1",
			expectedError: false,
		},
		{
			desc:           "determinePublicIpName shall report error if loadBalancerIP is not in the resource group",
			loadBalancerIP: "1.2.3.4",
			expectedIP:     "",
			expectedError:  true,
		},
		{
			desc: "determinePublicIpName shall return loadBalancerIP in service.Spec if it's in the " +
				"resource group",
			loadBalancerIP: "1.2.3.4",
			existingPIPs: []network.PublicIPAddress{
				{
					Name: to.StringPtr("pipName"),
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						IPAddress: to.StringPtr("1.2.3.4"),
					},
				},
			},
			expectedIP:    "pipName",
			expectedError: false,
		},
	}
	for i, test := range testCases {
		az := GetTestCloud(ctrl)
		service := getTestService("test1", v1.ProtocolTCP, nil, false, 80)
		service.Spec.LoadBalancerIP = test.loadBalancerIP

		mockPIPsClient := az.PublicIPAddressesClient.(*mockpublicipclient.MockInterface)
		mockPIPsClient.EXPECT().List(gomock.Any(), "rg").Return(test.existingPIPs, nil).AnyTimes()
		mockPIPsClient.EXPECT().CreateOrUpdate(gomock.Any(), "rg", gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		for _, existingPIP := range test.existingPIPs {
			mockPIPsClient.EXPECT().Get(gomock.Any(), "rg", *existingPIP.Name, gomock.Any()).Return(existingPIP, nil).AnyTimes()
			err := az.PublicIPAddressesClient.CreateOrUpdate(context.TODO(), "rg", *existingPIP.Name, existingPIP)
			if err != nil {
				t.Fatalf("TestCase[%d] meets unexpected error: %v", i, err)
			}
		}
		ip, _, err := az.determinePublicIPName("testCluster", &service)
		assert.Equal(t, test.expectedIP, ip, "TestCase[%d]: %s", i, test.desc)
		assert.Equal(t, test.expectedError, err != nil, "TestCase[%d]: %s", i, test.desc)
	}
}

func TestReconcileLoadBalancerRule(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testCases := []struct {
		desc            string
		service         v1.Service
		loadBalancerSku string
		wantLb          bool
		probeProtocol   string
		probePath       string
		expectedProbes  []network.Probe
		expectedRules   []network.LoadBalancingRule
		expectedErr     error
	}{
		{
			desc:    "getExpectedLBRules shall return nil if wantLb is false",
			service: getTestService("test1", v1.ProtocolTCP, nil, false, 80),
			wantLb:  false,
		},
		{
			desc:            "getExpectedLBRules shall return corresponding probe and lbRule(blb)",
			service:         getTestService("test1", v1.ProtocolTCP, map[string]string{"service.beta.kubernetes.io/azure-load-balancer-disable-tcp-reset": "true"}, false, 80),
			loadBalancerSku: "basic",
			wantLb:          true,
			expectedProbes:  getDefaultTestProbes("Tcp", ""),
			expectedRules:   getDefaultTestRules(false),
		},
		{
			desc:            "getExpectedLBRules shall return corresponding probe and lbRule (slb without tcp reset)",
			service:         getTestService("test1", v1.ProtocolTCP, map[string]string{"service.beta.kubernetes.io/azure-load-balancer-disable-tcp-reset": "True"}, false, 80),
			loadBalancerSku: "standard",
			wantLb:          true,
			expectedProbes:  getDefaultTestProbes("Tcp", ""),
			expectedRules:   getDefaultTestRules(true),
		},
		{
			desc:            "getExpectedLBRules shall return corresponding probe and lbRule(slb with tcp reset)",
			service:         getTestService("test1", v1.ProtocolTCP, nil, false, 80),
			loadBalancerSku: "standard",
			wantLb:          true,
			expectedProbes:  getDefaultTestProbes("Tcp", ""),
			expectedRules:   getDefaultTestRules(true),
		},
		{
			desc:            "getExpectedLBRules shall respect the probe protocol and path configuration in the config file",
			service:         getTestService("test1", v1.ProtocolTCP, nil, false, 80),
			loadBalancerSku: "standard",
			wantLb:          true,
			probeProtocol:   "http",
			probePath:       "/healthy",
			expectedProbes:  getDefaultTestProbes("http", "/healthy"),
			expectedRules:   getDefaultTestRules(true),
		},
		{
			desc: "getExpectedLBRules shall return corresponding probe and lbRule (slb with HA enabled)",
			service: getTestService("test1", v1.ProtocolTCP, map[string]string{
				"service.beta.kubernetes.io/azure-load-balancer-enable-high-availability-ports": "true",
				"service.beta.kubernetes.io/azure-load-balancer-internal":                       "true",
			}, false, 80),
			loadBalancerSku: "standard",
			wantLb:          true,
			expectedProbes:  getDefaultTestProbes("Tcp", ""),
			expectedRules:   getHATestRules(true, true, v1.ProtocolTCP),
		},
		{
			desc: "getExpectedLBRules shall return corresponding probe and lbRule (slb with SCTP)",
			service: getTestService("test1", v1.ProtocolSCTP, map[string]string{
				"service.beta.kubernetes.io/azure-load-balancer-enable-high-availability-ports": "true",
				"service.beta.kubernetes.io/azure-load-balancer-internal":                       "true",
			}, false, 80),
			loadBalancerSku: "standard",
			wantLb:          true,
			expectedRules:   getHATestRules(true, false, v1.ProtocolSCTP),
		},
		{
			desc: "getExpectedLBRules shall return corresponding probe and lbRule (slb with HA enabled multi-ports services)",
			service: getTestService("test1", v1.ProtocolTCP, map[string]string{
				"service.beta.kubernetes.io/azure-load-balancer-enable-high-availability-ports": "true",
				"service.beta.kubernetes.io/azure-load-balancer-internal":                       "true",
			}, false, 80, 8080),
			loadBalancerSku: "standard",
			wantLb:          true,
			expectedProbes:  getDefaultTestProbes("Tcp", ""),
			expectedRules:   getHATestRules(true, true, v1.ProtocolTCP),
		},
		{
			desc:            "getExpectedLBRules should leave probe path empty when using TCP probe",
			service:         getTestService("test1", v1.ProtocolTCP, nil, false, 80),
			loadBalancerSku: "standard",
			wantLb:          true,
			probeProtocol:   "Tcp",
			expectedProbes:  getDefaultTestProbes("Tcp", ""),
			expectedRules:   getDefaultTestRules(true),
		},
	}
	for i, test := range testCases {
		az := GetTestCloud(ctrl)
		az.Config.LoadBalancerSku = test.loadBalancerSku
		service := test.service
		if test.probeProtocol != "" {
			service.Annotations[consts.ServiceAnnotationLoadBalancerHealthProbeProtocol] = test.probeProtocol
		}
		if test.probePath != "" {
			service.Annotations[consts.ServiceAnnotationLoadBalancerHealthProbeRequestPath] = test.probePath
		}
		probe, lbrule, err := az.getExpectedLBRules(&test.service, test.wantLb,
			"frontendIPConfigID", "backendPoolID", "lbname", to.Int32Ptr(0))

		if test.expectedErr != nil {
			assert.Equal(t, test.expectedErr, err, "TestCase[%d]: %s", i, test.desc)
		} else {
			assert.Equal(t, test.expectedProbes, probe, "TestCase[%d]: %s", i, test.desc)
			assert.Equal(t, test.expectedRules, lbrule, "TestCase[%d]: %s", i, test.desc)
			assert.NoError(t, err)
		}
	}
}

func getDefaultTestProbes(protocol, path string) []network.Probe {
	expectedProbes := []network.Probe{
		{
			Name: to.StringPtr("atest1-TCP-80"),
			ProbePropertiesFormat: &network.ProbePropertiesFormat{
				Protocol:          network.ProbeProtocol(protocol),
				Port:              to.Int32Ptr(10080),
				IntervalInSeconds: to.Int32Ptr(5),
				NumberOfProbes:    to.Int32Ptr(2),
			},
		},
	}
	if path != "" {
		expectedProbes[0].RequestPath = to.StringPtr(path)
	}
	return expectedProbes
}

func getDefaultTestRules(enableTCPReset bool) []network.LoadBalancingRule {
	expectedRules := []network.LoadBalancingRule{
		{
			Name: to.StringPtr("atest1-TCP-80"),
			LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
				Protocol: network.TransportProtocol("Tcp"),
				FrontendIPConfiguration: &network.SubResource{
					ID: to.StringPtr("frontendIPConfigID"),
				},
				BackendAddressPool: &network.SubResource{
					ID: to.StringPtr("backendPoolID"),
				},
				LoadDistribution:     "Default",
				FrontendPort:         to.Int32Ptr(80),
				BackendPort:          to.Int32Ptr(80),
				EnableFloatingIP:     to.BoolPtr(true),
				DisableOutboundSnat:  to.BoolPtr(false),
				IdleTimeoutInMinutes: to.Int32Ptr(0),
				Probe: &network.SubResource{
					ID: to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/" +
						"Microsoft.Network/loadBalancers/lbname/probes/atest1-TCP-80"),
				},
			},
		},
	}
	if enableTCPReset {
		expectedRules[0].EnableTCPReset = to.BoolPtr(true)
	}
	return expectedRules
}

func getHATestRules(enableTCPReset, hasProbe bool, protocol v1.Protocol) []network.LoadBalancingRule {
	expectedRules := []network.LoadBalancingRule{
		{
			Name: to.StringPtr(fmt.Sprintf("atest1-%s-80", string(protocol))),
			LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
				Protocol: network.TransportProtocol("All"),
				FrontendIPConfiguration: &network.SubResource{
					ID: to.StringPtr("frontendIPConfigID"),
				},
				BackendAddressPool: &network.SubResource{
					ID: to.StringPtr("backendPoolID"),
				},
				LoadDistribution:    "Default",
				FrontendPort:        to.Int32Ptr(0),
				BackendPort:         to.Int32Ptr(0),
				EnableFloatingIP:    to.BoolPtr(true),
				DisableOutboundSnat: to.BoolPtr(false),
			},
		},
	}
	if protocol == v1.ProtocolTCP {
		expectedRules[0].IdleTimeoutInMinutes = to.Int32Ptr(0)
	}
	if enableTCPReset {
		expectedRules[0].EnableTCPReset = to.BoolPtr(true)
	}
	if hasProbe {
		expectedRules[0].Probe = &network.SubResource{
			ID: to.StringPtr(fmt.Sprintf("/subscriptions/subscription/resourceGroups/rg/providers/"+
				"Microsoft.Network/loadBalancers/lbname/probes/atest1-%s-80", string(protocol))),
		}
	}
	return expectedRules
}

func getTestLoadBalancer(name, rgName, clusterName, identifier *string, service v1.Service, lbSku string) network.LoadBalancer {
	lb := network.LoadBalancer{
		Name: name,
		Sku: &network.LoadBalancerSku{
			Name: network.LoadBalancerSkuName(lbSku),
		},
		LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
			FrontendIPConfigurations: &[]network.FrontendIPConfiguration{
				{
					Name: identifier,
					ID:   identifier,
					FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
						PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-aservice1")},
					},
				},
			},
			BackendAddressPools: &[]network.BackendAddressPool{
				{Name: clusterName},
			},
			Probes: &[]network.Probe{
				{
					Name: to.StringPtr(*identifier + "-" + string(service.Spec.Ports[0].Protocol) +
						"-" + strconv.Itoa(int(service.Spec.Ports[0].Port))),
					ProbePropertiesFormat: &network.ProbePropertiesFormat{
						Port: to.Int32Ptr(10080),
					},
				},
			},
			LoadBalancingRules: &[]network.LoadBalancingRule{
				{
					Name: to.StringPtr(*identifier + "-" + string(service.Spec.Ports[0].Protocol) +
						"-" + strconv.Itoa(int(service.Spec.Ports[0].Port))),
					LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
						Protocol: network.TransportProtocol(strings.Title(
							strings.ToLower(string(service.Spec.Ports[0].Protocol)))),
						FrontendIPConfiguration: &network.SubResource{
							ID: to.StringPtr("/subscriptions/subscription/resourceGroups/" + *rgName + "/providers/" +
								"Microsoft.Network/loadBalancers/" + *name + "/frontendIPConfigurations/aservice1"),
						},
						BackendAddressPool: &network.SubResource{
							ID: to.StringPtr("/subscriptions/subscription/resourceGroups/" + *rgName + "/providers/" +
								"Microsoft.Network/loadBalancers/" + *name + "/backendAddressPools/" + *clusterName),
						},
						LoadDistribution:    network.LoadDistribution("Default"),
						FrontendPort:        to.Int32Ptr(service.Spec.Ports[0].Port),
						BackendPort:         to.Int32Ptr(service.Spec.Ports[0].Port),
						EnableFloatingIP:    to.BoolPtr(true),
						EnableTCPReset:      to.BoolPtr(strings.EqualFold(lbSku, "standard")),
						DisableOutboundSnat: to.BoolPtr(false),
						Probe: &network.SubResource{
							ID: to.StringPtr("/subscriptions/subscription/resourceGroups/" + *rgName + "/providers/Microsoft.Network/loadBalancers/testCluster/probes/aservice1-TCP-80"),
						},
					},
				},
			},
		},
	}
	return lb
}

func TestReconcileLoadBalancer(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	service1 := getTestService("service1", v1.ProtocolTCP, nil, false, 80)
	basicLb1 := getTestLoadBalancer(to.StringPtr("testCluster"), to.StringPtr("rg"), to.StringPtr("testCluster"), to.StringPtr("aservice1"), service1, "Basic")

	service2 := getTestService("test1", v1.ProtocolTCP, nil, false, 80)
	basicLb2 := getTestLoadBalancer(to.StringPtr("lb1"), to.StringPtr("rg"), to.StringPtr("testCluster"), to.StringPtr("bservice1"), service2, "Basic")
	basicLb2.Name = to.StringPtr("testCluster")
	basicLb2.FrontendIPConfigurations = &[]network.FrontendIPConfiguration{
		{
			Name: to.StringPtr("bservice1"),
			ID:   to.StringPtr("bservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-bservice1")},
			},
		},
	}

	service3 := getTestService("service1", v1.ProtocolTCP, nil, false, 80)
	modifiedLb1 := getTestLoadBalancer(to.StringPtr("testCluster"), to.StringPtr("rg"), to.StringPtr("testCluster"), to.StringPtr("aservice1"), service3, "Basic")
	modifiedLb1.FrontendIPConfigurations = &[]network.FrontendIPConfiguration{
		{
			Name: to.StringPtr("aservice1"),
			ID:   to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/testCluster/frontendIPConfigurations/aservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-aservice1")},
			},
		},
		{
			Name: to.StringPtr("bservice1"),
			ID:   to.StringPtr("bservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-bservice1")},
			},
		},
	}
	modifiedLb1.Probes = &[]network.Probe{
		{
			Name: to.StringPtr("aservice1-" + string(service3.Spec.Ports[0].Protocol) +
				"-" + strconv.Itoa(int(service3.Spec.Ports[0].Port))),
			ProbePropertiesFormat: &network.ProbePropertiesFormat{
				Port: to.Int32Ptr(10080),
			},
		},
		{
			Name: to.StringPtr("aservice1-" + string(service3.Spec.Ports[0].Protocol) +
				"-" + strconv.Itoa(int(service3.Spec.Ports[0].Port))),
			ProbePropertiesFormat: &network.ProbePropertiesFormat{
				Port: to.Int32Ptr(10081),
			},
		},
	}
	expectedLb1 := getTestLoadBalancer(to.StringPtr("testCluster"), to.StringPtr("rg"), to.StringPtr("testCluster"), to.StringPtr("aservice1"), service3, "Basic")
	expectedLb1.FrontendIPConfigurations = &[]network.FrontendIPConfiguration{
		{
			Name: to.StringPtr("aservice1"),
			ID:   to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/testCluster/frontendIPConfigurations/aservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-aservice1")},
			},
		},
		{
			Name: to.StringPtr("bservice1"),
			ID:   to.StringPtr("bservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-bservice1")},
			},
		},
	}

	service4 := getTestService("service1", v1.ProtocolTCP, map[string]string{"service.beta.kubernetes.io/azure-load-balancer-disable-tcp-reset": "true"}, false, 80)
	existingSLB := getTestLoadBalancer(to.StringPtr("testCluster"), to.StringPtr("rg"), to.StringPtr("testCluster"), to.StringPtr("aservice1"), service4, "Standard")
	existingSLB.FrontendIPConfigurations = &[]network.FrontendIPConfiguration{
		{
			Name: to.StringPtr("aservice1"),
			ID:   to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/testCluster/frontendIPConfigurations/aservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-aservice1")},
			},
		},
		{
			Name: to.StringPtr("bservice1"),
			ID:   to.StringPtr("bservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-bservice1")},
			},
		},
	}
	existingSLB.Probes = &[]network.Probe{
		{
			Name: to.StringPtr("aservice1-" + string(service4.Spec.Ports[0].Protocol) +
				"-" + strconv.Itoa(int(service4.Spec.Ports[0].Port))),
			ProbePropertiesFormat: &network.ProbePropertiesFormat{
				Port: to.Int32Ptr(10080),
			},
		},
		{
			Name: to.StringPtr("aservice1-" + string(service4.Spec.Ports[0].Protocol) +
				"-" + strconv.Itoa(int(service4.Spec.Ports[0].Port))),
			ProbePropertiesFormat: &network.ProbePropertiesFormat{
				Port: to.Int32Ptr(10081),
			},
		},
	}

	expectedSLb := getTestLoadBalancer(to.StringPtr("testCluster"), to.StringPtr("rg"), to.StringPtr("testCluster"), to.StringPtr("aservice1"), service4, "Standard")
	(*expectedSLb.LoadBalancerPropertiesFormat.LoadBalancingRules)[0].DisableOutboundSnat = to.BoolPtr(true)
	(*expectedSLb.LoadBalancerPropertiesFormat.LoadBalancingRules)[0].EnableTCPReset = to.BoolPtr(true)
	expectedSLb.FrontendIPConfigurations = &[]network.FrontendIPConfiguration{
		{
			Name: to.StringPtr("aservice1"),
			ID:   to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/testCluster/frontendIPConfigurations/aservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-aservice1")},
			},
		},
		{
			Name: to.StringPtr("bservice1"),
			ID:   to.StringPtr("bservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-bservice1")},
			},
		},
	}

	service5 := getTestService("service1", v1.ProtocolTCP, nil, false, 80)
	slb5 := getTestLoadBalancer(to.StringPtr("testCluster"), to.StringPtr("rg"), to.StringPtr("testCluster"), to.StringPtr("aservice1"), service5, "Standard")
	slb5.FrontendIPConfigurations = &[]network.FrontendIPConfiguration{
		{
			Name: to.StringPtr("aservice1"),
			ID:   to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/testCluster/frontendIPConfigurations/aservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-aservice1")},
			},
		},
		{
			Name: to.StringPtr("bservice1"),
			ID:   to.StringPtr("bservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-bservice1")},
			},
		},
	}
	slb5.Probes = &[]network.Probe{
		{
			Name: to.StringPtr("aservice1-" + string(service4.Spec.Ports[0].Protocol) +
				"-" + strconv.Itoa(int(service4.Spec.Ports[0].Port))),
			ProbePropertiesFormat: &network.ProbePropertiesFormat{
				Port: to.Int32Ptr(10080),
			},
		},
		{
			Name: to.StringPtr("aservice1-" + string(service4.Spec.Ports[0].Protocol) +
				"-" + strconv.Itoa(int(service4.Spec.Ports[0].Port))),
			ProbePropertiesFormat: &network.ProbePropertiesFormat{
				Port: to.Int32Ptr(10081),
			},
		},
	}

	//change to false to test that reconciliation will fix it (despite the fact that disable-tcp-reset was removed in 1.20)
	(*slb5.LoadBalancerPropertiesFormat.LoadBalancingRules)[0].EnableTCPReset = to.BoolPtr(false)

	expectedSLb5 := getTestLoadBalancer(to.StringPtr("testCluster"), to.StringPtr("rg"), to.StringPtr("testCluster"), to.StringPtr("aservice1"), service5, "Standard")
	(*expectedSLb5.LoadBalancerPropertiesFormat.LoadBalancingRules)[0].DisableOutboundSnat = to.BoolPtr(true)
	expectedSLb5.FrontendIPConfigurations = &[]network.FrontendIPConfiguration{
		{
			Name: to.StringPtr("aservice1"),
			ID:   to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/testCluster/frontendIPConfigurations/aservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-aservice1")},
			},
		},
		{
			Name: to.StringPtr("bservice1"),
			ID:   to.StringPtr("bservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-bservice1")},
			},
		},
	}

	service6 := getTestService("service1", v1.ProtocolUDP, nil, false, 80)
	lb6 := getTestLoadBalancer(to.StringPtr("testCluster"), to.StringPtr("rg"), to.StringPtr("testCluster"), to.StringPtr("aservice1"), service6, "basic")
	lb6.FrontendIPConfigurations = &[]network.FrontendIPConfiguration{}
	lb6.Probes = &[]network.Probe{}
	expectedLB6 := getTestLoadBalancer(to.StringPtr("testCluster"), to.StringPtr("rg"), to.StringPtr("testCluster"), to.StringPtr("aservice1"), service6, "basic")
	expectedLB6.Probes = &[]network.Probe{}
	(*expectedLB6.LoadBalancerPropertiesFormat.LoadBalancingRules)[0].Probe = &network.SubResource{ID: to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/testCluster/probes/aservice1-TCP-80")}
	expectedLB6.FrontendIPConfigurations = &[]network.FrontendIPConfiguration{
		{
			Name: to.StringPtr("aservice1"),
			ID:   to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/testCluster/frontendIPConfigurations/aservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-aservice1")},
			},
		},
	}

	service7 := getTestService("service1", v1.ProtocolUDP, nil, false, 80)
	service7.Spec.HealthCheckNodePort = 10081
	service7.Spec.ExternalTrafficPolicy = v1.ServiceExternalTrafficPolicyTypeLocal
	lb7 := getTestLoadBalancer(to.StringPtr("testCluster"), to.StringPtr("rg"), to.StringPtr("testCluster"), to.StringPtr("aservice1"), service7, "basic")
	lb7.FrontendIPConfigurations = &[]network.FrontendIPConfiguration{}
	lb7.Probes = &[]network.Probe{}
	expectedLB7 := getTestLoadBalancer(to.StringPtr("testCluster"), to.StringPtr("rg"), to.StringPtr("testCluster"), to.StringPtr("aservice1"), service7, "basic")
	(*expectedLB7.LoadBalancerPropertiesFormat.LoadBalancingRules)[0].Probe = &network.SubResource{
		ID: to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/testCluster/probes/aservice1-UDP-80"),
	}
	(*expectedLB7.LoadBalancerPropertiesFormat.LoadBalancingRules)[0].EnableTCPReset = nil
	(*lb7.LoadBalancerPropertiesFormat.LoadBalancingRules)[0].DisableOutboundSnat = to.BoolPtr(true)
	expectedLB7.FrontendIPConfigurations = &[]network.FrontendIPConfiguration{
		{
			Name: to.StringPtr("aservice1"),
			ID:   to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/testCluster/frontendIPConfigurations/aservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-aservice1")},
			},
		},
	}
	expectedLB7.Probes = &[]network.Probe{
		{
			Name: to.StringPtr("aservice1-" + string(service7.Spec.Ports[0].Protocol) +
				"-" + strconv.Itoa(int(service7.Spec.Ports[0].Port))),
			ProbePropertiesFormat: &network.ProbePropertiesFormat{
				Port:              to.Int32Ptr(10081),
				RequestPath:       to.StringPtr("/healthz"),
				Protocol:          network.ProbeProtocolHTTP,
				IntervalInSeconds: to.Int32Ptr(5),
				NumberOfProbes:    to.Int32Ptr(2),
			},
		},
	}

	service8 := getTestService("service1", v1.ProtocolTCP, nil, false, 80)
	lb8 := getTestLoadBalancer(to.StringPtr("testCluster"), to.StringPtr("anotherRG"), to.StringPtr("testCluster"), to.StringPtr("aservice1"), service8, "Standard")
	lb8.FrontendIPConfigurations = &[]network.FrontendIPConfiguration{}
	lb8.Probes = &[]network.Probe{}
	expectedLB8 := getTestLoadBalancer(to.StringPtr("testCluster"), to.StringPtr("anotherRG"), to.StringPtr("testCluster"), to.StringPtr("aservice1"), service8, "Standard")
	(*expectedLB8.LoadBalancerPropertiesFormat.LoadBalancingRules)[0].DisableOutboundSnat = to.BoolPtr(false)
	expectedLB8.FrontendIPConfigurations = &[]network.FrontendIPConfiguration{
		{
			Name: to.StringPtr("aservice1"),
			ID:   to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/testCluster/frontendIPConfigurations/aservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-aservice1")},
			},
		},
	}
	expectedLB8.Probes = &[]network.Probe{
		{
			Name: to.StringPtr("aservice1-" + string(service8.Spec.Ports[0].Protocol) +
				"-" + strconv.Itoa(int(service7.Spec.Ports[0].Port))),
			ProbePropertiesFormat: &network.ProbePropertiesFormat{
				Port:              to.Int32Ptr(10080),
				Protocol:          network.ProbeProtocolTCP,
				IntervalInSeconds: to.Int32Ptr(5),
				NumberOfProbes:    to.Int32Ptr(2),
			},
		},
	}

	testCases := []struct {
		desc                      string
		service                   v1.Service
		loadBalancerSku           string
		preConfigLBType           string
		loadBalancerResourceGroup string
		disableOutboundSnat       *bool
		wantLb                    bool
		existingLB                network.LoadBalancer
		expectedLB                network.LoadBalancer
		expectedError             error
	}{
		{
			desc: "reconcileLoadBalancer shall return the lb deeply equal to the existingLB if there's no " +
				"modification needed when wantLb == true",
			loadBalancerSku: "basic",
			service:         service1,
			existingLB:      basicLb1,
			wantLb:          true,
			expectedLB:      basicLb1,
			expectedError:   nil,
		},
		{
			desc: "reconcileLoadBalancer shall return the lb deeply equal to the existingLB if there's no " +
				"modification needed when wantLb == false",
			loadBalancerSku: "basic",
			service:         service2,
			existingLB:      basicLb2,
			wantLb:          false,
			expectedLB:      basicLb2,
			expectedError:   nil,
		},
		{
			desc:            "reconcileLoadBalancer shall remove and reconstruct the corresponding field of lb",
			loadBalancerSku: "basic",
			service:         service3,
			existingLB:      modifiedLb1,
			wantLb:          true,
			expectedLB:      expectedLb1,
			expectedError:   nil,
		},
		{
			desc:            "reconcileLoadBalancer shall not raise an error",
			loadBalancerSku: "basic",
			service:         service3,
			existingLB:      modifiedLb1,
			preConfigLBType: "external",
			wantLb:          true,
			expectedLB:      expectedLb1,
			expectedError:   nil,
		},
		{
			desc:                "reconcileLoadBalancer shall remove and reconstruct the corresponding field of lb and set enableTcpReset to true in lbRule",
			loadBalancerSku:     "standard",
			service:             service4,
			disableOutboundSnat: to.BoolPtr(true),
			existingLB:          existingSLB,
			wantLb:              true,
			expectedLB:          expectedSLb,
			expectedError:       nil,
		},
		{
			desc:                "reconcileLoadBalancer shall remove and reconstruct the corresponding field of lb and set enableTcpReset (false => true) in lbRule",
			loadBalancerSku:     "standard",
			service:             service5,
			disableOutboundSnat: to.BoolPtr(true),
			existingLB:          slb5,
			wantLb:              true,
			expectedLB:          expectedSLb5,
			expectedError:       nil,
		},
		{
			desc:            "reconcileLoadBalancer shall reconcile UDP services",
			loadBalancerSku: "basic",
			service:         service6,
			existingLB:      lb6,
			wantLb:          true,
			expectedLB:      expectedLB6,
			expectedError:   nil,
		},
		{
			desc:            "reconcileLoadBalancer shall reconcile probes for local traffic policy UDP services",
			loadBalancerSku: "basic",
			service:         service7,
			existingLB:      lb7,
			wantLb:          true,
			expectedLB:      expectedLB7,
			expectedError:   nil,
		},
		{
			desc:                      "reconcileLoadBalancer in other resource group",
			loadBalancerSku:           "standard",
			loadBalancerResourceGroup: "anotherRG",
			service:                   service8,
			existingLB:                lb8,
			wantLb:                    true,
			expectedLB:                expectedLB8,
			expectedError:             nil,
		},
	}

	for i, test := range testCases {
		az := GetTestCloud(ctrl)
		az.Config.LoadBalancerSku = test.loadBalancerSku
		az.DisableOutboundSNAT = test.disableOutboundSnat
		if test.preConfigLBType != "" {
			az.Config.PreConfiguredBackendPoolLoadBalancerTypes = test.preConfigLBType
		}
		az.LoadBalancerResourceGroup = test.loadBalancerResourceGroup

		clusterResources, expectedInterfaces, expectedVirtualMachines := getClusterResources(az, 3, 3)
		setMockEnv(az, ctrl, expectedInterfaces, expectedVirtualMachines, 1)

		test.service.Spec.LoadBalancerIP = "1.2.3.4"

		err := az.PublicIPAddressesClient.CreateOrUpdate(context.TODO(), "rg", "pipName", network.PublicIPAddress{
			Name: to.StringPtr("pipName"),
			PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
				IPAddress: to.StringPtr("1.2.3.4"),
			},
		})
		if err != nil {
			t.Fatalf("TestCase[%d] meets unexpected error: %v", i, err)
		}

		mockLBsClient := mockloadbalancerclient.NewMockInterface(ctrl)
		mockLBsClient.EXPECT().List(gomock.Any(), az.getLoadBalancerResourceGroup()).Return([]network.LoadBalancer{test.existingLB}, nil)
		mockLBsClient.EXPECT().Get(gomock.Any(), az.getLoadBalancerResourceGroup(), *test.existingLB.Name, gomock.Any()).Return(test.existingLB, nil).AnyTimes()
		mockLBsClient.EXPECT().CreateOrUpdate(gomock.Any(), az.getLoadBalancerResourceGroup(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		az.LoadBalancerClient = mockLBsClient

		err = az.LoadBalancerClient.CreateOrUpdate(context.TODO(), az.getLoadBalancerResourceGroup(), "lb1", test.existingLB, "")
		if err != nil {
			t.Fatalf("TestCase[%d] meets unexpected error: %v", i, err)
		}

		lb, rerr := az.reconcileLoadBalancer("testCluster", &test.service, clusterResources.nodes, test.wantLb)
		assert.Equal(t, test.expectedError, rerr, "TestCase[%d]: %s", i, test.desc)

		if test.expectedError == nil {
			assert.Equal(t, test.expectedLB, *lb, "TestCase[%d]: %s", i, test.desc)
		}
	}
}

func TestGetServiceLoadBalancerStatus(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	az := GetTestCloud(ctrl)
	service := getTestService("service1", v1.ProtocolTCP, nil, false, 80)
	internalService := getInternalTestService("service1", 80)

	setMockPublicIPs(az, ctrl, 1)

	lb1 := getTestLoadBalancer(to.StringPtr("lb1"), to.StringPtr("rg"), to.StringPtr("testCluster"),
		to.StringPtr("aservice1"), internalService, "Basic")
	lb1.FrontendIPConfigurations = nil
	lb2 := getTestLoadBalancer(to.StringPtr("lb2"), to.StringPtr("rg"), to.StringPtr("testCluster"),
		to.StringPtr("aservice1"), internalService, "Basic")
	lb2.FrontendIPConfigurations = &[]network.FrontendIPConfiguration{
		{
			Name: to.StringPtr("aservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress:  &network.PublicIPAddress{ID: to.StringPtr("testCluster-aservice1")},
				PrivateIPAddress: to.StringPtr("private"),
			},
		},
	}
	lb3 := getTestLoadBalancer(to.StringPtr("lb3"), to.StringPtr("rg"), to.StringPtr("testCluster"),
		to.StringPtr("test1"), internalService, "Basic")
	lb3.FrontendIPConfigurations = &[]network.FrontendIPConfiguration{
		{
			Name: to.StringPtr("bservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress:  &network.PublicIPAddress{ID: to.StringPtr("testCluster-bservice1")},
				PrivateIPAddress: to.StringPtr("private"),
			},
		},
	}
	lb4 := getTestLoadBalancer(to.StringPtr("lb4"), to.StringPtr("rg"), to.StringPtr("testCluster"),
		to.StringPtr("aservice1"), service, "Basic")
	lb4.FrontendIPConfigurations = &[]network.FrontendIPConfiguration{
		{
			Name: to.StringPtr("aservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress:  &network.PublicIPAddress{ID: nil},
				PrivateIPAddress: to.StringPtr("private"),
			},
		},
	}
	lb5 := getTestLoadBalancer(to.StringPtr("lb5"), to.StringPtr("rg"), to.StringPtr("testCluster"),
		to.StringPtr("aservice1"), service, "Basic")
	lb5.FrontendIPConfigurations = &[]network.FrontendIPConfiguration{
		{
			Name: to.StringPtr("aservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress:  nil,
				PrivateIPAddress: to.StringPtr("private"),
			},
		},
	}
	lb6 := getTestLoadBalancer(to.StringPtr("lb6"), to.StringPtr("rg"), to.StringPtr("testCluster"),
		to.StringPtr("aservice1"), service, "Basic")
	lb6.FrontendIPConfigurations = &[]network.FrontendIPConfiguration{
		{
			Name: to.StringPtr("aservice1"),
			FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
				PublicIPAddress:  &network.PublicIPAddress{ID: to.StringPtr("illegal/id/")},
				PrivateIPAddress: to.StringPtr("private"),
			},
		},
	}

	testCases := []struct {
		desc           string
		service        *v1.Service
		lb             *network.LoadBalancer
		expectedStatus *v1.LoadBalancerStatus
		expectedError  bool
	}{
		{
			desc:    "getServiceLoadBalancer shall return nil if no lb is given",
			service: &service,
			lb:      nil,
		},
		{
			desc:    "getServiceLoadBalancerStatus shall return nil if given lb has no front ip config",
			service: &service,
			lb:      &lb1,
		},
		{
			desc:           "getServiceLoadBalancerStatus shall return private ip if service is internal",
			service:        &internalService,
			lb:             &lb2,
			expectedStatus: &v1.LoadBalancerStatus{Ingress: []v1.LoadBalancerIngress{{IP: "private"}}},
		},
		{
			desc: "getServiceLoadBalancerStatus shall return nil if lb.FrontendIPConfigurations.name != " +
				"az.getDefaultFrontendIPConfigName(service)",
			service: &internalService,
			lb:      &lb3,
		},
		{
			desc: "getServiceLoadBalancerStatus shall report error if the id of lb's " +
				"public ip address cannot be read",
			service:       &service,
			lb:            &lb4,
			expectedError: true,
		},
		{
			desc:          "getServiceLoadBalancerStatus shall report error if lb's public ip address cannot be read",
			service:       &service,
			lb:            &lb5,
			expectedError: true,
		},
		{
			desc:          "getServiceLoadBalancerStatus shall report error if id of lb's public ip address is illegal",
			service:       &service,
			lb:            &lb6,
			expectedError: true,
		},
		{
			desc: "getServiceLoadBalancerStatus shall return the corresponding " +
				"lb status if everything is good",
			service:        &service,
			lb:             &lb2,
			expectedStatus: &v1.LoadBalancerStatus{Ingress: []v1.LoadBalancerIngress{{IP: "1.2.3.4"}}},
		},
	}

	for i, test := range testCases {
		status, _, err := az.getServiceLoadBalancerStatus(test.service, test.lb)
		assert.Equal(t, test.expectedStatus, status, "TestCase[%d]: %s", i, test.desc)
		assert.Equal(t, test.expectedError, err != nil, "TestCase[%d]: %s", i, test.desc)
	}
}

func TestReconcileSecurityGroup(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testCases := []struct {
		desc          string
		lbIP          *string
		service       v1.Service
		existingSgs   map[string]network.SecurityGroup
		expectedSg    *network.SecurityGroup
		wantLb        bool
		expectedError bool
	}{
		{
			desc: "reconcileSecurityGroup shall report error if the sg is shared and no ports in service",
			service: v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						consts.ServiceAnnotationSharedSecurityRule: "true",
					},
				},
			},
			expectedError: true,
		},
		{
			desc:          "reconcileSecurityGroup shall report error if no such sg can be found",
			service:       getTestService("test1", v1.ProtocolTCP, nil, false, 80),
			expectedError: true,
		},
		{
			desc:          "reconcileSecurityGroup shall report error if wantLb is true and lbIP is nil",
			service:       getTestService("test1", v1.ProtocolTCP, nil, false, 80),
			wantLb:        true,
			existingSgs:   map[string]network.SecurityGroup{"nsg": {}},
			expectedError: true,
		},
		{
			desc:        "reconcileSecurityGroup shall remain the existingSgs intact if nothing needs to be modified",
			service:     getTestService("test1", v1.ProtocolTCP, nil, false, 80),
			existingSgs: map[string]network.SecurityGroup{"nsg": {}},
			expectedSg:  &network.SecurityGroup{},
		},
		{
			desc:    "reconcileSecurityGroup shall delete unwanted sgs and create needed ones",
			service: getTestService("test1", v1.ProtocolTCP, nil, false, 80),
			existingSgs: map[string]network.SecurityGroup{"nsg": {
				Name: to.StringPtr("nsg"),
				SecurityGroupPropertiesFormat: &network.SecurityGroupPropertiesFormat{
					SecurityRules: &[]network.SecurityRule{
						{
							Name: to.StringPtr("atest1-toBeDeleted"),
							SecurityRulePropertiesFormat: &network.SecurityRulePropertiesFormat{
								SourceAddressPrefix:      to.StringPtr("prefix"),
								SourcePortRange:          to.StringPtr("range"),
								DestinationAddressPrefix: to.StringPtr("desPrefix"),
								DestinationPortRange:     to.StringPtr("desRange"),
							},
						},
					},
				},
			}},
			lbIP:   to.StringPtr("1.1.1.1"),
			wantLb: true,
			expectedSg: &network.SecurityGroup{
				Name: to.StringPtr("nsg"),
				SecurityGroupPropertiesFormat: &network.SecurityGroupPropertiesFormat{
					SecurityRules: &[]network.SecurityRule{
						{
							Name: to.StringPtr("atest1-TCP-80-Internet"),
							SecurityRulePropertiesFormat: &network.SecurityRulePropertiesFormat{
								Protocol:                 network.SecurityRuleProtocol("Tcp"),
								SourcePortRange:          to.StringPtr("*"),
								DestinationPortRange:     to.StringPtr("80"),
								SourceAddressPrefix:      to.StringPtr("Internet"),
								DestinationAddressPrefix: to.StringPtr("1.1.1.1"),
								Access:                   network.SecurityRuleAccess("Allow"),
								Priority:                 to.Int32Ptr(500),
								Direction:                network.SecurityRuleDirection("Inbound"),
							},
						},
					},
				},
			},
		},
		{
			desc:    "reconcileSecurityGroup shall create sgs with correct destinationPrefix for IPv6",
			service: getTestService("test1", v1.ProtocolTCP, nil, true, 80),
			existingSgs: map[string]network.SecurityGroup{"nsg": {
				Name:                          to.StringPtr("nsg"),
				SecurityGroupPropertiesFormat: &network.SecurityGroupPropertiesFormat{},
			}},
			lbIP:   to.StringPtr("fd00::eef0"),
			wantLb: true,
			expectedSg: &network.SecurityGroup{
				Name: to.StringPtr("nsg"),
				SecurityGroupPropertiesFormat: &network.SecurityGroupPropertiesFormat{
					SecurityRules: &[]network.SecurityRule{
						{
							Name: to.StringPtr("atest1-TCP-80-Internet"),
							SecurityRulePropertiesFormat: &network.SecurityRulePropertiesFormat{
								Protocol:                 network.SecurityRuleProtocol("Tcp"),
								SourcePortRange:          to.StringPtr("*"),
								DestinationPortRange:     to.StringPtr("80"),
								SourceAddressPrefix:      to.StringPtr("Internet"),
								DestinationAddressPrefix: to.StringPtr("fd00::eef0"),
								Access:                   network.SecurityRuleAccess("Allow"),
								Priority:                 to.Int32Ptr(500),
								Direction:                network.SecurityRuleDirection("Inbound"),
							},
						},
					},
				},
			},
		},
		{
			desc:    "reconcileSecurityGroup shall create sgs with correct destinationPrefix with additional public IPs",
			service: getTestService("test1", v1.ProtocolTCP, map[string]string{consts.ServiceAnnotationAdditionalPublicIPs: "2.3.4.5"}, true, 80),
			existingSgs: map[string]network.SecurityGroup{"nsg": {
				Name:                          to.StringPtr("nsg"),
				SecurityGroupPropertiesFormat: &network.SecurityGroupPropertiesFormat{},
			}},
			lbIP:   to.StringPtr("1.2.3.4"),
			wantLb: true,
			expectedSg: &network.SecurityGroup{
				Name: to.StringPtr("nsg"),
				SecurityGroupPropertiesFormat: &network.SecurityGroupPropertiesFormat{
					SecurityRules: &[]network.SecurityRule{
						{
							Name: to.StringPtr("atest1-TCP-80-Internet"),
							SecurityRulePropertiesFormat: &network.SecurityRulePropertiesFormat{
								Protocol:                   network.SecurityRuleProtocol("Tcp"),
								SourcePortRange:            to.StringPtr("*"),
								DestinationPortRange:       to.StringPtr("80"),
								SourceAddressPrefix:        to.StringPtr("Internet"),
								DestinationAddressPrefixes: to.StringSlicePtr([]string{"1.2.3.4", "2.3.4.5"}),
								Access:                     network.SecurityRuleAccess("Allow"),
								Priority:                   to.Int32Ptr(500),
								Direction:                  network.SecurityRuleDirection("Inbound"),
							},
						},
					},
				},
			},
		},
		{
			desc:    "reconcileSecurityGroup shall not create unwanted security rules if there is service tags",
			service: getTestService("test1", v1.ProtocolTCP, map[string]string{consts.ServiceAnnotationAllowedServiceTag: "tag"}, true, 80),
			wantLb:  true,
			lbIP:    to.StringPtr("1.1.1.1"),
			existingSgs: map[string]network.SecurityGroup{"nsg": {
				Name: to.StringPtr("nsg"),
				SecurityGroupPropertiesFormat: &network.SecurityGroupPropertiesFormat{
					SecurityRules: &[]network.SecurityRule{
						{
							Name: to.StringPtr("atest1-toBeDeleted"),
							SecurityRulePropertiesFormat: &network.SecurityRulePropertiesFormat{
								SourceAddressPrefix:      to.StringPtr("prefix"),
								SourcePortRange:          to.StringPtr("range"),
								DestinationAddressPrefix: to.StringPtr("destPrefix"),
								DestinationPortRange:     to.StringPtr("desRange"),
							},
						},
					},
				},
			}},
			expectedSg: &network.SecurityGroup{
				Name: to.StringPtr("nsg"),
				SecurityGroupPropertiesFormat: &network.SecurityGroupPropertiesFormat{
					SecurityRules: &[]network.SecurityRule{
						{
							Name: to.StringPtr("atest1-TCP-80-tag"),
							SecurityRulePropertiesFormat: &network.SecurityRulePropertiesFormat{
								Protocol:                 network.SecurityRuleProtocol("Tcp"),
								SourcePortRange:          to.StringPtr("*"),
								DestinationPortRange:     to.StringPtr("80"),
								SourceAddressPrefix:      to.StringPtr("tag"),
								DestinationAddressPrefix: to.StringPtr("1.1.1.1"),
								Access:                   network.SecurityRuleAccess("Allow"),
								Priority:                 to.Int32Ptr(500),
								Direction:                network.SecurityRuleDirection("Inbound"),
							},
						},
					},
				},
			},
		},
		{
			desc:    "reconcileSecurityGroup shall create shared sgs for service with azure-shared-securityrule annotations",
			service: getTestService("test1", v1.ProtocolTCP, map[string]string{consts.ServiceAnnotationSharedSecurityRule: "true"}, true, 80),
			existingSgs: map[string]network.SecurityGroup{"nsg": {
				Name:                          to.StringPtr("nsg"),
				SecurityGroupPropertiesFormat: &network.SecurityGroupPropertiesFormat{},
			}},
			lbIP:   to.StringPtr("1.2.3.4"),
			wantLb: true,
			expectedSg: &network.SecurityGroup{
				Name: to.StringPtr("nsg"),
				SecurityGroupPropertiesFormat: &network.SecurityGroupPropertiesFormat{
					SecurityRules: &[]network.SecurityRule{
						{
							Name: to.StringPtr("shared-TCP-80-Internet"),
							SecurityRulePropertiesFormat: &network.SecurityRulePropertiesFormat{
								Protocol:                   network.SecurityRuleProtocol("Tcp"),
								SourcePortRange:            to.StringPtr("*"),
								DestinationPortRange:       to.StringPtr("80"),
								SourceAddressPrefix:        to.StringPtr("Internet"),
								DestinationAddressPrefixes: to.StringSlicePtr([]string{"1.2.3.4"}),
								Access:                     network.SecurityRuleAccess("Allow"),
								Priority:                   to.Int32Ptr(500),
								Direction:                  network.SecurityRuleDirection("Inbound"),
							},
						},
					},
				},
			},
		},
	}

	for i, test := range testCases {
		az := GetTestCloud(ctrl)
		mockSGsClient := az.SecurityGroupsClient.(*mocksecuritygroupclient.MockInterface)
		mockSGsClient.EXPECT().CreateOrUpdate(gomock.Any(), "rg", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		if len(test.existingSgs) == 0 {
			mockSGsClient.EXPECT().Get(gomock.Any(), "rg", gomock.Any(), gomock.Any()).Return(network.SecurityGroup{}, &retry.Error{HTTPStatusCode: http.StatusNotFound}).AnyTimes()
		}
		for name, sg := range test.existingSgs {
			mockSGsClient.EXPECT().Get(gomock.Any(), "rg", name, gomock.Any()).Return(sg, nil).AnyTimes()
			err := az.SecurityGroupsClient.CreateOrUpdate(context.TODO(), "rg", name, sg, "")
			if err != nil {
				t.Fatalf("TestCase[%d] meets unexpected error: %v", i, err)
			}
		}
		sg, err := az.reconcileSecurityGroup("testCluster", &test.service, test.lbIP, test.wantLb)
		assert.Equal(t, test.expectedSg, sg, "TestCase[%d]: %s", i, test.desc)
		assert.Equal(t, test.expectedError, err != nil, "TestCase[%d]: %s", i, test.desc)
	}
}

func TestReconcileSecurityGroupLoadBalancerSourceRanges(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	az := GetTestCloud(ctrl)
	service := getTestService("test1", v1.ProtocolTCP, map[string]string{consts.ServiceAnnotationDenyAllExceptLoadBalancerSourceRanges: "true"}, false, 80)
	service.Spec.LoadBalancerSourceRanges = []string{"1.2.3.4/32"}
	existingSg := network.SecurityGroup{
		Name: to.StringPtr("nsg"),
		SecurityGroupPropertiesFormat: &network.SecurityGroupPropertiesFormat{
			SecurityRules: &[]network.SecurityRule{},
		},
	}
	lbIP := to.StringPtr("1.1.1.1")
	expectedSg := network.SecurityGroup{
		Name: to.StringPtr("nsg"),
		SecurityGroupPropertiesFormat: &network.SecurityGroupPropertiesFormat{
			SecurityRules: &[]network.SecurityRule{
				{
					Name: to.StringPtr("atest1-TCP-80-1.2.3.4_32"),
					SecurityRulePropertiesFormat: &network.SecurityRulePropertiesFormat{
						Protocol:                 network.SecurityRuleProtocol("Tcp"),
						SourcePortRange:          to.StringPtr("*"),
						SourceAddressPrefix:      to.StringPtr("1.2.3.4/32"),
						DestinationPortRange:     to.StringPtr("80"),
						DestinationAddressPrefix: to.StringPtr("1.1.1.1"),
						Access:                   network.SecurityRuleAccess("Allow"),
						Priority:                 to.Int32Ptr(500),
						Direction:                network.SecurityRuleDirection("Inbound"),
					},
				},
				{
					Name: to.StringPtr("atest1-TCP-80-deny_all"),
					SecurityRulePropertiesFormat: &network.SecurityRulePropertiesFormat{
						Protocol:                 network.SecurityRuleProtocol("Tcp"),
						SourcePortRange:          to.StringPtr("*"),
						SourceAddressPrefix:      to.StringPtr("*"),
						DestinationPortRange:     to.StringPtr("80"),
						DestinationAddressPrefix: to.StringPtr("1.1.1.1"),
						Access:                   network.SecurityRuleAccess("Deny"),
						Priority:                 to.Int32Ptr(501),
						Direction:                network.SecurityRuleDirection("Inbound"),
					},
				},
			},
		},
	}
	mockSGClient := az.SecurityGroupsClient.(*mocksecuritygroupclient.MockInterface)
	mockSGClient.EXPECT().Get(gomock.Any(), az.ResourceGroup, gomock.Any(), gomock.Any()).Return(existingSg, nil)
	mockSGClient.EXPECT().CreateOrUpdate(gomock.Any(), az.ResourceGroup, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	sg, err := az.reconcileSecurityGroup("testCluster", &service, lbIP, true)
	assert.NoError(t, err)
	assert.Equal(t, expectedSg, *sg)
}

func TestSafeDeletePublicIP(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testCases := []struct {
		desc          string
		pip           *network.PublicIPAddress
		lb            *network.LoadBalancer
		expectedError bool
	}{
		{
			desc: "safeDeletePublicIP shall delete corresponding ip configurations and lb rules",
			pip: &network.PublicIPAddress{
				Name: to.StringPtr("pip1"),
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					IPConfiguration: &network.IPConfiguration{
						ID: to.StringPtr("id1"),
					},
				},
			},
			lb: &network.LoadBalancer{
				Name: to.StringPtr("lb1"),
				LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
					FrontendIPConfigurations: &[]network.FrontendIPConfiguration{
						{
							ID: to.StringPtr("id1"),
							FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
								LoadBalancingRules: &[]network.SubResource{{ID: to.StringPtr("rules1")}},
							},
						},
					},
					LoadBalancingRules: &[]network.LoadBalancingRule{{ID: to.StringPtr("rules1")}},
				},
			},
		},
	}

	for i, test := range testCases {
		az := GetTestCloud(ctrl)
		mockPIPsClient := az.PublicIPAddressesClient.(*mockpublicipclient.MockInterface)
		mockPIPsClient.EXPECT().CreateOrUpdate(gomock.Any(), "rg", "pip1", gomock.Any()).Return(nil).AnyTimes()
		mockPIPsClient.EXPECT().Delete(gomock.Any(), "rg", "pip1").Return(nil).AnyTimes()
		err := az.PublicIPAddressesClient.CreateOrUpdate(context.TODO(), "rg", "pip1", network.PublicIPAddress{
			Name: to.StringPtr("pip1"),
			PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
				IPConfiguration: &network.IPConfiguration{
					ID: to.StringPtr("id1"),
				},
			},
		})
		if err != nil {
			t.Fatalf("TestCase[%d] meets unexpected error: %v", i, err)
		}
		service := getTestService("test1", v1.ProtocolTCP, nil, false, 80)
		mockLBsClient := mockloadbalancerclient.NewMockInterface(ctrl)
		mockLBsClient.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		az.LoadBalancerClient = mockLBsClient
		rerr := az.safeDeletePublicIP(&service, "rg", test.pip, test.lb)
		assert.Equal(t, 0, len(*test.lb.FrontendIPConfigurations), "TestCase[%d]: %s", i, test.desc)
		assert.Equal(t, 0, len(*test.lb.LoadBalancingRules), "TestCase[%d]: %s", i, test.desc)
		assert.Equal(t, test.expectedError, rerr != nil, "TestCase[%d]: %s", i, test.desc)
	}
}

func TestReconcilePublicIP(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testCases := []struct {
		desc                        string
		expectedID                  string
		annotations                 map[string]string
		existingPIPs                []network.PublicIPAddress
		expectedPIP                 *network.PublicIPAddress
		wantLb                      bool
		expectedError               bool
		expectedCreateOrUpdateCount int
		expectedDeleteCount         int
	}{
		{
			desc:                        "reconcilePublicIP shall return nil if there's no pip in service",
			wantLb:                      false,
			expectedCreateOrUpdateCount: 0,
			expectedDeleteCount:         0,
		},
		{
			desc:   "reconcilePublicIP shall return nil if no pip is owned by service",
			wantLb: false,
			existingPIPs: []network.PublicIPAddress{
				{
					Name: to.StringPtr("pip1"),
				},
			},
			expectedCreateOrUpdateCount: 0,
			expectedDeleteCount:         0,
		},
		{
			desc:   "reconcilePublicIP shall delete unwanted pips and create a new one",
			wantLb: true,
			existingPIPs: []network.PublicIPAddress{
				{
					Name: to.StringPtr("pip1"),
					Tags: map[string]*string{"service": to.StringPtr("default/test1")},
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						IPAddress: to.StringPtr("1.2.3.4"),
					},
				},
			},
			expectedID: "/subscriptions/subscription/resourceGroups/rg/providers/" +
				"Microsoft.Network/publicIPAddresses/testCluster-atest1",
			expectedCreateOrUpdateCount: 1,
			expectedDeleteCount:         1,
		},
		{
			desc:        "reconcilePublicIP shall report error if the given PIP name doesn't exist in the resource group",
			wantLb:      true,
			annotations: map[string]string{consts.ServiceAnnotationPIPName: "testPIP"},
			existingPIPs: []network.PublicIPAddress{
				{
					Name: to.StringPtr("pip1"),
					Tags: map[string]*string{"service": to.StringPtr("default/test1")},
				},
				{
					Name: to.StringPtr("pip2"),
					Tags: map[string]*string{"service": to.StringPtr("default/test1")},
				},
			},
			expectedError:               true,
			expectedCreateOrUpdateCount: 0,
			expectedDeleteCount:         0,
		},
		{
			desc:        "reconcilePublicIP shall delete unwanted PIP when given the name of desired PIP",
			wantLb:      true,
			annotations: map[string]string{consts.ServiceAnnotationPIPName: "testPIP"},
			existingPIPs: []network.PublicIPAddress{
				{
					Name: to.StringPtr("pip1"),
					Tags: map[string]*string{"service": to.StringPtr("default/test1")},
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						IPAddress: to.StringPtr("1.2.3.4"),
					},
				},
				{
					Name: to.StringPtr("pip2"),
					Tags: map[string]*string{"service": to.StringPtr("default/test1")},
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						IPAddress: to.StringPtr("1.2.3.4"),
					},
				},
				{
					Name: to.StringPtr("testPIP"),
					Tags: map[string]*string{"service": to.StringPtr("default/test1")},
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						IPAddress: to.StringPtr("1.2.3.4"),
					},
				},
			},
			expectedPIP: &network.PublicIPAddress{
				ID:   to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/publicIPAddresses/testPIP"),
				Name: to.StringPtr("testPIP"),
				Tags: map[string]*string{"service": to.StringPtr("default/test1")},
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					PublicIPAddressVersion: network.IPVersionIPv4,
					IPAddress:              to.StringPtr("1.2.3.4"),
				},
			},
			expectedCreateOrUpdateCount: 1,
			expectedDeleteCount:         2,
		},
		{
			desc:        "reconcilePublicIP shall not delete unwanted PIP when there are other service references",
			wantLb:      true,
			annotations: map[string]string{consts.ServiceAnnotationPIPName: "testPIP"},
			existingPIPs: []network.PublicIPAddress{
				{
					Name: to.StringPtr("pip1"),
					Tags: map[string]*string{"service": to.StringPtr("default/test1")},
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						IPAddress: to.StringPtr("1.2.3.4"),
					},
				},
				{
					Name: to.StringPtr("pip2"),
					Tags: map[string]*string{"service": to.StringPtr("default/test1,default/test2")},
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						IPAddress: to.StringPtr("1.2.3.4"),
					},
				},
				{
					Name: to.StringPtr("testPIP"),
					Tags: map[string]*string{"service": to.StringPtr("default/test1")},
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						IPAddress: to.StringPtr("1.2.3.4"),
					},
				},
			},
			expectedPIP: &network.PublicIPAddress{
				ID:   to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/publicIPAddresses/testPIP"),
				Name: to.StringPtr("testPIP"),
				Tags: map[string]*string{"service": to.StringPtr("default/test1")},
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					PublicIPAddressVersion: network.IPVersionIPv4,
					IPAddress:              to.StringPtr("1.2.3.4"),
				},
			},
			expectedCreateOrUpdateCount: 1,
			expectedDeleteCount:         1,
		},
		{
			desc:   "reconcilePublicIP shall delete unwanted pips and existing pips, when the existing pips IP tags do not match",
			wantLb: true,
			annotations: map[string]string{
				consts.ServiceAnnotationPIPName:           "testPIP",
				consts.ServiceAnnotationIPTagsForPublicIP: "tag1=tag1value",
			},
			existingPIPs: []network.PublicIPAddress{
				{
					Name: to.StringPtr("pip1"),
					Tags: map[string]*string{"service": to.StringPtr("default/test1")},
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						IPAddress: to.StringPtr("1.2.3.4"),
					},
				},
				{
					Name: to.StringPtr("pip2"),
					Tags: map[string]*string{"service": to.StringPtr("default/test1")},
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						IPAddress: to.StringPtr("1.2.3.4"),
					},
				},
				{
					Name: to.StringPtr("testPIP"),
					Tags: map[string]*string{"service": to.StringPtr("default/test1")},
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						IPAddress: to.StringPtr("1.2.3.4"),
					},
				},
			},
			expectedPIP: &network.PublicIPAddress{
				ID:   to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/publicIPAddresses/testPIP"),
				Name: to.StringPtr("testPIP"),
				Tags: map[string]*string{"service": to.StringPtr("default/test1")},
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					PublicIPAddressVersion:   network.IPVersionIPv4,
					PublicIPAllocationMethod: network.IPAllocationMethodStatic,
					IPTags: &[]network.IPTag{
						{
							IPTagType: to.StringPtr("tag1"),
							Tag:       to.StringPtr("tag1value"),
						},
					},
				},
			},
			expectedCreateOrUpdateCount: 1,
			expectedDeleteCount:         2,
		},
		{
			desc:   "reconcilePublicIP shall preserve existing pips, when the existing pips IP tags do match",
			wantLb: true,
			annotations: map[string]string{
				consts.ServiceAnnotationPIPName:           "testPIP",
				consts.ServiceAnnotationIPTagsForPublicIP: "tag1=tag1value",
			},
			existingPIPs: []network.PublicIPAddress{
				{
					Name: to.StringPtr("testPIP"),
					Tags: map[string]*string{"service": to.StringPtr("default/test1")},
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						PublicIPAddressVersion:   network.IPVersionIPv4,
						PublicIPAllocationMethod: network.IPAllocationMethodStatic,
						IPTags: &[]network.IPTag{
							{
								IPTagType: to.StringPtr("tag1"),
								Tag:       to.StringPtr("tag1value"),
							},
						},
						IPAddress: to.StringPtr("1.2.3.4"),
					},
				},
			},
			expectedPIP: &network.PublicIPAddress{
				ID:   to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/publicIPAddresses/testPIP"),
				Name: to.StringPtr("testPIP"),
				Tags: map[string]*string{"service": to.StringPtr("default/test1")},
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					PublicIPAddressVersion:   network.IPVersionIPv4,
					PublicIPAllocationMethod: network.IPAllocationMethodStatic,
					IPTags: &[]network.IPTag{
						{
							IPTagType: to.StringPtr("tag1"),
							Tag:       to.StringPtr("tag1value"),
						},
					},
					IPAddress: to.StringPtr("1.2.3.4"),
				},
			},
			expectedCreateOrUpdateCount: 1,
			expectedDeleteCount:         0,
		},
		{
			desc:        "reconcilePublicIP shall find the PIP by given name and shall not delete the PIP which is not owned by service",
			wantLb:      true,
			annotations: map[string]string{consts.ServiceAnnotationPIPName: "testPIP"},
			existingPIPs: []network.PublicIPAddress{
				{
					Name: to.StringPtr("pip1"),
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						IPAddress: to.StringPtr("1.2.3.4"),
					},
				},
				{
					Name: to.StringPtr("pip2"),
					Tags: map[string]*string{"service": to.StringPtr("default/test1")},
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						IPAddress: to.StringPtr("1.2.3.4"),
					},
				},
				{
					Name: to.StringPtr("testPIP"),
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						IPAddress: to.StringPtr("1.2.3.4"),
					},
				},
			},
			expectedPIP: &network.PublicIPAddress{
				ID:   to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/publicIPAddresses/testPIP"),
				Name: to.StringPtr("testPIP"),
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					PublicIPAddressVersion: network.IPVersionIPv4,
					IPAddress:              to.StringPtr("1.2.3.4"),
				},
			},
			expectedCreateOrUpdateCount: 1,
			expectedDeleteCount:         1,
		},
		{
			desc:   "reconcilePublicIP shall delete the unwanted PIP name from service tag and shall not delete it if there is other reference",
			wantLb: false,
			existingPIPs: []network.PublicIPAddress{
				{
					Name: to.StringPtr("pip1"),
					Tags: map[string]*string{consts.ServiceTagKey: to.StringPtr("default/test1,default/test2")},
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						IPAddress: to.StringPtr("1.2.3.4"),
					},
				},
			},
			expectedCreateOrUpdateCount: 1,
		},
	}

	for i, test := range testCases {
		t.Run(test.desc, func(t *testing.T) {
			deletedPips := make(map[string]bool)
			savedPips := make(map[string]network.PublicIPAddress)
			createOrUpdateCount := 0
			var m sync.Mutex
			az := GetTestCloud(ctrl)
			mockPIPsClient := az.PublicIPAddressesClient.(*mockpublicipclient.MockInterface)
			creator := mockPIPsClient.EXPECT().CreateOrUpdate(gomock.Any(), "rg", gomock.Any(), gomock.Any()).AnyTimes()
			creator.DoAndReturn(func(ctx context.Context, resourceGroupName string, publicIPAddressName string, parameters network.PublicIPAddress) *retry.Error {
				m.Lock()
				deletedPips[publicIPAddressName] = false
				savedPips[publicIPAddressName] = parameters
				createOrUpdateCount++
				m.Unlock()
				return nil
			})

			mockPIPsClient.EXPECT().List(gomock.Any(), "rg").Return(test.existingPIPs, nil).AnyTimes()
			if i == 2 {
				mockPIPsClient.EXPECT().Get(gomock.Any(), "rg", "testCluster-atest1", gomock.Any()).Return(network.PublicIPAddress{}, &retry.Error{HTTPStatusCode: http.StatusNotFound}).Times(1)
				mockPIPsClient.EXPECT().Get(gomock.Any(), "rg", "testCluster-atest1", gomock.Any()).Return(network.PublicIPAddress{ID: to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/publicIPAddresses/testCluster-atest1")}, nil).Times(1)
			}
			service := getTestService("test1", v1.ProtocolTCP, nil, false, 80)
			service.Annotations = test.annotations
			for _, pip := range test.existingPIPs {
				savedPips[*pip.Name] = pip
				getter := mockPIPsClient.EXPECT().Get(gomock.Any(), "rg", *pip.Name, gomock.Any()).AnyTimes()
				getter.DoAndReturn(func(ctx context.Context, resourceGroupName string, publicIPAddressName string, expand string) (result network.PublicIPAddress, rerr *retry.Error) {
					m.Lock()
					deletedValue, deletedContains := deletedPips[publicIPAddressName]
					savedPipValue, savedPipContains := savedPips[publicIPAddressName]
					m.Unlock()

					if (!deletedContains || !deletedValue) && savedPipContains {
						return savedPipValue, nil
					}

					return network.PublicIPAddress{}, &retry.Error{HTTPStatusCode: http.StatusNotFound}

				})
				deleter := mockPIPsClient.EXPECT().Delete(gomock.Any(), "rg", *pip.Name).Return(nil).AnyTimes()
				deleter.Do(func(ctx context.Context, resourceGroupName string, publicIPAddressName string) *retry.Error {
					m.Lock()
					deletedPips[publicIPAddressName] = true
					m.Unlock()
					return nil
				})

				err := az.PublicIPAddressesClient.CreateOrUpdate(context.TODO(), "rg", to.String(pip.Name), pip)
				if err != nil {
					t.Fatalf("TestCase[%d] meets unexpected error: %v", i, err)
				}

				// Clear create or update count to prepare for main execution
				createOrUpdateCount = 0
			}
			pip, err := az.reconcilePublicIP("testCluster", &service, "", test.wantLb)
			if !test.expectedError {
				assert.Equal(t, nil, err, "TestCase[%d]: %s", i, test.desc)
			}
			if test.expectedID != "" {
				assert.Equal(t, test.expectedID, to.String(pip.ID), "TestCase[%d]: %s", i, test.desc)
			} else if test.expectedPIP != nil && test.expectedPIP.Name != nil {
				assert.Equal(t, *test.expectedPIP.Name, *pip.Name, "TestCase[%d]: %s", i, test.desc)

				if test.expectedPIP.PublicIPAddressPropertiesFormat != nil {
					sortIPTags(test.expectedPIP.PublicIPAddressPropertiesFormat.IPTags)
				}

				if pip.PublicIPAddressPropertiesFormat != nil {
					sortIPTags(pip.PublicIPAddressPropertiesFormat.IPTags)
				}

				assert.Equal(t, test.expectedPIP.PublicIPAddressPropertiesFormat, pip.PublicIPAddressPropertiesFormat, "TestCase[%d]: %s", i, test.desc)

			}
			assert.Equal(t, test.expectedCreateOrUpdateCount, createOrUpdateCount, "TestCase[%d]: %s", i, test.desc)
			assert.Equal(t, test.expectedError, err != nil, "TestCase[%d]: %s", i, test.desc)

			deletedCount := 0
			for _, deleted := range deletedPips {
				if deleted {
					deletedCount++
				}
			}
			assert.Equal(t, test.expectedDeleteCount, deletedCount, "TestCase[%d]: %s", i, test.desc)
		})
	}
}

func TestEnsurePublicIPExists(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testCases := []struct {
		desc                    string
		inputDNSLabel           string
		expectedID              string
		additionalAnnotations   map[string]string
		existingPIPs            []network.PublicIPAddress
		expectedPIP             *network.PublicIPAddress
		foundDNSLabelAnnotation bool
		isIPv6                  bool
		useSLB                  bool
		shouldPutPIP            bool
		expectedError           bool
	}{
		{
			desc:         "ensurePublicIPExists shall return existed PIP if there is any",
			existingPIPs: []network.PublicIPAddress{{Name: to.StringPtr("pip1")}},
			expectedPIP: &network.PublicIPAddress{
				Name: to.StringPtr("pip1"),
				ID: to.StringPtr("/subscriptions/subscription/resourceGroups/rg" +
					"/providers/Microsoft.Network/publicIPAddresses/pip1"),
			},
			shouldPutPIP: true,
		},
		{
			desc: "ensurePublicIPExists shall create a new pip if there is no existed pip",
			expectedID: "/subscriptions/subscription/resourceGroups/rg/providers/" +
				"Microsoft.Network/publicIPAddresses/pip1",
			shouldPutPIP: true,
		},
		{
			desc:                    "ensurePublicIPExists shall update existed PIP's dns label",
			inputDNSLabel:           "newdns",
			foundDNSLabelAnnotation: true,
			existingPIPs: []network.PublicIPAddress{{
				Name: to.StringPtr("pip1"),
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					DNSSettings: &network.PublicIPAddressDNSSettings{
						DomainNameLabel: to.StringPtr("previousdns"),
					},
				},
			}},
			expectedPIP: &network.PublicIPAddress{
				Name: to.StringPtr("pip1"),
				ID: to.StringPtr("/subscriptions/subscription/resourceGroups/rg" +
					"/providers/Microsoft.Network/publicIPAddresses/pip1"),
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					DNSSettings: &network.PublicIPAddressDNSSettings{
						DomainNameLabel: to.StringPtr("newdns"),
					},
					PublicIPAddressVersion: "IPv4",
				},
			},
			shouldPutPIP: true,
		},
		{
			desc:                    "ensurePublicIPExists shall delete DNS from PIP if DNS label is set empty",
			foundDNSLabelAnnotation: true,
			existingPIPs: []network.PublicIPAddress{{
				Name: to.StringPtr("pip1"),
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					DNSSettings: &network.PublicIPAddressDNSSettings{
						DomainNameLabel: to.StringPtr("previousdns"),
					},
				},
			}},
			expectedPIP: &network.PublicIPAddress{
				Name: to.StringPtr("pip1"),
				ID: to.StringPtr("/subscriptions/subscription/resourceGroups/rg" +
					"/providers/Microsoft.Network/publicIPAddresses/pip1"),
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					DNSSettings:            nil,
					PublicIPAddressVersion: "IPv4",
				},
			},
			shouldPutPIP: true,
		},
		{
			desc:                    "ensurePublicIPExists shall not delete DNS from PIP if DNS label annotation is not set",
			foundDNSLabelAnnotation: false,
			existingPIPs: []network.PublicIPAddress{{
				Name: to.StringPtr("pip1"),
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					DNSSettings: &network.PublicIPAddressDNSSettings{
						DomainNameLabel: to.StringPtr("previousdns"),
					},
				},
			}},
			expectedPIP: &network.PublicIPAddress{
				Name: to.StringPtr("pip1"),
				ID: to.StringPtr("/subscriptions/subscription/resourceGroups/rg" +
					"/providers/Microsoft.Network/publicIPAddresses/pip1"),
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					DNSSettings: &network.PublicIPAddressDNSSettings{
						DomainNameLabel: to.StringPtr("previousdns"),
					},
					PublicIPAddressVersion: "IPv4",
				},
			},
			shouldPutPIP: true,
		},
		{
			desc:                    "ensurePublicIPExists shall update existed PIP's dns label for IPv6",
			inputDNSLabel:           "newdns",
			foundDNSLabelAnnotation: true,
			isIPv6:                  true,
			existingPIPs: []network.PublicIPAddress{{
				Name: to.StringPtr("pip1"),
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					DNSSettings: &network.PublicIPAddressDNSSettings{
						DomainNameLabel: to.StringPtr("previousdns"),
					},
				},
			}},
			expectedPIP: &network.PublicIPAddress{
				Name: to.StringPtr("pip1"),
				ID: to.StringPtr("/subscriptions/subscription/resourceGroups/rg" +
					"/providers/Microsoft.Network/publicIPAddresses/pip1"),
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					DNSSettings: &network.PublicIPAddressDNSSettings{
						DomainNameLabel: to.StringPtr("newdns"),
					},
					PublicIPAllocationMethod: "Dynamic",
					PublicIPAddressVersion:   "IPv6",
				},
			},
			shouldPutPIP: true,
		},
		{
			desc:                    "ensurePublicIPExists shall report an conflict error if the DNS label is conflicted",
			inputDNSLabel:           "test",
			foundDNSLabelAnnotation: true,
			existingPIPs: []network.PublicIPAddress{{
				Name: to.StringPtr("pip1"),
				Tags: map[string]*string{consts.ServiceUsingDNSKey: to.StringPtr("test1")},
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					DNSSettings: &network.PublicIPAddressDNSSettings{
						DomainNameLabel: to.StringPtr("previousdns"),
					},
				},
			}},
			expectedError: true,
		},
		{
			desc:          "ensurePublicIPExists shall return the pip without calling PUT API if the tags are good",
			inputDNSLabel: "test",
			existingPIPs: []network.PublicIPAddress{
				{
					Name: to.StringPtr("pip1"),
					ID: to.StringPtr("/subscriptions/subscription/resourceGroups/rg" +
						"/providers/Microsoft.Network/publicIPAddresses/pip1"),
					Tags: map[string]*string{
						consts.ServiceUsingDNSKey: to.StringPtr("default/test1"),
						consts.ServiceTagKey:      to.StringPtr("default/test1"),
					},
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						DNSSettings: &network.PublicIPAddressDNSSettings{
							DomainNameLabel: to.StringPtr("test"),
						},
						PublicIPAllocationMethod: network.IPAllocationMethodStatic,
						PublicIPAddressVersion:   network.IPVersionIPv4,
					},
				},
			},
			expectedPIP: &network.PublicIPAddress{
				Name: to.StringPtr("pip1"),
				ID: to.StringPtr("/subscriptions/subscription/resourceGroups/rg" +
					"/providers/Microsoft.Network/publicIPAddresses/pip1"),
				Tags: map[string]*string{
					consts.ServiceUsingDNSKey: to.StringPtr("default/test1"),
					consts.ServiceTagKey:      to.StringPtr("default/test1"),
				},
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					DNSSettings: &network.PublicIPAddressDNSSettings{
						DomainNameLabel: to.StringPtr("test"),
					},
					PublicIPAllocationMethod: network.IPAllocationMethodStatic,
					PublicIPAddressVersion:   network.IPVersionIPv4,
				},
			},
		},
		{
			desc: "ensurePublicIPExists shall tag the service name to the pip correctly",
			existingPIPs: []network.PublicIPAddress{
				{Name: to.StringPtr("pip1")},
			},
			expectedPIP: &network.PublicIPAddress{
				Name: to.StringPtr("pip1"),
				ID: to.StringPtr("/subscriptions/subscription/resourceGroups/rg" +
					"/providers/Microsoft.Network/publicIPAddresses/pip1"),
			},
			shouldPutPIP: true,
		},
		{
			desc:   "ensurePublicIPExists shall not call the PUT API for IPV6 pip if it is not necessary",
			isIPv6: true,
			useSLB: true,
			existingPIPs: []network.PublicIPAddress{
				{
					Name: to.StringPtr("pip1"),
					PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
						PublicIPAddressVersion:   network.IPVersionIPv6,
						PublicIPAllocationMethod: network.IPAllocationMethodStatic,
					},
				},
			},
			expectedPIP: &network.PublicIPAddress{
				Name: to.StringPtr("pip1"),
				ID: to.StringPtr("/subscriptions/subscription/resourceGroups/rg" +
					"/providers/Microsoft.Network/publicIPAddresses/pip1"),
				PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
					PublicIPAddressVersion:   network.IPVersionIPv6,
					PublicIPAllocationMethod: network.IPAllocationMethodDynamic,
				},
			},
			shouldPutPIP: true,
		},
	}

	for i, test := range testCases {
		t.Run(test.desc, func(t *testing.T) {
			az := GetTestCloud(ctrl)
			if test.useSLB {
				az.LoadBalancerSku = consts.LoadBalancerSkuStandard
			}

			service := getTestService("test1", v1.ProtocolTCP, nil, test.isIPv6, 80)
			service.ObjectMeta.Annotations = test.additionalAnnotations
			mockPIPsClient := az.PublicIPAddressesClient.(*mockpublicipclient.MockInterface)
			if test.shouldPutPIP {
				mockPIPsClient.EXPECT().CreateOrUpdate(gomock.Any(), "rg", gomock.Any(), gomock.Any()).Return(nil)
			}
			mockPIPsClient.EXPECT().Get(gomock.Any(), "rg", "pip1", gomock.Any()).DoAndReturn(func(ctx context.Context, resourceGroupName string, publicIPAddressName string, expand string) (network.PublicIPAddress, *retry.Error) {
				var basicPIP network.PublicIPAddress
				if len(test.existingPIPs) == 0 {
					basicPIP = network.PublicIPAddress{
						Name: to.StringPtr("pip1"),
					}
				} else {
					basicPIP = test.existingPIPs[0]
				}

				basicPIP.ID = to.StringPtr("/subscriptions/subscription/resourceGroups/rg" +
					"/providers/Microsoft.Network/publicIPAddresses/pip1")

				if basicPIP.PublicIPAddressPropertiesFormat == nil {
					return basicPIP, nil
				}

				if test.foundDNSLabelAnnotation {
					if test.inputDNSLabel != "" {
						basicPIP.DNSSettings.DomainNameLabel = &test.inputDNSLabel
					} else {
						basicPIP.DNSSettings = nil
					}
				}

				if test.isIPv6 {
					basicPIP.PublicIPAddressPropertiesFormat.PublicIPAddressVersion = "IPv6"
					basicPIP.PublicIPAllocationMethod = "Dynamic"
				} else {
					basicPIP.PublicIPAddressPropertiesFormat.PublicIPAddressVersion = "IPv4"
				}

				return basicPIP, nil
			}).AnyTimes()

			pip, err := az.ensurePublicIPExists(&service, "pip1", test.inputDNSLabel, "", false, test.foundDNSLabelAnnotation)
			assert.Equal(t, test.expectedError, err != nil, "TestCase[%d]: %s, encountered unexpected error: %v", i, test.desc, err)
			if test.expectedID != "" {
				assert.Equal(t, test.expectedID, to.String(pip.ID), "TestCase[%d]: %s", i, test.desc)
			} else {
				assert.Equal(t, test.expectedPIP, pip, "TestCase[%d]: %s", i, test.desc)
			}
		})
	}
}
func TestEnsurePublicIPExistsWithExtendedLocation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	az := GetTestCloudWithExtendedLocation(ctrl)
	az.LoadBalancerSku = consts.LoadBalancerSkuStandard
	service := getTestService("test1", v1.ProtocolTCP, nil, false, 80)

	exLocName := "microsoftlosangeles1"
	expectedPIP := &network.PublicIPAddress{
		Name:     to.StringPtr("pip1"),
		Location: &az.location,
		ExtendedLocation: &network.ExtendedLocation{
			Name: to.StringPtr("microsoftlosangeles1"),
			Type: network.ExtendedLocationTypesEdgeZone,
		},
		PublicIPAddressPropertiesFormat: &network.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: "Static",
			PublicIPAddressVersion:   "IPv4",
			ProvisioningState:        "",
		},
		Tags: map[string]*string{
			"service":                 to.StringPtr("default/test1"),
			"kubernetes-cluster-name": to.StringPtr(""),
		},
	}
	mockPIPsClient := az.PublicIPAddressesClient.(*mockpublicipclient.MockInterface)
	first := mockPIPsClient.EXPECT().Get(gomock.Any(), "rg", "pip1", gomock.Any()).Return(network.PublicIPAddress{}, &retry.Error{
		HTTPStatusCode: 404,
	})
	mockPIPsClient.EXPECT().Get(gomock.Any(), "rg", "pip1", gomock.Any()).Return(*expectedPIP, nil).After(first)

	mockPIPsClient.EXPECT().CreateOrUpdate(gomock.Any(), "rg", "pip1", gomock.Any()).
		DoAndReturn(func(ctx context.Context, resourceGroupName string, publicIPAddressName string, publicIPAddressParameters network.PublicIPAddress) *retry.Error {
			assert.NotNil(t, publicIPAddressParameters)
			assert.NotNil(t, publicIPAddressParameters.ExtendedLocation)
			assert.Equal(t, *publicIPAddressParameters.ExtendedLocation.Name, exLocName)
			assert.Equal(t, publicIPAddressParameters.ExtendedLocation.Type, network.ExtendedLocationTypesEdgeZone)
			// Edge zones don't support availability zones.
			assert.Nil(t, publicIPAddressParameters.Zones)
			return nil
		}).Times(1)
	pip, err := az.ensurePublicIPExists(&service, "pip1", "", "", false, false)
	assert.NotNil(t, pip, "ensurePublicIPExists shall create a new pip"+
		"with extendedLocation if there is no existed pip")
	assert.Nil(t, err, "ensurePublicIPExists should create a new pip without errors.")
}

func TestShouldUpdateLoadBalancer(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testCases := []struct {
		desc                   string
		lbHasDeletionTimestamp bool
		existsLb               bool
		expectedOutput         bool
	}{
		{
			desc:                   "should update a load balancer that does not have a deletion timestamp and exists in Azure",
			lbHasDeletionTimestamp: false,
			existsLb:               true,
			expectedOutput:         true,
		},
		{
			desc:                   "should not update a load balancer that is being deleted / already deleted in K8s",
			lbHasDeletionTimestamp: true,
			existsLb:               true,
			expectedOutput:         false,
		},
		{
			desc:                   "should not update a load balancer that does not exist in Azure",
			lbHasDeletionTimestamp: false,
			existsLb:               false,
			expectedOutput:         false,
		},
		{
			desc:                   "should not update a load balancer that has a deletion timestamp and does not exist in Azure",
			lbHasDeletionTimestamp: true,
			existsLb:               false,
			expectedOutput:         false,
		},
	}

	for i, test := range testCases {
		az := GetTestCloud(ctrl)
		service := getTestService("test1", v1.ProtocolTCP, nil, false, 80)
		setMockPublicIPs(az, ctrl, 1)
		mockLBsClient := mockloadbalancerclient.NewMockInterface(ctrl)
		az.LoadBalancerClient = mockLBsClient
		mockLBsClient.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		if test.lbHasDeletionTimestamp {
			service.ObjectMeta.DeletionTimestamp = &metav1.Time{Time: time.Now()}
		}
		if test.existsLb {
			lb := network.LoadBalancer{
				Name: to.StringPtr("vmas"),
				LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
					FrontendIPConfigurations: &[]network.FrontendIPConfiguration{
						{
							Name: to.StringPtr("atest1"),
							FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
								PublicIPAddress: &network.PublicIPAddress{ID: to.StringPtr("testCluster-aservice1")},
							},
						},
					},
				},
			}
			err := az.LoadBalancerClient.CreateOrUpdate(context.TODO(), "rg", *lb.Name, lb, "")
			if err != nil {
				t.Fatalf("TestCase[%d] meets unexpected error: %v", i, err)
			}
			mockLBsClient.EXPECT().List(gomock.Any(), "rg").Return([]network.LoadBalancer{lb}, nil)
		} else {
			mockLBsClient.EXPECT().List(gomock.Any(), "rg").Return(nil, nil)
		}

		existingNodes := []*v1.Node{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "vmas-1"},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "vmas-2",
					Labels: map[string]string{consts.NodeLabelRole: "master"},
				},
			},
		}

		shouldUpdateLoadBalancer := az.shouldUpdateLoadBalancer(testClusterName, &service, existingNodes)
		assert.Equal(t, test.expectedOutput, shouldUpdateLoadBalancer, "TestCase[%d]: %s", i, test.desc)
	}
}

func TestIsBackendPoolPreConfigured(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testCases := []struct {
		desc                                      string
		preConfiguredBackendPoolLoadBalancerTypes string
		isInternalService                         bool
		expectedOutput                            bool
	}{
		{
			desc: "should return true when preConfiguredBackendPoolLoadBalancerTypes is both for any case",
			preConfiguredBackendPoolLoadBalancerTypes: "all",
			isInternalService:                         true,
			expectedOutput:                            true,
		},
		{
			desc: "should return true when preConfiguredBackendPoolLoadBalancerTypes is both for any case",
			preConfiguredBackendPoolLoadBalancerTypes: "all",
			isInternalService:                         false,
			expectedOutput:                            true,
		},
		{
			desc: "should return true when preConfiguredBackendPoolLoadBalancerTypes is external when creating external lb",
			preConfiguredBackendPoolLoadBalancerTypes: "external",
			isInternalService:                         false,
			expectedOutput:                            true,
		},
		{
			desc: "should return false when preConfiguredBackendPoolLoadBalancerTypes is external when creating internal lb",
			preConfiguredBackendPoolLoadBalancerTypes: "external",
			isInternalService:                         true,
			expectedOutput:                            false,
		},
		{
			desc: "should return false when preConfiguredBackendPoolLoadBalancerTypes is internal when creating external lb",
			preConfiguredBackendPoolLoadBalancerTypes: "internal",
			isInternalService:                         false,
			expectedOutput:                            false,
		},
		{
			desc: "should return true when preConfiguredBackendPoolLoadBalancerTypes is internal when creating internal lb",
			preConfiguredBackendPoolLoadBalancerTypes: "internal",
			isInternalService:                         true,
			expectedOutput:                            true,
		},
		{
			desc: "should return false when preConfiguredBackendPoolLoadBalancerTypes is empty for any case",
			preConfiguredBackendPoolLoadBalancerTypes: "",
			isInternalService:                         true,
			expectedOutput:                            false,
		},
		{
			desc: "should return false when preConfiguredBackendPoolLoadBalancerTypes is empty for any case",
			preConfiguredBackendPoolLoadBalancerTypes: "",
			isInternalService:                         false,
			expectedOutput:                            false,
		},
	}

	for i, test := range testCases {
		az := GetTestCloud(ctrl)
		az.Config.PreConfiguredBackendPoolLoadBalancerTypes = test.preConfiguredBackendPoolLoadBalancerTypes
		var service v1.Service
		if test.isInternalService {
			service = getInternalTestService("test", 80)
		} else {
			service = getTestService("test", v1.ProtocolTCP, nil, false, 80)
		}

		isPreConfigured := az.isBackendPoolPreConfigured(&service)
		assert.Equal(t, test.expectedOutput, isPreConfigured, "TestCase[%d]: %s", i, test.desc)
	}
}

func TestParsePIPServiceTag(t *testing.T) {
	tags := []*string{
		to.StringPtr("ns1/svc1,ns2/svc2"),
		to.StringPtr(" ns1/svc1, ns2/svc2 "),
		to.StringPtr("ns1/svc1,"),
		to.StringPtr(""),
		nil,
	}
	expectedNames := [][]string{
		{"ns1/svc1", "ns2/svc2"},
		{"ns1/svc1", "ns2/svc2"},
		{"ns1/svc1"},
		{},
		{},
	}

	for i, tag := range tags {
		names := parsePIPServiceTag(tag)
		assert.Equal(t, expectedNames[i], names)
	}
}

func TestBindServicesToPIP(t *testing.T) {
	pips := []*network.PublicIPAddress{
		{Tags: nil},
		{Tags: map[string]*string{}},
		{Tags: map[string]*string{consts.ServiceTagKey: to.StringPtr("ns1/svc1")}},
		{Tags: map[string]*string{consts.ServiceTagKey: to.StringPtr("ns1/svc1,ns2/svc2")}},
		{Tags: map[string]*string{consts.ServiceTagKey: to.StringPtr("ns2/svc2,ns3/svc3")}},
	}
	serviceNames := []string{"ns2/svc2", "ns3/svc3"}
	expectedTags := []map[string]*string{
		{consts.ServiceTagKey: to.StringPtr("ns2/svc2,ns3/svc3")},
		{consts.ServiceTagKey: to.StringPtr("ns2/svc2,ns3/svc3")},
		{consts.ServiceTagKey: to.StringPtr("ns1/svc1,ns2/svc2,ns3/svc3")},
		{consts.ServiceTagKey: to.StringPtr("ns1/svc1,ns2/svc2,ns3/svc3")},
		{consts.ServiceTagKey: to.StringPtr("ns2/svc2,ns3/svc3")},
	}

	flags := []bool{true, true, true, true, false}

	for i, pip := range pips {
		addedNew, _ := bindServicesToPIP(pip, serviceNames, false)
		assert.Equal(t, expectedTags[i], pip.Tags)
		assert.Equal(t, flags[i], addedNew)
	}
}

func TestUnbindServiceFromPIP(t *testing.T) {
	pips := []*network.PublicIPAddress{
		{Tags: nil},
		{Tags: map[string]*string{consts.ServiceTagKey: to.StringPtr("")}},
		{Tags: map[string]*string{consts.ServiceTagKey: to.StringPtr("ns1/svc1")}},
		{Tags: map[string]*string{consts.ServiceTagKey: to.StringPtr("ns1/svc1,ns2/svc2")}},
	}
	serviceName := "ns2/svc2"
	service := getTestService(serviceName, v1.ProtocolTCP, nil, false, 80)
	service.Spec.LoadBalancerIP = "1.2.3.4"
	expectedTags := []map[string]*string{
		nil,
		{consts.ServiceTagKey: to.StringPtr("")},
		{consts.ServiceTagKey: to.StringPtr("ns1/svc1")},
		{consts.ServiceTagKey: to.StringPtr("ns1/svc1")},
	}

	for i, pip := range pips {
		_ = unbindServiceFromPIP(pip, &service, serviceName, "")
		assert.Equal(t, expectedTags[i], pip.Tags)
	}
}

func TestIsFrontendIPConfigIsUnsafeToDelete(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	service := getTestService("service1", v1.ProtocolTCP, nil, false, 80)
	az := GetTestCloud(ctrl)
	fipID := to.StringPtr("fip")

	testCases := []struct {
		desc       string
		existingLB *network.LoadBalancer
		unsafe     bool
	}{
		{
			desc: "isFrontendIPConfigUnsafeToDelete should return true if there is a " +
				"loadBalancing rule from other service referencing the frontend IP config",
			existingLB: &network.LoadBalancer{
				Name: to.StringPtr("lb"),
				LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
					LoadBalancingRules: &[]network.LoadBalancingRule{
						{
							Name: to.StringPtr("aservice2-rule"),
							LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
								FrontendIPConfiguration: &network.SubResource{ID: to.StringPtr("fip")},
							},
						},
					},
				},
			},
			unsafe: true,
		},
		{
			desc: "isFrontendIPConfigUnsafeToDelete should return false if there is a " +
				"loadBalancing rule from this service referencing the frontend IP config",
			existingLB: &network.LoadBalancer{
				Name: to.StringPtr("lb"),
				LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
					OutboundRules: &[]network.OutboundRule{
						{
							Name: to.StringPtr("aservice1-rule"),
							OutboundRulePropertiesFormat: &network.OutboundRulePropertiesFormat{
								FrontendIPConfigurations: &[]network.SubResource{
									{ID: to.StringPtr("fip")},
								},
							},
						},
					},
				},
			},
			unsafe: true,
		},
		{
			desc: "isFrontendIPConfigUnsafeToDelete should return false if there is a " +
				"outbound rule referencing the frontend IP config",
			existingLB: &network.LoadBalancer{
				Name: to.StringPtr("lb"),
				LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
					LoadBalancingRules: &[]network.LoadBalancingRule{
						{
							Name: to.StringPtr("aservice1-rule"),
							LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
								FrontendIPConfiguration: &network.SubResource{ID: to.StringPtr("fip")},
							},
						},
					},
				},
			},
		},
		{
			desc: "isFrontendIPConfigUnsafeToDelete should return true if there is a " +
				"inbound NAT rule referencing the frontend IP config",
			existingLB: &network.LoadBalancer{
				Name: to.StringPtr("lb"),
				LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
					InboundNatRules: &[]network.InboundNatRule{
						{
							Name: to.StringPtr("aservice2-rule"),
							InboundNatRulePropertiesFormat: &network.InboundNatRulePropertiesFormat{
								FrontendIPConfiguration: &network.SubResource{ID: to.StringPtr("fip")},
							},
						},
					},
				},
			},
			unsafe: true,
		},
		{
			desc: "isFrontendIPConfigUnsafeToDelete should return true if there is a " +
				"inbound NAT pool referencing the frontend IP config",
			existingLB: &network.LoadBalancer{
				Name: to.StringPtr("lb"),
				LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
					InboundNatPools: &[]network.InboundNatPool{
						{
							Name: to.StringPtr("aservice2-rule"),
							InboundNatPoolPropertiesFormat: &network.InboundNatPoolPropertiesFormat{
								FrontendIPConfiguration: &network.SubResource{ID: to.StringPtr("fip")},
							},
						},
					},
				},
			},
			unsafe: true,
		},
	}

	for _, testCase := range testCases {
		unsafe, _ := az.isFrontendIPConfigUnsafeToDelete(testCase.existingLB, &service, fipID)
		assert.Equal(t, testCase.unsafe, unsafe, testCase.desc)
	}
}

func TestCheckLoadBalancerResourcesConflicted(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	service := getTestService("service1", v1.ProtocolTCP, nil, false, 80)
	az := GetTestCloud(ctrl)
	fipID := "fip"

	testCases := []struct {
		desc        string
		existingLB  *network.LoadBalancer
		expectedErr bool
	}{
		{
			desc: "checkLoadBalancerResourcesConflicts should report the conflict error if " +
				"there is a conflicted loadBalancing rule",
			existingLB: &network.LoadBalancer{
				Name: to.StringPtr("lb"),
				LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
					LoadBalancingRules: &[]network.LoadBalancingRule{
						{
							Name: to.StringPtr("aservice2-rule"),
							LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
								FrontendIPConfiguration: &network.SubResource{ID: to.StringPtr("fip")},
								FrontendPort:            to.Int32Ptr(80),
								Protocol:                network.TransportProtocol(v1.ProtocolTCP),
							},
						},
					},
				},
			},
			expectedErr: true,
		},
		{
			desc: "checkLoadBalancerResourcesConflicts should report the conflict error if " +
				"there is a conflicted inbound NAT rule",
			existingLB: &network.LoadBalancer{
				Name: to.StringPtr("lb"),
				LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
					InboundNatRules: &[]network.InboundNatRule{
						{
							Name: to.StringPtr("aservice1-rule"),
							InboundNatRulePropertiesFormat: &network.InboundNatRulePropertiesFormat{
								FrontendIPConfiguration: &network.SubResource{ID: to.StringPtr("fip")},
								FrontendPort:            to.Int32Ptr(80),
								Protocol:                network.TransportProtocol(v1.ProtocolTCP),
							},
						},
					},
				},
			},
			expectedErr: true,
		},
		{
			desc: "checkLoadBalancerResourcesConflicts should report the conflict error if " +
				"there is a conflicted inbound NAT pool",
			existingLB: &network.LoadBalancer{
				Name: to.StringPtr("lb"),
				LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
					InboundNatPools: &[]network.InboundNatPool{
						{
							Name: to.StringPtr("aservice1-rule"),
							InboundNatPoolPropertiesFormat: &network.InboundNatPoolPropertiesFormat{
								FrontendIPConfiguration: &network.SubResource{ID: to.StringPtr("fip")},
								FrontendPortRangeStart:  to.Int32Ptr(80),
								FrontendPortRangeEnd:    to.Int32Ptr(90),
								Protocol:                network.TransportProtocol(v1.ProtocolTCP),
							},
						},
					},
				},
			},
			expectedErr: true,
		},
		{
			desc: "checkLoadBalancerResourcesConflicts should not report the conflict error if there " +
				"is no conflicted loadBalancer resources",
			existingLB: &network.LoadBalancer{
				Name: to.StringPtr("lb"),
				LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
					LoadBalancingRules: &[]network.LoadBalancingRule{
						{
							Name: to.StringPtr("aservice2-rule"),
							LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
								FrontendIPConfiguration: &network.SubResource{ID: to.StringPtr("fip")},
								FrontendPort:            to.Int32Ptr(90),
								Protocol:                network.TransportProtocol(v1.ProtocolTCP),
							},
						},
					},
					InboundNatRules: &[]network.InboundNatRule{
						{
							Name: to.StringPtr("aservice1-rule"),
							InboundNatRulePropertiesFormat: &network.InboundNatRulePropertiesFormat{
								FrontendIPConfiguration: &network.SubResource{ID: to.StringPtr("fip")},
								FrontendPort:            to.Int32Ptr(90),
								Protocol:                network.TransportProtocol(v1.ProtocolTCP),
							},
						},
					},
					InboundNatPools: &[]network.InboundNatPool{
						{
							Name: to.StringPtr("aservice1-rule"),
							InboundNatPoolPropertiesFormat: &network.InboundNatPoolPropertiesFormat{
								FrontendIPConfiguration: &network.SubResource{ID: to.StringPtr("fip")},
								FrontendPortRangeStart:  to.Int32Ptr(800),
								FrontendPortRangeEnd:    to.Int32Ptr(900),
								Protocol:                network.TransportProtocol(v1.ProtocolTCP),
							},
						},
					},
				},
			},
		},
	}

	for _, testCase := range testCases {
		err := az.checkLoadBalancerResourcesConflicts(testCase.existingLB, fipID, &service)
		assert.Equal(t, testCase.expectedErr, err != nil, testCase.desc)
	}
}

func TestCleanupVMSetFromBackendPoolByCondition(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	cloud := GetTestCloud(ctrl)
	cloud.LoadBalancerSku = consts.LoadBalancerSkuStandard
	cloud.EnableMultipleStandardLoadBalancers = true
	cloud.PrimaryAvailabilitySetName = "agentpool1-availabilitySet-00000000"
	clusterName := "testCluster"
	service := getTestService("test", v1.ProtocolTCP, nil, false, 80)
	lb := buildDefaultTestLB("testCluster", []string{
		"/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/networkInterfaces/k8s-agentpool1-00000000-nic-1/ipConfigurations/ipconfig1",
		"/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/networkInterfaces/k8s-agentpool2-00000000-nic-1/ipConfigurations/ipconfig1",
	})
	existingVMForAS1 := buildDefaultTestVirtualMachine("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Compute/availabilitySets/agentpool1-availabilitySet-00000000", []string{"/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/networkInterfaces/k8s-agentpool1-00000000-nic-1"})
	existingVMForAS2 := buildDefaultTestVirtualMachine("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Compute/availabilitySets/agentpool2-availabilitySet-00000000", []string{"/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/networkInterfaces/k8s-agentpool2-00000000-nic-1"})
	existingNICForAS1 := buildDefaultTestInterface(true, []string{"/subscriptions/sub/resourceGroups/gh/providers/Microsoft.Network/loadBalancers/testCluster/backendAddressPools/testCluster"})
	existingNICForAS1.VirtualMachine = &network.SubResource{
		ID: to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/k8s-agentpool1-00000000-1"),
	}
	existingNICForAS2 := buildDefaultTestInterface(true, []string{"/subscriptions/sub/resourceGroups/gh/providers/Microsoft.Network/loadBalancers/testCluster/backendAddressPools/testCluster"})
	existingNICForAS2.VirtualMachine = &network.SubResource{
		ID: to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/k8s-agentpool2-00000000-1"),
	}
	mockVMClient := mockvmclient.NewMockInterface(ctrl)
	mockVMClient.EXPECT().Get(gomock.Any(), cloud.ResourceGroup, "k8s-agentpool1-00000000-1", gomock.Any()).Return(existingVMForAS1, nil)
	mockVMClient.EXPECT().Get(gomock.Any(), cloud.ResourceGroup, "k8s-agentpool2-00000000-1", gomock.Any()).Return(existingVMForAS2, nil)
	cloud.VirtualMachinesClient = mockVMClient
	mockNICClient := mockinterfaceclient.NewMockInterface(ctrl)
	mockNICClient.EXPECT().Get(gomock.Any(), "rg", "k8s-agentpool1-00000000-nic-1", gomock.Any()).Return(existingNICForAS1, nil)
	mockNICClient.EXPECT().Get(gomock.Any(), "rg", "k8s-agentpool2-00000000-nic-1", gomock.Any()).Return(existingNICForAS2, nil).Times(3)
	mockNICClient.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	cloud.InterfacesClient = mockNICClient

	expectedLB := network.LoadBalancer{
		Name: to.StringPtr("testCluster"),
		LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
			BackendAddressPools: &[]network.BackendAddressPool{
				{
					Name: to.StringPtr("testCluster"),
					BackendAddressPoolPropertiesFormat: &network.BackendAddressPoolPropertiesFormat{
						BackendIPConfigurations: &[]network.InterfaceIPConfiguration{
							{
								ID: to.StringPtr("/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/networkInterfaces/k8s-agentpool1-00000000-nic-1/ipConfigurations/ipconfig1"),
							},
						},
					},
				},
			},
		},
	}

	shouldRemoveVMSetFromSLB := func(vmSetName string) bool {
		return !strings.EqualFold(vmSetName, cloud.VMSet.GetPrimaryVMSetName()) && vmSetName != ""
	}
	cleanedLB, err := cloud.cleanupVMSetFromBackendPoolByCondition(&lb, &service, clusterName, shouldRemoveVMSetFromSLB)
	assert.NoError(t, err)
	assert.Equal(t, expectedLB, *cleanedLB)
}

func buildDefaultTestLB(name string, backendIPConfigs []string) network.LoadBalancer {
	expectedLB := network.LoadBalancer{
		Name: to.StringPtr(name),
		LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
			BackendAddressPools: &[]network.BackendAddressPool{
				{
					Name: to.StringPtr(name),
					BackendAddressPoolPropertiesFormat: &network.BackendAddressPoolPropertiesFormat{
						BackendIPConfigurations: &[]network.InterfaceIPConfiguration{},
					},
				},
			},
		},
	}
	backendIPConfigurations := make([]network.InterfaceIPConfiguration, 0)
	for _, ipConfig := range backendIPConfigs {
		backendIPConfigurations = append(backendIPConfigurations, network.InterfaceIPConfiguration{ID: to.StringPtr(ipConfig)})
	}
	(*expectedLB.BackendAddressPools)[0].BackendIPConfigurations = &backendIPConfigurations
	return expectedLB
}

func TestEnsurePIPTagged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cloud := GetTestCloud(ctrl)
	cloud.Tags = "a=x,y=z"

	service := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				consts.ServiceAnnotationAzurePIPTags: "a=b,c=d,e=,=f,ghi",
			},
		},
	}
	pip := network.PublicIPAddress{
		Tags: map[string]*string{
			consts.ClusterNameKey: to.StringPtr("testCluster"),
			consts.ServiceTagKey:  to.StringPtr("default/svc1,default/svc2"),
			"foo":                 to.StringPtr("bar"),
			"a":                   to.StringPtr("j"),
			"m":                   to.StringPtr("n"),
		},
	}

	t.Run("ensurePIPTagged should ensure the pip is tagged as configured", func(t *testing.T) {
		expectedPIP := network.PublicIPAddress{
			Tags: map[string]*string{
				consts.ClusterNameKey: to.StringPtr("testCluster"),
				consts.ServiceTagKey:  to.StringPtr("default/svc1,default/svc2"),
				"foo":                 to.StringPtr("bar"),
				"a":                   to.StringPtr("b"),
				"c":                   to.StringPtr("d"),
				"y":                   to.StringPtr("z"),
				"m":                   to.StringPtr("n"),
			},
		}
		changed := cloud.ensurePIPTagged(&service, &pip)
		assert.True(t, changed)
		assert.Equal(t, expectedPIP, pip)
	})

	t.Run("ensurePIPTagged should delete the old tags if tht SystemTags is set", func(t *testing.T) {
		cloud.SystemTags = "a,foo"
		expectedPIP := network.PublicIPAddress{
			Tags: map[string]*string{
				consts.ClusterNameKey: to.StringPtr("testCluster"),
				consts.ServiceTagKey:  to.StringPtr("default/svc1,default/svc2"),
				"foo":                 to.StringPtr("bar"),
				"a":                   to.StringPtr("b"),
				"c":                   to.StringPtr("d"),
				"y":                   to.StringPtr("z"),
			},
		}
		changed := cloud.ensurePIPTagged(&service, &pip)
		assert.True(t, changed)
		assert.Equal(t, expectedPIP, pip)
	})
}

func TestEnsureLoadBalancerTagged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	for _, tc := range []struct {
		description               string
		existedTags, expectedTags map[string]*string
		newTags, systemTags       string
		expectedChanged           bool
	}{
		{
			description:     "ensureLoadBalancerTagged should not delete the old tags if SystemTags is not specified",
			existedTags:     map[string]*string{"a": to.StringPtr("b")},
			newTags:         "c=d",
			expectedTags:    map[string]*string{"a": to.StringPtr("b"), "c": to.StringPtr("d")},
			expectedChanged: true,
		},
		{
			description:     "ensureLoadBalancerTagged should delete the old tags if SystemTags is specified",
			existedTags:     map[string]*string{"a": to.StringPtr("b"), "c": to.StringPtr("d"), "h": to.StringPtr("i")},
			newTags:         "c=e,f=g",
			systemTags:      "a,x,y,z",
			expectedTags:    map[string]*string{"a": to.StringPtr("b"), "c": to.StringPtr("e"), "f": to.StringPtr("g")},
			expectedChanged: true,
		},
	} {
		t.Run(tc.description, func(t *testing.T) {
			cloud := GetTestCloud(ctrl)
			cloud.Tags = tc.newTags
			cloud.SystemTags = tc.systemTags
			lb := &network.LoadBalancer{Tags: tc.existedTags}

			changed := cloud.ensureLoadBalancerTagged(lb)
			assert.Equal(t, tc.expectedChanged, changed)
			assert.Equal(t, tc.expectedTags, lb.Tags)
		})
	}
}

func TestShouldChangeLoadBalancer(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	cloud := GetTestCloud(ctrl)
	cloud.LoadBalancerSku = consts.LoadBalancerSkuBasic

	t.Run("shouldChangeLoadBalancer should return true if the mode is different from the current vm set", func(t *testing.T) {
		annotations := map[string]string{
			consts.ServiceAnnotationLoadBalancerMode: "as2",
		}
		service := getTestService("service1", v1.ProtocolTCP, annotations, false, 80)
		res := cloud.shouldChangeLoadBalancer(&service, "as1", "testCluster")
		assert.True(t, res)
	})

	t.Run("shouldChangeLoadBalancer should return false if the current lb is the primary slb and the vmSet selected by annotation is sharing the primary slb", func(t *testing.T) {
		cloud.LoadBalancerSku = consts.LoadBalancerSkuStandard
		cloud.EnableMultipleStandardLoadBalancers = true
		cloud.NodePoolsWithoutDedicatedSLB = "vmss-1,vmss2"

		annotations := map[string]string{
			consts.ServiceAnnotationLoadBalancerMode: "vmss-1",
		}
		service := getTestService("service1", v1.ProtocolTCP, annotations, false, 80)
		res := cloud.shouldChangeLoadBalancer(&service, "testCluster-internal", "testCluster")
		assert.False(t, res)
	})
}

func TestRemoveFrontendIPConfigurationFromLoadBalancerDelete(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	t.Run("removeFrontendIPConfigurationFromLoadBalancer should remove the unwanted frontend IP configuration and delete the orphaned LB", func(t *testing.T) {
		fip := &network.FrontendIPConfiguration{Name: to.StringPtr("testCluster")}
		service := getTestService("svc1", v1.ProtocolTCP, nil, false, 80)
		lb := getTestLoadBalancer(to.StringPtr("lb"), to.StringPtr("rg"), to.StringPtr("testCluster"), to.StringPtr("testCluster"), service, "standard")
		bid := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/networkInterfaces/k8s-agentpool1-00000000-nic-0/ipConfigurations/ipconfig1"
		lb.BackendAddressPools = &[]network.BackendAddressPool{
			{
				Name: to.StringPtr("testCluster"),
				BackendAddressPoolPropertiesFormat: &network.BackendAddressPoolPropertiesFormat{
					BackendIPConfigurations: &[]network.InterfaceIPConfiguration{
						{ID: to.StringPtr(bid)},
					},
				},
			},
		}
		cloud := GetTestCloud(ctrl)
		mockLBClient := cloud.LoadBalancerClient.(*mockloadbalancerclient.MockInterface)
		mockLBClient.EXPECT().Delete(gomock.Any(), "rg", "lb").Return(nil)
		err := cloud.removeFrontendIPConfigurationFromLoadBalancer(&lb, fip, "testCluster", &service)
		assert.NoError(t, err)
	})
}

func TestRemoveFrontendIPConfigurationFromLoadBalancerUpdate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	t.Run("removeFrontendIPConfigurationFromLoadBalancer should remove the unwanted frontend IP configuration and update the LB if there are remaining frontend IP configurations", func(t *testing.T) {
		fip := &network.FrontendIPConfiguration{Name: to.StringPtr("testCluster")}
		service := getTestService("svc1", v1.ProtocolTCP, nil, false, 80)
		lb := getTestLoadBalancer(to.StringPtr("lb"), to.StringPtr("rg"), to.StringPtr("testCluster"), to.StringPtr("testCluster"), service, "standard")
		*lb.FrontendIPConfigurations = append(*lb.FrontendIPConfigurations, network.FrontendIPConfiguration{Name: to.StringPtr("fip1")})
		cloud := GetTestCloud(ctrl)
		mockLBClient := cloud.LoadBalancerClient.(*mockloadbalancerclient.MockInterface)
		mockLBClient.EXPECT().CreateOrUpdate(gomock.Any(), "rg", "lb", gomock.Any(), gomock.Any()).Return(nil)
		err := cloud.removeFrontendIPConfigurationFromLoadBalancer(&lb, fip, "testCluster", &service)
		assert.NoError(t, err)
	})
}

func TestCleanOrphanedLoadBalancerLBInUseByVMSS(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	t.Run("cleanOrphanedLoadBalancer should retry deleting lb when meeting LoadBalancerInUseByVirtualMachineScaleSet", func(t *testing.T) {
		cloud := GetTestCloud(ctrl)
		vmss, err := newScaleSet(cloud)
		assert.NoError(t, err)
		cloud.VMSet = vmss
		cloud.LoadBalancerSku = consts.LoadBalancerSkuStandard

		mockLBClient := cloud.LoadBalancerClient.(*mockloadbalancerclient.MockInterface)
		mockLBClient.EXPECT().Delete(gomock.Any(), "rg", "test").Return(&retry.Error{RawError: errors.New(LBInUseRawError)})
		mockLBClient.EXPECT().Delete(gomock.Any(), "rg", "test").Return(nil)

		expectedVMSS := buildTestVMSSWithLB(testVMSSName, "vmss-vm-", []string{testLBBackendpoolID0}, false)
		mockVMSSClient := cloud.VirtualMachineScaleSetsClient.(*mockvmssclient.MockInterface)
		mockVMSSClient.EXPECT().List(gomock.Any(), "rg").Return([]compute.VirtualMachineScaleSet{expectedVMSS}, nil)

		service := getTestService("test", v1.ProtocolTCP, nil, false, 80)
		lb := getTestLoadBalancer(to.StringPtr("test"), to.StringPtr("rg"), to.StringPtr("test"), to.StringPtr("test"), service, consts.LoadBalancerSkuStandard)
		(*lb.BackendAddressPools)[0].ID = to.StringPtr(testLBBackendpoolID0)

		err = cloud.cleanOrphanedLoadBalancer(&lb, &service, "test")
		assert.NoError(t, err)
	})
}

func TestReconcileZonesForFrontendIPConfigs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	for _, tc := range []struct {
		description               string
		service                   v1.Service
		existingFrontendIPConfigs []network.FrontendIPConfiguration
		existingPIP               network.PublicIPAddress
		getPIPError               *retry.Error
		regionZonesMap            map[string][]string
		expectedZones             *[]string
		expectedDirty             bool
	}{
		{
			description:               "reconcileFrontendIPConfigs should reconcile the zones for the new fip config",
			service:                   getTestService("test", v1.ProtocolTCP, nil, false, 80),
			existingFrontendIPConfigs: []network.FrontendIPConfiguration{},
			existingPIP:               network.PublicIPAddress{Location: to.StringPtr("eastus")},
			getPIPError:               &retry.Error{HTTPStatusCode: http.StatusNotFound},
			regionZonesMap:            map[string][]string{"westus": {"1", "2", "3"}, "eastus": {"1", "2"}},
			expectedDirty:             true,
		},
		{
			description:               "reconcileFrontendIPConfigs should reconcile the zones for the new internal fip config",
			service:                   getInternalTestService("test", 80),
			existingFrontendIPConfigs: []network.FrontendIPConfiguration{},
			existingPIP:               network.PublicIPAddress{Location: to.StringPtr("eastus")},
			getPIPError:               &retry.Error{HTTPStatusCode: http.StatusNotFound},
			regionZonesMap:            map[string][]string{"westus": {"1", "2", "3"}, "eastus": {"1", "2"}},
			expectedZones:             &[]string{"1", "2", "3"},
			expectedDirty:             true,
		},
	} {
		t.Run(tc.description, func(t *testing.T) {
			cloud := GetTestCloud(ctrl)
			cloud.regionZonesMap = tc.regionZonesMap
			cloud.LoadBalancerSku = string(network.LoadBalancerSkuNameStandard)

			lb := getTestLoadBalancer(to.StringPtr("lb"), to.StringPtr("rg"), to.StringPtr("testCluster"), to.StringPtr("testCluster"), tc.service, "standard")
			lb.FrontendIPConfigurations = &tc.existingFrontendIPConfigs

			mockPIPClient := cloud.PublicIPAddressesClient.(*mockpublicipclient.MockInterface)
			mockPIPClient.EXPECT().Get(gomock.Any(), "rg", gomock.Any(), gomock.Any()).Return(tc.existingPIP, tc.getPIPError).MaxTimes(1)
			mockPIPClient.EXPECT().Get(gomock.Any(), "rg", gomock.Any(), gomock.Any()).Return(tc.existingPIP, nil).MaxTimes(1)
			mockPIPClient.EXPECT().CreateOrUpdate(gomock.Any(), "rg", gomock.Any(), gomock.Any()).Return(nil).MaxTimes(1)

			subnetClient := cloud.SubnetsClient.(*mocksubnetclient.MockInterface)
			subnetClient.EXPECT().Get(gomock.Any(), "rg", "vnet", "subnet", gomock.Any()).Return(network.Subnet{}, nil).MaxTimes(1)

			defaultLBFrontendIPConfigName := cloud.getDefaultFrontendIPConfigName(&tc.service)
			_, dirty, err := cloud.reconcileFrontendIPConfigs("testCluster", &tc.service, &lb, true, defaultLBFrontendIPConfigName)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedDirty, dirty)

			for _, fip := range *lb.FrontendIPConfigurations {
				if strings.EqualFold(to.String(fip.Name), defaultLBFrontendIPConfigName) {
					assert.Equal(t, tc.expectedZones, fip.Zones)
				}
			}
		})
	}
}

func TestReconcileSharedLoadBalancer(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	for _, tc := range []struct {
		description, vmSetsSharingPrimarySLB   string
		useMultipleSLBs, useBasicLB            bool
		existingLBs                            []network.LoadBalancer
		expectedListCount, expectedDeleteCount int
		expectedLBs                            []network.LoadBalancer
		expectedErr                            error
	}{
		{
			description:             "reconcileSharedLoadBalancer should decouple the vmSet from its dedicated lb if the vmSet is sharing the primary slb",
			useMultipleSLBs:         true,
			vmSetsSharingPrimarySLB: "vmss1,vmss2",
			existingLBs: []network.LoadBalancer{
				{
					Name: to.StringPtr("kubernetes"),
					LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
						BackendAddressPools: &[]network.BackendAddressPool{
							{
								Name: to.StringPtr("kubernetes"),
								BackendAddressPoolPropertiesFormat: &network.BackendAddressPoolPropertiesFormat{
									BackendIPConfigurations: &[]network.InterfaceIPConfiguration{
										{
											ID: to.StringPtr("vmss2-nic-1"),
										},
									},
								},
							},
						},
					},
				},
				{
					Name: to.StringPtr("kubernetes-internal"),
					LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
						BackendAddressPools: &[]network.BackendAddressPool{
							{
								Name: to.StringPtr("kubernetes"),
								BackendAddressPoolPropertiesFormat: &network.BackendAddressPoolPropertiesFormat{
									BackendIPConfigurations: &[]network.InterfaceIPConfiguration{
										{
											ID: to.StringPtr("vmss2-nic-1"),
										},
									},
								},
							},
						},
					},
				},
				{
					Name: to.StringPtr("vmss1"),
					LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
						BackendAddressPools: &[]network.BackendAddressPool{
							{
								Name: to.StringPtr("kubernetes"),
								BackendAddressPoolPropertiesFormat: &network.BackendAddressPoolPropertiesFormat{
									BackendIPConfigurations: &[]network.InterfaceIPConfiguration{
										{
											ID: to.StringPtr("vmss1-nic-1"),
										},
									},
								},
							},
						},
					},
				},
				{
					Name: to.StringPtr("vmss1-internal"),
					LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
						BackendAddressPools: &[]network.BackendAddressPool{
							{
								Name: to.StringPtr("kubernetes"),
								BackendAddressPoolPropertiesFormat: &network.BackendAddressPoolPropertiesFormat{
									BackendIPConfigurations: &[]network.InterfaceIPConfiguration{
										{
											ID: to.StringPtr("vmss1-nic-1"),
										},
									},
								},
							},
						},
					},
				},
			},
			expectedLBs: []network.LoadBalancer{
				{
					Name: to.StringPtr("kubernetes"),
					LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
						BackendAddressPools: &[]network.BackendAddressPool{
							{
								Name: to.StringPtr("kubernetes"),
								BackendAddressPoolPropertiesFormat: &network.BackendAddressPoolPropertiesFormat{
									BackendIPConfigurations: &[]network.InterfaceIPConfiguration{
										{
											ID: to.StringPtr("vmss2-nic-1"),
										},
										{
											ID: to.StringPtr("vmss1-nic-1"),
										},
									},
								},
							},
						},
					},
				},
				{
					Name: to.StringPtr("kubernetes-internal"),
					LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
						BackendAddressPools: &[]network.BackendAddressPool{
							{
								Name: to.StringPtr("kubernetes"),
								BackendAddressPoolPropertiesFormat: &network.BackendAddressPoolPropertiesFormat{
									BackendIPConfigurations: &[]network.InterfaceIPConfiguration{
										{
											ID: to.StringPtr("vmss2-nic-1"),
										},
										{
											ID: to.StringPtr("vmss1-nic-1"),
										},
									},
								},
							},
						},
					},
				},
			},
			expectedListCount:   1,
			expectedDeleteCount: 1,
		},
		{
			description: "reconcileSharedLoadBalancer should do nothing if the basic load balancer is used",
			useBasicLB:  true,
		},
		{
			description:     "reconcileSharedLoadBalancer should do nothing if the vmSet is not sharing the primary slb",
			useMultipleSLBs: true,
			existingLBs: []network.LoadBalancer{
				{
					Name: to.StringPtr("kubernetes"),
				},
				{
					Name: to.StringPtr("vmss1"),
				},
			},
			expectedLBs: []network.LoadBalancer{
				{
					Name: to.StringPtr("kubernetes"),
				},
				{
					Name: to.StringPtr("vmss1"),
				},
			},
			expectedListCount: 1,
		},
	} {
		t.Run(tc.description, func(t *testing.T) {
			cloud := GetTestCloud(ctrl)

			cloud.NodePoolsWithoutDedicatedSLB = tc.vmSetsSharingPrimarySLB

			if tc.useMultipleSLBs {
				cloud.LoadBalancerSku = consts.VMTypeStandard
				cloud.EnableMultipleStandardLoadBalancers = true
			} else if tc.useBasicLB {
				cloud.LoadBalancerSku = consts.LoadBalancerSkuBasic
			}

			mockLBClient := cloud.LoadBalancerClient.(*mockloadbalancerclient.MockInterface)
			mockLBClient.EXPECT().List(gomock.Any(), cloud.ResourceGroup).Return(tc.existingLBs, nil).Times(tc.expectedListCount)
			mockLBClient.EXPECT().Delete(gomock.Any(), cloud.ResourceGroup, "vmss1").Return(nil).Times(tc.expectedDeleteCount)
			mockLBClient.EXPECT().Delete(gomock.Any(), cloud.ResourceGroup, "vmss1-internal").Return(nil).Times(tc.expectedDeleteCount)

			mockVMSet := NewMockVMSet(ctrl)
			mockVMSet.EXPECT().EnsureBackendPoolDeleted(gomock.Any(), "/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/vmss1/backendAddressPools/kubernetes", "vmss1", gomock.Any()).Return(nil).Times(tc.expectedDeleteCount)
			mockVMSet.EXPECT().EnsureBackendPoolDeleted(gomock.Any(), "/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/vmss1-internal/backendAddressPools/kubernetes", "vmss1", gomock.Any()).Return(nil).Times(tc.expectedDeleteCount)
			mockVMSet.EXPECT().EnsureHostsInPool(gomock.Any(), gomock.Any(), "/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/kubernetes/backendAddressPools/kubernetes", "vmss1", false).Return(nil).Times(tc.expectedDeleteCount)
			mockVMSet.EXPECT().EnsureHostsInPool(gomock.Any(), gomock.Any(), "/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/kubernetes-internal/backendAddressPools/kubernetes", "vmss1", false).Return(nil).Times(tc.expectedDeleteCount)
			mockVMSet.EXPECT().GetAgentPoolVMSetNames(gomock.Any()).Return(&[]string{"vmss1", "vmss2"}, nil).Times(tc.expectedListCount)
			mockVMSet.EXPECT().GetPrimaryVMSetName().Return("vmss2").AnyTimes()
			cloud.VMSet = mockVMSet

			service := getTestService("test", v1.ProtocolTCP, nil, false, 80)
			lbs, err := cloud.reconcileSharedLoadBalancer(&service, "kubernetes", []*v1.Node{})
			assert.Equal(t, tc.expectedErr, err)
			assert.Equal(t, tc.expectedLBs, lbs)
		})
	}
}
