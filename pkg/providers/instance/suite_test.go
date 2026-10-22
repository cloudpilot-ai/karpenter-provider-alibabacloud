/*
Copyright 2024 The CloudPilot AI Authors.

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

package instance

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/vswitch"
)

const (
	zoneA = "cn-hangzhou-a"
	zoneB = "cn-hangzhou-b"
	zoneC = "cn-hangzhou-c"
)

func newOffering(zone string, capacityType string, price float64, available bool) cloudprovider.Offering {
	return cloudprovider.Offering{
		Requirements: scheduling.NewRequirements(
			scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, capacityType),
			scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, zone),
			scheduling.NewRequirement(v1alpha1.LabelTopologyZoneID, corev1.NodeSelectorOpIn, zone),
		),
		Price:     price,
		Available: available,
	}
}

func zonalVSwitches(zones ...string) map[string]*vswitch.VSwitch {
	out := map[string]*vswitch.VSwitch{}
	for _, zone := range zones {
		out[zone] = &vswitch.VSwitch{ID: "vsw-" + zone, ZoneID: zone, AvailableIPAddressCount: 100}
	}
	return out
}

// pinnedReqs mirrors what getProvisioningGroup passes in: nodeclaim requirements with
// the capacity type pinned to a single value, optionally with a zone constraint.
func pinnedReqs(capacityType string, zones ...string) scheduling.Requirements {
	reqs := scheduling.NewRequirements(
		scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, capacityType),
	)
	if len(zones) > 0 {
		reqs.Add(scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, zones...))
		reqs.Add(scheduling.NewRequirement(v1alpha1.LabelTopologyZoneID, corev1.NodeSelectorOpIn, zones...))
	}
	return reqs
}

func TestInstanceProvider(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "InstanceProvider")
}
