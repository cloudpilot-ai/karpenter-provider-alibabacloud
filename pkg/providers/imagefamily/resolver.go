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

package imagefamily

import (
	"context"
	"fmt"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sync"

	ecs "github.com/alibabacloud-go/ecs-20140526/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/patrickmn/go-cache"
	"go.uber.org/multierr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/utils/resources"

	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/cluster"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/utils/alierrors"
)

var DefaultSystemDisk = v1alpha1.SystemDisk{
	// TODO: Change me, comprehensive ranking based on the pricing
	Categories: []string{"cloud", "cloud_ssd", "cloud_efficiency", "cloud_essd", "cloud_auto", "cloud_essd_entry"},
	VolumeSize: resources.Quantity("20Gi"),
}

// Options for ImageFamily
type Options struct {
	ACKProvider cluster.Provider
}

type InstanceTypeAvailableSystemDisk struct {
	availableSystemDisk sets.Set[string]
	// todo: verify availability zone
	// availableZone sets.Set[string]
}

func newInstanceTypeAvailableSystemDisk() *InstanceTypeAvailableSystemDisk {
	return &InstanceTypeAvailableSystemDisk{
		availableSystemDisk: sets.Set[string]{},
	}
}

func (s *InstanceTypeAvailableSystemDisk) AddAvailableSystemDisk(systemDisks ...string) {
	s.availableSystemDisk.Insert(systemDisks...)
}

func (s *InstanceTypeAvailableSystemDisk) Compatible(systemDisks []string) bool {
	for sdi := range systemDisks {
		if s.availableSystemDisk.Has(systemDisks[sdi]) {
			return true
		}
	}

	return false
}

type Resolver interface {
	FilterInstanceTypesBySystemDisk(context.Context, *v1alpha1.ECSNodeClass, []*cloudprovider.InstanceType) []*cloudprovider.InstanceType
	BuildUserData(context.Context, string, *v1alpha1.ECSNodeClass, *karpv1.NodeClaim, *Options) (string, error)
}

// DefaultResolver is able to fill-in dynamic launch template parameters
type DefaultResolver struct {
	sync.Mutex
	region string
	ecsapi *ecs.Client
	cache  *cache.Cache
}

// NewDefaultResolver constructs a new launch template DefaultResolver
func NewDefaultResolver(region string, ecsapi *ecs.Client, cache *cache.Cache) *DefaultResolver {
	return &DefaultResolver{
		region: region,
		ecsapi: ecsapi,
		cache:  cache,
	}
}

func GetImageFamily(family string, options *Options) ImageFamily {
	switch family {
	case v1alpha1.ImageFamilyContainerOS:
		return &ContainerOS{Options: options}
	case v1alpha1.ImageFamilyAlibabaCloudLinux3:
		return &AlibabaCloudLinux3{Options: options}
	case v1alpha1.ImageFamilyCustom:
		return &Custom{Options: options}
	default:
		return nil
	}
}

