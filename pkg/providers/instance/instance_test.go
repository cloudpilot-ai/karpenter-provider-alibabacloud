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
	"context"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"

	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/cluster"
)

var _ = Describe("getVSwitchID", func() {
	var provider *DefaultProvider

	BeforeEach(func() {
		provider = &DefaultProvider{}
	})

	Context("random selection (on-demand / balanced policy)", func() {
		It("should never return a vswitch in a zone not allowed by the nodeclaim requirements", func() {
			instanceType := &cloudprovider.InstanceType{
				Name: "ecs.g7.large",
				Offerings: cloudprovider.Offerings{
					newOffering(zoneA, karpv1.CapacityTypeOnDemand, 1.0, true),
					newOffering(zoneB, karpv1.CapacityTypeOnDemand, 1.0, true),
					newOffering(zoneC, karpv1.CapacityTypeOnDemand, 1.0, true),
				},
			}
			reqs := pinnedReqs(karpv1.CapacityTypeOnDemand, zoneB)

			for i := 0; i < 50; i++ {
				Expect(provider.getVSwitchID(instanceType, zonalVSwitches(zoneA, zoneB, zoneC), reqs, karpv1.CapacityTypeOnDemand, "")).To(Equal("vsw-" + zoneB))
			}
		})

		It("should skip zones where the instance type has no offering", func() {
			// Without this filter, the APG request fails with NoStock for (instanceType, vswitch) pairs
			// in zones that don't sell the instance type.
			instanceType := &cloudprovider.InstanceType{
				Name:      "ecs.g7.large",
				Offerings: cloudprovider.Offerings{newOffering(zoneB, karpv1.CapacityTypeOnDemand, 1.0, true)},
			}
			reqs := pinnedReqs(karpv1.CapacityTypeOnDemand)

			for i := 0; i < 50; i++ {
				Expect(provider.getVSwitchID(instanceType, zonalVSwitches(zoneA, zoneB, zoneC), reqs, karpv1.CapacityTypeOnDemand, "")).To(Equal("vsw-" + zoneB))
			}
		})

		It("should skip offerings marked unavailable", func() {
			// Unavailable offerings are those marked by the ICE cache or zones that don't sell the type.
			instanceType := &cloudprovider.InstanceType{
				Name: "ecs.g7.large",
				Offerings: cloudprovider.Offerings{
					newOffering(zoneA, karpv1.CapacityTypeOnDemand, 1.0, false),
					newOffering(zoneB, karpv1.CapacityTypeOnDemand, 1.0, true),
				},
			}
			reqs := pinnedReqs(karpv1.CapacityTypeOnDemand)

			for i := 0; i < 50; i++ {
				Expect(provider.getVSwitchID(instanceType, zonalVSwitches(zoneA, zoneB), reqs, karpv1.CapacityTypeOnDemand, "")).To(Equal("vsw-" + zoneB))
			}
		})

		It("should respect the pinned capacity type when the balanced policy is set", func() {
			// A zone with only on-demand offerings is not a valid candidate for a spot launch.
			instanceType := &cloudprovider.InstanceType{
				Name: "ecs.g7.large",
				Offerings: cloudprovider.Offerings{
					newOffering(zoneA, karpv1.CapacityTypeOnDemand, 1.0, true),
					newOffering(zoneB, karpv1.CapacityTypeSpot, 0.5, true),
				},
			}
			reqs := pinnedReqs(karpv1.CapacityTypeSpot)

			for i := 0; i < 50; i++ {
				Expect(provider.getVSwitchID(instanceType, zonalVSwitches(zoneA, zoneB), reqs, karpv1.CapacityTypeSpot, v1alpha1.VSwitchSelectionPolicyBalanced)).To(Equal("vsw-" + zoneB))
			}
		})

		It("should return empty when there is no compatible available offering", func() {
			// The instance type must be skipped (vSwitchID == "") rather than launched into an arbitrary zone.
			instanceType := &cloudprovider.InstanceType{
				Name: "ecs.g7.large",
				Offerings: cloudprovider.Offerings{
					newOffering(zoneA, karpv1.CapacityTypeOnDemand, 1.0, false),
					newOffering(zoneC, karpv1.CapacityTypeOnDemand, 1.0, true), // no vswitch resolved in zone C
				},
			}

			// zone requirement excludes the only available zone
			Expect(provider.getVSwitchID(instanceType, zonalVSwitches(zoneA, zoneC), pinnedReqs(karpv1.CapacityTypeOnDemand, zoneB), karpv1.CapacityTypeOnDemand, "")).To(Equal(""))
			// no vswitch in the available zone (zone C has no resolved vswitch)
			Expect(provider.getVSwitchID(instanceType, zonalVSwitches(zoneA, zoneB), pinnedReqs(karpv1.CapacityTypeOnDemand), karpv1.CapacityTypeOnDemand, "")).To(Equal(""))

			// all offerings unavailable
			unavailableType := &cloudprovider.InstanceType{
				Name: "ecs.g7.large",
				Offerings: cloudprovider.Offerings{
					newOffering(zoneA, karpv1.CapacityTypeOnDemand, 1.0, false),
					newOffering(zoneB, karpv1.CapacityTypeOnDemand, 1.0, false),
				},
			}
			Expect(provider.getVSwitchID(unavailableType, zonalVSwitches(zoneA, zoneB), pinnedReqs(karpv1.CapacityTypeOnDemand), karpv1.CapacityTypeOnDemand, "")).To(Equal(""))
		})

		It("should spread launches across compatible zones with the balanced policy", func() {
			instanceType := &cloudprovider.InstanceType{
				Name: "ecs.g7.large",
				Offerings: cloudprovider.Offerings{
					newOffering(zoneA, karpv1.CapacityTypeOnDemand, 1.0, true),
					newOffering(zoneB, karpv1.CapacityTypeOnDemand, 1.0, true),
				},
			}
			reqs := pinnedReqs(karpv1.CapacityTypeOnDemand)

			seen := map[string]int{}
			for i := 0; i < 200; i++ {
				seen[provider.getVSwitchID(instanceType, zonalVSwitches(zoneA, zoneB), reqs, karpv1.CapacityTypeOnDemand, v1alpha1.VSwitchSelectionPolicyBalanced)]++
			}
			Expect(seen).To(HaveLen(2))
			Expect(seen["vsw-"+zoneA]).To(BeNumerically(">", 0))
			Expect(seen["vsw-"+zoneB]).To(BeNumerically(">", 0))
		})
	})

	Context("cheapest selection (spot)", func() {
		It("should pick the lowest-priced available compatible zone", func() {
			instanceType := &cloudprovider.InstanceType{
				Name: "ecs.g7.large",
				Offerings: cloudprovider.Offerings{
					newOffering(zoneA, karpv1.CapacityTypeSpot, 0.1, false), // cheapest but unavailable (ICE)
					newOffering(zoneB, karpv1.CapacityTypeSpot, 0.5, true),
					newOffering(zoneC, karpv1.CapacityTypeSpot, 0.8, true),
				},
			}
			reqs := pinnedReqs(karpv1.CapacityTypeSpot)

			Expect(provider.getVSwitchID(instanceType, zonalVSwitches(zoneA, zoneB, zoneC), reqs, karpv1.CapacityTypeSpot, "")).To(Equal("vsw-" + zoneB))
		})
	})
})

