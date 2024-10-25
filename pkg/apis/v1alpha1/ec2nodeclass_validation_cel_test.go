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

package v1alpha1

import (
	"time"

	"github.com/alibabacloud-go/tea/tea"
	"github.com/imdario/mergo"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

var _ = Describe("CEL/Validation", func() {
	var nc *ECSNodeClass

	BeforeEach(func() {
		if env.Version.Minor() < 25 {
			Skip("CEL Validation is for 1.25>")
		}
		nc = &ECSNodeClass{
			ObjectMeta: test.ObjectMeta(metav1.ObjectMeta{}),
			Spec: ECSNodeClassSpec{
				ImageSelectorTerms: []ImageSelectorTerm{{Alias: "AlibabaCloudLinux3"}},
				SecurityGroupSelectorTerms: []SecurityGroupSelectorTerm{
					{
						Tags: map[string]string{
							"*": "*",
						},
					},
				},
				VSwitchSelectorTerms: []VSwitchSelectorTerm{
					{
						Tags: map[string]string{
							"*": "*",
						},
					},
				},
			},
		}
	})
	It("should succeed if just minimum required", func() {
		Expect(env.Client.Create(ctx, nc)).To(Succeed())
	})
	Context("UserData", func() {
		It("should succeed if user data is empty", func() {
			Expect(env.Client.Create(ctx, nc)).To(Succeed())
		})
	})
	Context("Tags", func() {
		It("should succeed when tags are empty", func() {
			nc.Spec.Tags = map[string]string{}
			Expect(env.Client.Create(ctx, nc)).To(Succeed())
		})
		It("should succeed if tags aren't in restricted tag keys", func() {
			nc.Spec.Tags = map[string]string{
				"karpenter.sh/custom-key": "value",
				"karpenter.sh/managed":    "true",
				"kubernetes.io/role/key":  "value",
			}
			Expect(env.Client.Create(ctx, nc)).To(Succeed())
		})
		It("should fail if tags contain a restricted domain key", func() {
			nc.Spec.Tags = map[string]string{
				karpv1.NodePoolLabelKey: "value",
			}
			Expect(env.Client.Create(ctx, nc)).To(Not(Succeed()))
			nc.Spec.Tags = map[string]string{
				"kubernetes.io/cluster/test": "value",
			}
			Expect(env.Client.Create(ctx, nc)).To(Not(Succeed()))
			nc.Spec.Tags = map[string]string{
				ECSClusterNameTagKey: "test",
			}
			Expect(env.Client.Create(ctx, nc)).To(Not(Succeed()))
			nc.Spec.Tags = map[string]string{
				LabelNodeClass: "test",
			}
			Expect(env.Client.Create(ctx, nc)).To(Not(Succeed()))
			nc.Spec.Tags = map[string]string{
				"karpenter.sh/nodeclaim": "test",
			}
			Expect(env.Client.Create(ctx, nc)).To(Not(Succeed()))
		})
	})
	Context("VSwitchSelectorTerms", func() {
		It("should succeed with a valid vSwitch selector on tags", func() {
			nc.Spec.VSwitchSelectorTerms = []VSwitchSelectorTerm{
				{
					Tags: map[string]string{
						"test": "testvalue",
					},
				},
			}
			Expect(env.Client.Create(ctx, nc)).To(Succeed())
		})
		It("should succeed with a valid vSwitch selector on id", func() {
			nc.Spec.VSwitchSelectorTerms = []VSwitchSelectorTerm{
				{
					ID: "vsw-12345749",
				},
			}
			Expect(env.Client.Create(ctx, nc)).To(Succeed())
		})
		It("should fail when vSwitch selector terms is set to nil", func() {
			nc.Spec.VSwitchSelectorTerms = nil
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		It("should fail when no vSwitch selector terms exist", func() {
			nc.Spec.VSwitchSelectorTerms = []VSwitchSelectorTerm{}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		It("should fail when a vSwitch selector term has no values", func() {
			nc.Spec.VSwitchSelectorTerms = []VSwitchSelectorTerm{
				{},
			}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		It("should fail when a vSwitch selector term has no tag map values", func() {
			nc.Spec.VSwitchSelectorTerms = []VSwitchSelectorTerm{
				{
					Tags: map[string]string{},
				},
			}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		It("should succeed when a vSwitch selector term has a tag map key that is empty", func() {
			nc.Spec.VSwitchSelectorTerms = []VSwitchSelectorTerm{
				{
					Tags: map[string]string{
						"test": "",
					},
				},
			}
			Expect(env.Client.Create(ctx, nc)).To(Succeed())
		})
		It("should fail when a vSwitch selector term has a tag map value that is empty", func() {
			nc.Spec.VSwitchSelectorTerms = []VSwitchSelectorTerm{
				{
					Tags: map[string]string{
						"": "testvalue",
					},
				},
			}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		It("should fail when the last vSwitch selector is invalid", func() {
			nc.Spec.VSwitchSelectorTerms = []VSwitchSelectorTerm{
				{
					Tags: map[string]string{
						"test": "testvalue",
					},
				},
				{
					Tags: map[string]string{
						"test2": "testvalue2",
					},
				},
				{
					Tags: map[string]string{
						"test3": "testvalue3",
					},
				},
				{
					Tags: map[string]string{
						"": "testvalue4",
					},
				},
			}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		It("should fail when specifying id with tags", func() {
			nc.Spec.VSwitchSelectorTerms = []VSwitchSelectorTerm{
				{
					ID: "vsw-12345749",
					Tags: map[string]string{
						"test": "testvalue",
					},
				},
			}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		It("should fail when id invalid", func() {
			nc.Spec.VSwitchSelectorTerms = []VSwitchSelectorTerm{
				{
					ID: "subnet-12345749",
				},
			}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
	})
	Context("SecurityGroupSelectorTerms", func() {
		It("should succeed with a valid security group selector on tags", func() {
			nc.Spec.SecurityGroupSelectorTerms = []SecurityGroupSelectorTerm{
				{
					Tags: map[string]string{
						"test": "testvalue",
					},
				},
			}
			Expect(env.Client.Create(ctx, nc)).To(Succeed())
		})
		It("should succeed with a valid security group selector on id", func() {
			nc.Spec.SecurityGroupSelectorTerms = []SecurityGroupSelectorTerm{
				{
					ID: "sg-12345749",
				},
			}
			Expect(env.Client.Create(ctx, nc)).To(Succeed())
		})
		It("should succeed with a valid security group selector on name", func() {
			nc.Spec.SecurityGroupSelectorTerms = []SecurityGroupSelectorTerm{
				{
					Name: "testname",
				},
			}
			Expect(env.Client.Create(ctx, nc)).To(Succeed())
		})
		It("should fail when security group selector terms is set to nil", func() {
			nc.Spec.SecurityGroupSelectorTerms = nil
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		It("should fail when no security group selector terms exist", func() {
			nc.Spec.SecurityGroupSelectorTerms = []SecurityGroupSelectorTerm{}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		It("should fail when a security group selector term has no values", func() {
			nc.Spec.SecurityGroupSelectorTerms = []SecurityGroupSelectorTerm{
				{},
			}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		It("should fail when a security group selector term has no tag map values", func() {
			nc.Spec.SecurityGroupSelectorTerms = []SecurityGroupSelectorTerm{
				{
					Tags: map[string]string{},
				},
			}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		It("should succeed when a security group selector term has a tag map key that is empty", func() {
			nc.Spec.SecurityGroupSelectorTerms = []SecurityGroupSelectorTerm{
				{
					Tags: map[string]string{
						"test": "",
					},
				},
			}
			Expect(env.Client.Create(ctx, nc)).To(Succeed())
		})
		It("should fail when a security group selector term has a tag map value that is empty", func() {
			nc.Spec.SecurityGroupSelectorTerms = []SecurityGroupSelectorTerm{
				{
					Tags: map[string]string{
						"": "testvalue",
					},
				},
			}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		It("should fail when the last security group selector is invalid", func() {
			nc.Spec.SecurityGroupSelectorTerms = []SecurityGroupSelectorTerm{
				{
					Tags: map[string]string{
						"test": "testvalue",
					},
				},
				{
					Tags: map[string]string{
						"test2": "testvalue2",
					},
				},
				{
					Tags: map[string]string{
						"test3": "testvalue3",
					},
				},
				{
					Tags: map[string]string{
						"": "testvalue4",
					},
				},
			}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		It("should fail when specifying id with tags", func() {
			nc.Spec.SecurityGroupSelectorTerms = []SecurityGroupSelectorTerm{
				{
					ID: "sg-12345749",
					Tags: map[string]string{
						"test": "testvalue",
					},
				},
			}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		It("should fail when specifying id with name", func() {
			nc.Spec.SecurityGroupSelectorTerms = []SecurityGroupSelectorTerm{
				{
					ID:   "sg-12345749",
					Name: "my-security-group",
				},
			}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		It("should fail when specifying name with tags", func() {
			nc.Spec.SecurityGroupSelectorTerms = []SecurityGroupSelectorTerm{
				{
					Name: "my-security-group",
					Tags: map[string]string{
						"test": "testvalue",
					},
				},
			}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
	})
	Context("ImageSelectorTerms", func() {
		Context("ImageFamily", func() {
			imageFamilies := []string{ImageFamilyAlibabaCloudLinux3, ImageFamilyAlibabaCloudLinux2}
			DescribeTable("should succeed with valid families", func() []interface{} {
				f := func(imageFamily string) {
					nc.Spec.ImageSelectorTerms = []ImageSelectorTerm{{Alias: imageFamily}}
					Expect(env.Client.Create(ctx, nc)).To(Succeed())
				}
				entries := lo.Map(imageFamilies, func(family string, _ int) interface{} {
					return Entry(family, family)
				})
				return append([]interface{}{f}, entries...)
			}()...)
			It("should fail with the invalid family", func() {
				nc.Spec.ImageSelectorTerms = []ImageSelectorTerm{{Alias: "Ubuntu"}}
				Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
			})
			DescribeTable(
				"should fail for incorrectly formatted aliases",
				func(aliases ...string) {
					for _, alias := range aliases {
						nc.Spec.ImageSelectorTerms = []ImageSelectorTerm{{Alias: alias}}
						Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
					}
				},
				Entry("missing family", "@latest"),
				Entry("missing version", "AlibabaCloudLinux3@"),
				Entry("invalid separator", "AlibabaCloudLinux3-latest"),
			)
		})
		It("should succeed with a valid image selector on alias", func() {
			nc.Spec.ImageSelectorTerms = []ImageSelectorTerm{{
				Alias: "AlibabaCloudLinux3",
			}}
			Expect(env.Client.Create(ctx, nc)).To(Succeed())
		})
		It("should succeed with a valid image selector on id", func() {
			nc.Spec.ImageSelectorTerms = []ImageSelectorTerm{
				{
					ID: "img-12345749",
				},
			}
			Expect(env.Client.Create(ctx, nc)).To(Succeed())
		})
		It("should fail when a image selector term has no values", func() {
			nc.Spec.ImageSelectorTerms = []ImageSelectorTerm{
				{},
			}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		It("should fail when neither imageID nor an alias are specified", func() {
			nc.Spec.ImageSelectorTerms = []ImageSelectorTerm{}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		It("should fail when image selector terms is set to nil", func() {
			nc.Spec.ImageSelectorTerms = nil
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		DescribeTable(
			"should fail when specifying id with other fields",
			func(mutation ImageSelectorTerm) {
				term := ImageSelectorTerm{ID: "img-1234749"}
				Expect(mergo.Merge(&term, &mutation)).To(Succeed())
				nc.Spec.ImageSelectorTerms = []ImageSelectorTerm{term}
				Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
			},
			Entry("alias", ImageSelectorTerm{Alias: "AlibabaCloudLinux3"}),
		)
		DescribeTable(
			"should fail when specifying alias with other fields",
			func(mutation ImageSelectorTerm) {
				term := ImageSelectorTerm{Alias: "AlibabaCloudLinux3"}
				Expect(mergo.Merge(&term, &mutation)).To(Succeed())
				nc.Spec.ImageSelectorTerms = []ImageSelectorTerm{term}
				Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
			},
			Entry("id", ImageSelectorTerm{ID: "img-1234749"}),
		)
		It("should fail when specifying alias with other terms", func() {
			nc.Spec.ImageSelectorTerms = []ImageSelectorTerm{
				{Alias: "AlibabaCloudLinux3"},
				{ID: "img-1234749"},
			}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
	})
	Context("Kubelet", func() {
		It("should fail on kubeReserved with invalid keys", func() {
			nc.Spec.KubeletConfiguration = &KubeletConfiguration{
				KubeReserved: map[string]string{
					string(corev1.ResourcePods): "2",
				},
			}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		It("should fail on systemReserved with invalid keys", func() {
			nc.Spec.KubeletConfiguration = &KubeletConfiguration{
				SystemReserved: map[string]string{
					string(corev1.ResourcePods): "2",
				},
			}
			Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
		})
		Context("Eviction Signals", func() {
			Context("Eviction Hard", func() {
				It("should succeed on evictionHard with valid keys", func() {
					nc.Spec.KubeletConfiguration = &KubeletConfiguration{
						EvictionHard: map[string]string{
							"memory.available":   "5%",
							"nodefs.available":   "10%",
							"nodefs.inodesFree":  "15%",
							"imagefs.available":  "5%",
							"imagefs.inodesFree": "5%",
							"pid.available":      "5%",
						},
					}
					Expect(env.Client.Create(ctx, nc)).To(Succeed())
				})
				It("should fail on evictionHard with invalid keys", func() {
					nc.Spec.KubeletConfiguration = &KubeletConfiguration{
						EvictionHard: map[string]string{
							"memory": "5%",
						},
					}
					Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
				})
				It("should fail on invalid formatted percentage value in evictionHard", func() {
					nc.Spec.KubeletConfiguration = &KubeletConfiguration{
						EvictionHard: map[string]string{
							"memory.available": "5%3",
						},
					}
					Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
				})
				It("should fail on invalid percentage value (too large) in evictionHard", func() {
					nc.Spec.KubeletConfiguration = &KubeletConfiguration{
						EvictionHard: map[string]string{
							"memory.available": "110%",
						},
					}
					Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
				})
				It("should fail on invalid quantity value in evictionHard", func() {
					nc.Spec.KubeletConfiguration = &KubeletConfiguration{
						EvictionHard: map[string]string{
							"memory.available": "110GB",
						},
					}
					Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
				})
			})
		})
		Context("Eviction Soft", func() {
			It("should succeed on evictionSoft with valid keys", func() {
				nc.Spec.KubeletConfiguration = &KubeletConfiguration{
					EvictionSoft: map[string]string{
						"memory.available":   "5%",
						"nodefs.available":   "10%",
						"nodefs.inodesFree":  "15%",
						"imagefs.available":  "5%",
						"imagefs.inodesFree": "5%",
						"pid.available":      "5%",
					},
					EvictionSoftGracePeriod: map[string]metav1.Duration{
						"memory.available":   {Duration: time.Minute},
						"nodefs.available":   {Duration: time.Second * 90},
						"nodefs.inodesFree":  {Duration: time.Minute * 5},
						"imagefs.available":  {Duration: time.Hour},
						"imagefs.inodesFree": {Duration: time.Hour * 24},
						"pid.available":      {Duration: time.Minute},
					},
				}
				Expect(env.Client.Create(ctx, nc)).To(Succeed())
			})
			It("should fail on evictionSoft with invalid keys", func() {
				nc.Spec.KubeletConfiguration = &KubeletConfiguration{
					EvictionSoft: map[string]string{
						"memory": "5%",
					},
					EvictionSoftGracePeriod: map[string]metav1.Duration{
						"memory": {Duration: time.Minute},
					},
				}
				Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
			})
			It("should fail on invalid formatted percentage value in evictionSoft", func() {
				nc.Spec.KubeletConfiguration = &KubeletConfiguration{
					EvictionSoft: map[string]string{
						"memory.available": "5%3",
					},
					EvictionSoftGracePeriod: map[string]metav1.Duration{
						"memory.available": {Duration: time.Minute},
					},
				}
				Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
			})
			It("should fail on invalid percentage value (too large) in evictionSoft", func() {
				nc.Spec.KubeletConfiguration = &KubeletConfiguration{
					EvictionSoft: map[string]string{
						"memory.available": "110%",
					},
					EvictionSoftGracePeriod: map[string]metav1.Duration{
						"memory.available": {Duration: time.Minute},
					},
				}
				Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
			})
			It("should fail on invalid quantity value in evictionSoft", func() {
				nc.Spec.KubeletConfiguration = &KubeletConfiguration{
					EvictionSoft: map[string]string{
						"memory.available": "110GB",
					},
					EvictionSoftGracePeriod: map[string]metav1.Duration{
						"memory.available": {Duration: time.Minute},
					},
				}
				Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
			})
			It("should fail when eviction soft doesn't have matching grace period", func() {
				nc.Spec.KubeletConfiguration = &KubeletConfiguration{
					EvictionSoft: map[string]string{
						"memory.available": "200Mi",
					},
				}
				Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
			})
		})
		Context("GCThresholdPercent", func() {
			It("should succeed on a valid imageGCHighThresholdPercent", func() {
				nc.Spec.KubeletConfiguration = &KubeletConfiguration{
					ImageGCHighThresholdPercent: lo.ToPtr(int32(10)),
				}
				Expect(env.Client.Create(ctx, nc)).To(Succeed())
			})
			It("should fail when imageGCHighThresholdPercent is less than imageGCLowThresholdPercent", func() {
				nc.Spec.KubeletConfiguration = &KubeletConfiguration{
					ImageGCHighThresholdPercent: lo.ToPtr(int32(50)),
					ImageGCLowThresholdPercent:  lo.ToPtr(int32(60)),
				}
				Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
			})
			It("should fail when imageGCLowThresholdPercent is greather than imageGCHighThresheldPercent", func() {
				nc.Spec.KubeletConfiguration = &KubeletConfiguration{
					ImageGCHighThresholdPercent: lo.ToPtr(int32(50)),
					ImageGCLowThresholdPercent:  lo.ToPtr(int32(60)),
				}
				Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
			})
		})
		Context("Eviction Soft Grace Period", func() {
			It("should succeed on evictionSoftGracePeriod with valid keys", func() {
				nc.Spec.KubeletConfiguration = &KubeletConfiguration{
					EvictionSoft: map[string]string{
						"memory.available":   "5%",
						"nodefs.available":   "10%",
						"nodefs.inodesFree":  "15%",
						"imagefs.available":  "5%",
						"imagefs.inodesFree": "5%",
						"pid.available":      "5%",
					},
					EvictionSoftGracePeriod: map[string]metav1.Duration{
						"memory.available":   {Duration: time.Minute},
						"nodefs.available":   {Duration: time.Second * 90},
						"nodefs.inodesFree":  {Duration: time.Minute * 5},
						"imagefs.available":  {Duration: time.Hour},
						"imagefs.inodesFree": {Duration: time.Hour * 24},
						"pid.available":      {Duration: time.Minute},
					},
				}
				Expect(env.Client.Create(ctx, nc)).To(Succeed())
			})
			It("should fail on evictionSoftGracePeriod with invalid keys", func() {
				nc.Spec.KubeletConfiguration = &KubeletConfiguration{
					EvictionSoftGracePeriod: map[string]metav1.Duration{
						"memory": {Duration: time.Minute},
					},
				}
				Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
			})
			It("should fail when eviction soft grace period doesn't have matching threshold", func() {
				nc.Spec.KubeletConfiguration = &KubeletConfiguration{
					EvictionSoftGracePeriod: map[string]metav1.Duration{
						"memory.available": {Duration: time.Minute},
					},
				}
				Expect(env.Client.Create(ctx, nc)).ToNot(Succeed())
			})
		})
	})
	Context("SystemDisk", func() {
		It("should succeed if one system disk is specified", func() {
			nodeClass := &ECSNodeClass{
				ObjectMeta: test.ObjectMeta(metav1.ObjectMeta{}),
				Spec: ECSNodeClassSpec{
					ImageSelectorTerms:         nc.Spec.ImageSelectorTerms,
					VSwitchSelectorTerms:       nc.Spec.VSwitchSelectorTerms,
					SecurityGroupSelectorTerms: nc.Spec.SecurityGroupSelectorTerms,
					SystemDisk: &SystemDisk{
						Category:             tea.String("cloud_essd"),
						Size:                 tea.Int32(80),
						DiskName:             tea.String("device-1"),
						PerformanceLevel:     tea.String("PL2"),
						AutoSnapshotPolicyID: tea.String("sp-1234"),
						BurstingEnabled:      tea.Bool(true),
					},
				},
			}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
		It("should succeed for few parameters", func() {
			nodeClass := &ECSNodeClass{
				ObjectMeta: test.ObjectMeta(metav1.ObjectMeta{}),
				Spec: ECSNodeClassSpec{
					ImageSelectorTerms:         nc.Spec.ImageSelectorTerms,
					VSwitchSelectorTerms:       nc.Spec.VSwitchSelectorTerms,
					SecurityGroupSelectorTerms: nc.Spec.SecurityGroupSelectorTerms,
					SystemDisk: &SystemDisk{
						Category: tea.String("cloud_auto"),
						Size:     tea.Int32(80),
					},
				},
			}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})

		It("should fail size is less then 20G", func() {
			nodeClass := &ECSNodeClass{
				ObjectMeta: test.ObjectMeta(metav1.ObjectMeta{}),
				Spec: ECSNodeClassSpec{
					ImageSelectorTerms:         nc.Spec.ImageSelectorTerms,
					VSwitchSelectorTerms:       nc.Spec.VSwitchSelectorTerms,
					SecurityGroupSelectorTerms: nc.Spec.SecurityGroupSelectorTerms,
					SystemDisk: &SystemDisk{
						Category: tea.String("cloud_essd"),
						Size:     tea.Int32(10),
					},
				},
			}
			Expect(env.Client.Create(ctx, nodeClass)).To(Not(Succeed()))
		})
		It("should fail size is greater then 2T", func() {
			nodeClass := &ECSNodeClass{
				ObjectMeta: test.ObjectMeta(metav1.ObjectMeta{}),
				Spec: ECSNodeClassSpec{
					ImageSelectorTerms:         nc.Spec.ImageSelectorTerms,
					VSwitchSelectorTerms:       nc.Spec.VSwitchSelectorTerms,
					SecurityGroupSelectorTerms: nc.Spec.SecurityGroupSelectorTerms,
					SystemDisk: &SystemDisk{
						Category: tea.String("cloud_essd"),
						Size:     tea.Int32(4096),
					},
				},
			}
			Expect(env.Client.Create(ctx, nodeClass)).To(Not(Succeed()))
		})
	})
})