// TODO: check system disk stock, currently only checking compatibility
func (r *DefaultResolver) FilterInstanceTypesBySystemDisk(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass, instanceTypes []*cloudprovider.InstanceType) []*cloudprovider.InstanceType {
	r.Lock()
	defer r.Unlock()
	var result []*cloudprovider.InstanceType
	var resultMutex sync.Mutex

	if nodeClass.Spec.SystemDisk == nil || nodeClass.Spec.SystemDisk.Categories == nil {
		return instanceTypes
	}
	expectDiskCategories := nodeClass.Spec.SystemDisk.Categories
	errs := make([]error, len(instanceTypes))
	workqueue.ParallelizeUntil(ctx, 50, len(instanceTypes), func(i int) {
		instanceType := instanceTypes[i]
		if availableSystemDisk, ok := r.cache.Get(instanceType.Name); ok {
			if availableSystemDisk.(*InstanceTypeAvailableSystemDisk).Compatible(expectDiskCategories) {
				resultMutex.Lock()
				result = append(result, instanceTypes[i])
				resultMutex.Unlock()
			}
			return
		}

		availableSystemDisk := newInstanceTypeAvailableSystemDisk()
		if err := r.describeAvailableSystemDisk(&ecs.DescribeAvailableResourceRequest{
			RegionId:            tea.String(r.region),
			DestinationResource: tea.String("SystemDisk"),
			InstanceType:        tea.String(instanceType.Name),
		}, func(resource *ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource) {
			if tea.StringValue(resource.Status) == "Available" &&
				tea.StringValue(resource.Value) != "" {
				availableSystemDisk.AddAvailableSystemDisk(tea.StringValue(resource.Value))
			}
		}); err != nil {
			errs[i] = err
			return
		}
		if availableSystemDisk.Compatible(expectDiskCategories) {
			resultMutex.Lock()
			result = append(result, instanceTypes[i])
			resultMutex.Unlock()
		} else {
			errs[i] = fmt.Errorf("%s is not compatible with NodeClass %s SystemDisk %v", instanceType.Name, nodeClass.Name, expectDiskCategories)
		}
		r.cache.SetDefault(instanceType.Name, availableSystemDisk)
	})
	if err := multierr.Combine(errs...); err != nil {
		log.FromContext(ctx).V(1).Info("filter instance types by system disk", "errs", err)
	}
	return result
}

//nolint:gocyclo
func (r *DefaultResolver) describeAvailableSystemDisk(request *ecs.DescribeAvailableResourceRequest, process func(*ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource)) error {
	runtime := &util.RuntimeOptions{}
	output, err := r.ecsapi.DescribeAvailableResourceWithOptions(request, runtime)
	if err != nil {
		return err
	} else if output == nil || output.Body == nil {
		return fmt.Errorf("unexpected null value was returned")
	} else if output.Body.AvailableZones == nil {
		return alierrors.WithRequestID(tea.StringValue(output.Body.RequestId), fmt.Errorf("unexpected null value was returned"))
	}
	for _, az := range output.Body.AvailableZones.AvailableZone {
		// todo: ClosedWithStock
		if *az.Status != "Available" || *az.StatusCategory != "WithStock" || az.AvailableResources == nil {
			continue
		}

		for _, ar := range az.AvailableResources.AvailableResource {
			if ar.SupportedResources == nil {
				continue
			}
			for _, sr := range ar.SupportedResources.SupportedResource {
				process(sr)
			}
		}
	}
	return nil
}

func (r *DefaultResolver) BuildUserData(ctx context.Context, capacityType string, nodeClass *v1alpha1.ECSNodeClass, nodeClaim *karpv1.NodeClaim, options *Options) (string, error) {
	kubeletCfg := resolveKubeletConfiguration(nodeClass)
	imageFamily := GetImageFamily(nodeClass.ImageFamily(), options)
	if imageFamily == nil {
		return "", fmt.Errorf("ImageFamily not found")
	}
	labels := lo.Assign(nodeClaim.Labels, map[string]string{karpv1.CapacityTypeLabelKey: capacityType})
	taints := lo.Flatten([][]corev1.Taint{
		nodeClaim.Spec.Taints,
		nodeClaim.Spec.StartupTaints,
	})
	if !lo.ContainsBy(taints, func(t corev1.Taint) bool {
		return t.MatchTaint(&karpv1.UnregisteredNoExecuteTaint)
	}) {
		taints = append(taints, karpv1.UnregisteredNoExecuteTaint)
	}
	return imageFamily.UserData(ctx, kubeletCfg, taints, labels, nodeClass.Spec.UserData)
}

func resolveKubeletConfiguration(nodeClass *v1alpha1.ECSNodeClass) *v1alpha1.KubeletConfiguration {
	kubeletConfig := nodeClass.Spec.KubeletConfiguration
	if kubeletConfig == nil {
		kubeletConfig = &v1alpha1.KubeletConfiguration{}
	}

	return kubeletConfig
}