// getProvisioningGroup only needs two provider dependencies: the image family resolver to filter
// instance types by system disk, and the cluster provider to build user data. Both fakes are no-ops.
type fakeImageFamilyResolver struct{}

func (fakeImageFamilyResolver) FilterInstanceTypesBySystemDisk(_ context.Context, _ *v1alpha1.ECSNodeClass, instanceTypes []*cloudprovider.InstanceType) []*cloudprovider.InstanceType {
	return instanceTypes
}

type fakeClusterProvider struct{}

func (fakeClusterProvider) ClusterType() string { return "" }

func (fakeClusterProvider) UserData(context.Context, map[string]string, []corev1.Taint, *v1alpha1.KubeletConfiguration, *string, bool) (string, error) {
	return "", nil
}

func (fakeClusterProvider) GetClusterCNI(context.Context) (string, error)      { return "", nil }
func (fakeClusterProvider) LivenessProbe(*http.Request) error                  { return nil }
func (fakeClusterProvider) GetSupportedImages(string) ([]cluster.Image, error) { return nil, nil }
func (fakeClusterProvider) FeatureFlags() cluster.FeatureFlags                 { return cluster.FeatureFlags{} }

func (fakeClusterProvider) DefaultOverhead(corev1.ResourceList) cloudprovider.InstanceTypeOverhead {
	return cloudprovider.InstanceTypeOverhead{}
}

func nodeClaimWithZones(zones ...string) *karpv1.NodeClaim {
	reqs := []karpv1.NodeSelectorRequirementWithMinValues{}
	if len(zones) > 0 {
		reqs = append(reqs, karpv1.NodeSelectorRequirementWithMinValues{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      corev1.LabelTopologyZone,
				Operator: corev1.NodeSelectorOpIn,
				Values:   zones,
			},
		})
	}
	return &karpv1.NodeClaim{Spec: karpv1.NodeClaimSpec{Requirements: reqs}}
}

var _ = Describe("getProvisioningGroup", func() {
	var provider *DefaultProvider
	var nodeClass *v1alpha1.ECSNodeClass

	BeforeEach(func() {
		provider = &DefaultProvider{
			region:              "cn-hangzhou",
			imageFamilyResolver: fakeImageFamilyResolver{},
			clusterProvider:     fakeClusterProvider{},
		}
		nodeClass = &v1alpha1.ECSNodeClass{
			Status: v1alpha1.ECSNodeClassStatus{
				Images:         []v1alpha1.Image{{ID: "img-123"}},
				SecurityGroups: []v1alpha1.SecurityGroup{{ID: "sg-1"}},
			},
		}
	})

	It("should only use vswitches in zones allowed by the nodeclaim requirements", func() {
		nodeClaim := nodeClaimWithZones(zoneB)
		instanceTypes := []*cloudprovider.InstanceType{
			{
				Name: "ecs.g7.large",
				Offerings: cloudprovider.Offerings{
					newOffering(zoneA, karpv1.CapacityTypeOnDemand, 1.0, true),
					newOffering(zoneB, karpv1.CapacityTypeOnDemand, 1.0, true),
					newOffering(zoneC, karpv1.CapacityTypeOnDemand, 1.0, true),
				},
			},
			{
				Name:      "ecs.c7.large", // no offering in the required zone
				Offerings: cloudprovider.Offerings{newOffering(zoneA, karpv1.CapacityTypeOnDemand, 1.0, true)},
			},
		}

		req, err := provider.getProvisioningGroup(context.Background(), nodeClass, nodeClaim, instanceTypes, zonalVSwitches(zoneA, zoneB, zoneC), karpv1.CapacityTypeOnDemand, map[string]string{})
		Expect(err).NotTo(HaveOccurred())

		// every launch template config must target the required zone, and the instance type
		// without an offering there is excluded from the APG request
		Expect(req.LaunchTemplateConfig).NotTo(BeEmpty())
		for _, config := range req.LaunchTemplateConfig {
			Expect(lo.FromPtr(config.InstanceType)).To(Equal("ecs.g7.large"))
			Expect(lo.FromPtr(config.VSwitchId)).To(Equal("vsw-" + zoneB))
		}
	})

	It("should fail when no instance type has a compatible offering", func() {
		nodeClaim := nodeClaimWithZones(zoneB)
		instanceTypes := []*cloudprovider.InstanceType{
			{
				Name:      "ecs.g7.large", // offering not in the required zone
				Offerings: cloudprovider.Offerings{newOffering(zoneA, karpv1.CapacityTypeOnDemand, 1.0, true)},
			},
		}

		_, err := provider.getProvisioningGroup(context.Background(), nodeClass, nodeClaim, instanceTypes, zonalVSwitches(zoneA, zoneB), karpv1.CapacityTypeOnDemand, map[string]string{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no capacity offerings are currently available"))
	})
})
