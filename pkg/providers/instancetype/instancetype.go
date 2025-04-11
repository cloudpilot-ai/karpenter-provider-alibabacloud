/*
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

package instancetype

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	ecsclient "github.com/alibabacloud-go/ecs-20140526/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/mitchellh/hashstructure/v2"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"

	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	kcache "github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/cache"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/cluster"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/pricing"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/utils/alierrors"
)

type Provider interface {
	LivenessProbe(*http.Request) error
	List(context.Context, *v1alpha1.KubeletConfiguration, *v1alpha1.ECSNodeClass) ([]*cloudprovider.InstanceType, error)
	UpdateInstanceTypes(ctx context.Context) error
	UpdateInstanceTypeOfferings(ctx context.Context) error
}

type DefaultProvider struct {
	region          string
	ecsClient       *ecsclient.Client
	pricingProvider pricing.Provider
	ackProvider     cluster.Provider

	// Values stored *before* considering insufficient capacity errors from the unavailableOfferings cache.
	// Fully initialized Instance Types are also cached based on the set of all instance types, zones, unavailableOfferings cache,
	// ECSNodeClass, and kubelet configuration from the NodePool

	muInstanceTypeInfo sync.RWMutex

	instanceTypesInfo []*ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType

	muInstanceTypesOfferings   sync.RWMutex
	instanceTypesOfferings     map[string]sets.Set[string]
	spotInstanceTypesOfferings map[string]sets.Set[string]

	instanceTypesCache *cache.Cache

	unavailableOfferings *kcache.UnavailableOfferings
	cm                   *pretty.ChangeMonitor
	// instanceTypesSeqNum is a monotonically increasing change counter used to avoid the expensive hashing operation on instance types
	instanceTypesSeqNum uint64
	// instanceTypesOfferingsSeqNum is a monotonically increasing change counter used to avoid the expensive hashing operation on instance types
	instanceTypesOfferingsSeqNum uint64
}

func NewDefaultProvider(region string, ecsClient *ecsclient.Client,
	instanceTypesCache *cache.Cache, unavailableOfferingsCache *kcache.UnavailableOfferings,
	pricingProvider pricing.Provider, ackProvider cluster.Provider) *DefaultProvider {
	return &DefaultProvider{
		ecsClient:                  ecsClient,
		region:                     region,
		pricingProvider:            pricingProvider,
		ackProvider:                ackProvider,
		instanceTypesInfo:          []*ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType{},
		instanceTypesOfferings:     map[string]sets.Set[string]{},
		spotInstanceTypesOfferings: map[string]sets.Set[string]{},
		instanceTypesCache:         instanceTypesCache,
		unavailableOfferings:       unavailableOfferingsCache,
		cm:                         pretty.NewChangeMonitor(),
		instanceTypesSeqNum:        0,
	}
}

func (p *DefaultProvider) LivenessProbe(req *http.Request) error {
	if err := p.ackProvider.LivenessProbe(req); err != nil {
		return err
	}
	return p.pricingProvider.LivenessProbe(req)
}

func (p *DefaultProvider) validateState(nodeClass *v1alpha1.ECSNodeClass) error {
	if len(p.instanceTypesInfo) == 0 {
		return errors.New("no instance types found")
	}
	if len(p.instanceTypesOfferings) == 0 {
		return errors.New("no instance types offerings found")
	}
	if len(p.spotInstanceTypesOfferings) == 0 {
		return errors.New("no spot instance types offerings found")
	}
	if len(nodeClass.Status.VSwitches) == 0 {
		return errors.New("no vswitches found")
	}

	return nil
}

func (p *DefaultProvider) List(ctx context.Context, kc *v1alpha1.KubeletConfiguration, nodeClass *v1alpha1.ECSNodeClass) ([]*cloudprovider.InstanceType, error) {
	p.muInstanceTypeInfo.RLock()
	p.muInstanceTypesOfferings.RLock()
	defer p.muInstanceTypeInfo.RUnlock()
	defer p.muInstanceTypesOfferings.RUnlock()

	if kc == nil {
		kc = &v1alpha1.KubeletConfiguration{}
	}
	if err := p.validateState(nodeClass); err != nil {
		return nil, err
	}

	vSwitchsZones := sets.New(lo.Map(nodeClass.Status.VSwitches, func(s v1alpha1.VSwitch, _ int) string {
		return s.ZoneID
	})...)

	// Compute fully initialized instance types hash key
	vSwitchZonesHash, _ := hashstructure.Hash(vSwitchsZones, hashstructure.FormatV2, &hashstructure.HashOptions{SlicesAsSets: true})
	kcHash, _ := hashstructure.Hash(kc, hashstructure.FormatV2, &hashstructure.HashOptions{SlicesAsSets: true})
	key := fmt.Sprintf("%d-%d-%d-%016x-%016x",
		p.instanceTypesSeqNum,
		p.instanceTypesOfferingsSeqNum,
		p.unavailableOfferings.SeqNum,
		vSwitchZonesHash,
		kcHash,
	)

	if item, ok := p.instanceTypesCache.Get(key); ok {
		// TODO: some place changes the Capacity filed, we should find it out and fix it, same with aws provider
		return lo.Map(item.([]*cloudprovider.InstanceType),
			func(item *cloudprovider.InstanceType, _ int) *cloudprovider.InstanceType {
				return &cloudprovider.InstanceType{
					Name:         item.Name,
					Requirements: item.Requirements,
					Offerings:    item.Offerings,
					Capacity:     item.Capacity.DeepCopy(),
					Overhead:     item.Overhead,
				}
			}), nil
	}

	// Get all zones across all offerings
	// We don't use this in the cache key since this is produced from our instanceTypesOfferings which we do cache
	allZones := sets.New[string]()
	for _, offeringZones := range p.instanceTypesOfferings {
		allZones.Insert(offeringZones.UnsortedList()...)
	}

	if p.cm.HasChanged("zones", allZones) {
		log.FromContext(ctx).WithValues("zones", allZones.UnsortedList()).V(1).Info("discovered zones")
	}

	clusterCNI, err := p.ackProvider.GetClusterCNI(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster CNI: %w", err)
	}

	result := lo.Map(p.instanceTypesInfo, func(i *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType, _ int) *cloudprovider.InstanceType {
		zoneData := lo.Map(allZones.UnsortedList(), func(zoneID string, _ int) ZoneData {
			ret := ZoneData{ID: zoneID, Available: true, SpotAvailable: true}
			if !p.instanceTypesOfferings[lo.FromPtr(i.InstanceTypeId)].Has(zoneID) || !vSwitchsZones.Has(zoneID) {
				ret.Available = false
			}
			if !p.spotInstanceTypesOfferings[lo.FromPtr(i.InstanceTypeId)].Has(zoneID) || !vSwitchsZones.Has(zoneID) {
				ret.SpotAvailable = false
			}
			return ret
		})

		// !!! Important !!!
		// Any changes to the values passed into the NewInstanceType method will require making updates to the cache key
		// so that Karpenter is able to cache the set of InstanceTypes based on values that alter the set of instance types
		// !!! Important !!!
		offers := p.createOfferings(ctx, *i.InstanceTypeId, zoneData)
		return NewInstanceType(ctx, i, kc, p.region, nodeClass.Spec.SystemDisk, offers, clusterCNI)
	})

	// Filter out nil values
	result = lo.Compact(result)

	p.instanceTypesCache.SetDefault(key, result)
	return result, nil
}

func (p *DefaultProvider) UpdateInstanceTypes(ctx context.Context) error {
	// DO NOT REMOVE THIS LOCK ----------------------------------------------------------------------------
	// We lock here so that multiple callers to getInstanceTypesOfferings do not result in cache misses and multiple
	// calls to ECS when we could have just made one call.

	p.muInstanceTypeInfo.Lock()
	defer p.muInstanceTypeInfo.Unlock()

	instanceTypes, err := getAllInstanceTypes(p.ecsClient)
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to get instance types")
		return err
	}

	clusterCNI, err := p.ackProvider.GetClusterCNI(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster CNI: %w", err)
	}

	instanceTypes = lo.Filter(instanceTypes,
		func(item *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType, index int) bool {
			switch clusterCNI {
			// TODO: support other network type, please check https://help.aliyun.com/zh/ack/ack-managed-and-ack-dedicated/user-guide/container-network/?spm=a2c4g.11186623.help-menu-85222.d_2_4_3.6d501109uQI315&scm=20140722.H_195424._.OR_help-V_1
			case ClusterCNITypeTerway:
				maxENIPods := (tea.Int32Value(item.EniQuantity) - 1) * tea.Int32Value(item.EniPrivateIpAddressQuantity)
				return maxENIPods >= TerwayMinENIRequirements
			default:
				return true
			}
		})

	if p.cm.HasChanged("instance-types", instanceTypes) {
		// Only update instanceTypesSeqNun with the instance types have been changed
		// This is to not create new keys with duplicate instance types option
		atomic.AddUint64(&p.instanceTypesSeqNum, 1)
		log.FromContext(ctx).WithValues(
			"count", len(instanceTypes)).V(1).Info("discovered instance types")
	}
	p.instanceTypesInfo = instanceTypes
	return nil
}

func (p *DefaultProvider) UpdateInstanceTypeOfferings(ctx context.Context) error {
	// DO NOT REMOVE THIS LOCK ----------------------------------------------------------------------------
	// We lock here so that multiple callers to getInstanceTypesOfferings do not result in cache misses and multiple
	// calls to ECS when we could have just made one call.

	p.muInstanceTypesOfferings.Lock()
	defer p.muInstanceTypesOfferings.Unlock()

	// Get offerings from ECS
	instanceTypesOfferings := map[string]sets.Set[string]{}
	describeAvailableResourceRequest := &ecsclient.DescribeAvailableResourceRequest{
		RegionId:            tea.String(p.region),
		DestinationResource: tea.String("InstanceType"),
	}

	// TODO: we may use other better API in the future.
	resp, err := p.ecsClient.DescribeAvailableResourceWithOptions(
		describeAvailableResourceRequest, &util.RuntimeOptions{})
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to get instance type offerings")
		return err
	}
	if err := processAvailableResourcesResponse(resp, instanceTypesOfferings); err != nil {
		log.FromContext(ctx).Error(err, "failed to process available resource response")
		return err
	}

	if p.cm.HasChanged("instance-type-offering", instanceTypesOfferings) {
		// Only update instanceTypesSeqNun with the instance type offerings  have been changed
		// This is to not create new keys with duplicate instance type offerings option
		atomic.AddUint64(&p.instanceTypesOfferingsSeqNum, 1)
		log.FromContext(ctx).WithValues("instance-type-count", len(instanceTypesOfferings)).V(1).Info("discovered offerings for instance types")
	}
	p.instanceTypesOfferings = instanceTypesOfferings

	spotInstanceTypesOfferings := map[string]sets.Set[string]{}
	describeAvailableResourceRequest = &ecsclient.DescribeAvailableResourceRequest{
		RegionId:            tea.String(p.region),
		DestinationResource: tea.String("InstanceType"),
		SpotStrategy:        tea.String("SpotAsPriceGo"),
	}
	resp, err = p.ecsClient.DescribeAvailableResourceWithOptions(
		describeAvailableResourceRequest, &util.RuntimeOptions{})
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to get spot instance type offerings")
		return err
	}
	if err := processAvailableResourcesResponse(resp, spotInstanceTypesOfferings); err != nil {
		log.FromContext(ctx).Error(err, "failed to process spot instance type offerings")
		return err
	}
	p.spotInstanceTypesOfferings = spotInstanceTypesOfferings
	return nil
}

func processAvailableResourcesResponse(resp *ecsclient.DescribeAvailableResourceResponse, offerings map[string]sets.Set[string]) error {
	if resp == nil || resp.Body == nil {
		return errors.New("DescribeAvailableResourceWithOptions failed to return any instance types")
	} else if resp.Body.AvailableZones == nil || len(resp.Body.AvailableZones.AvailableZone) == 0 {
		return alierrors.WithRequestID(tea.StringValue(resp.Body.RequestId), errors.New("DescribeAvailableResourceWithOptions failed to return any instance types"))
	}

	for _, az := range resp.Body.AvailableZones.AvailableZone {
		// TODO: Later, `ClosedWithStock` will be tested to determine if `ClosedWithStock` should be added.
		// WithStock, ClosedWithStock, WithoutStock, ClosedWithoutStock
		// ClosedWithStock means there is available capacity, just the provider will not add more capacity.
		if tea.StringValue(az.StatusCategory) != "WithStock" &&
			tea.StringValue(az.StatusCategory) != "ClosedWithStock" {
			continue
		}
		processAvailableResources(az, offerings)
	}
	return nil
}

func processAvailableResources(az *ecsclient.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZone, instanceTypesOfferings map[string]sets.Set[string]) {
	if az.AvailableResources == nil || az.AvailableResources.AvailableResource == nil {
		return
	}

	for _, ar := range az.AvailableResources.AvailableResource {
		if ar.SupportedResources == nil || ar.SupportedResources.SupportedResource == nil {
			continue
		}

		for _, sr := range ar.SupportedResources.SupportedResource {
			// TODO: Later, `ClosedWithStock` will be tested to determine if `ClosedWithStock` should be added.
			// WithStock, ClosedWithStock, WithoutStock, ClosedWithoutStock
			// ClosedWithStock means there is available capacity, just the provider will not add more capacity.
			if tea.StringValue(sr.StatusCategory) != "WithStock" &&
				tea.StringValue(sr.StatusCategory) != "ClosedWithStock" {
				continue
			}
			if _, ok := instanceTypesOfferings[*sr.Value]; !ok {
				instanceTypesOfferings[*sr.Value] = sets.New[string]()
			}
			instanceTypesOfferings[*sr.Value].Insert(*az.ZoneId)
		}
	}
}

func getAllInstanceTypes(client *ecsclient.Client) ([]*ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType, error) {
	var InstanceTypes []*ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType

	describeInstanceTypesRequest := &ecsclient.DescribeInstanceTypesRequest{
		/*
			Reference: https://api.aliyun.com/api/Ecs/2014-05-26/DescribeInstanceTypes caveat:
			The maximum value of Max Results (maximum number of entries per page) parameter is 100,
			for users who have called this API in 2022, the maximum value of Max Results parameter is still 1600,
			on and after November 15, 2023, we will reduce the maximum value of Max Results parameter to 100 for all users,
			and no longer support 1600, if you do not pass the Next Token parameter for paging when you call this API,
			only the first page of the specification (no more than 100 items) will be returned by default.
		*/
		MaxResults: tea.Int64(100),
	}

	for {
		resp, err := client.DescribeInstanceTypesWithOptions(describeInstanceTypesRequest, &util.RuntimeOptions{})
		if err != nil {
			return nil, err
		}

		if resp == nil || resp.Body == nil || resp.Body.InstanceTypes == nil || len(resp.Body.InstanceTypes.InstanceType) == 0 {
			break
		}

		describeInstanceTypesRequest.NextToken = resp.Body.NextToken
		InstanceTypes = append(InstanceTypes, resp.Body.InstanceTypes.InstanceType...)

		if resp.Body.NextToken == nil || *resp.Body.NextToken == "" {
			break
		}
	}

	return InstanceTypes, nil
}

// createOfferings creates a set of mutually exclusive offerings for a given instance type. This provider maintains an
// invariant that each offering is mutually exclusive. Specifically, there is an offering for each permutation of zone
// and capacity type. ZoneID is also injected into the offering requirements, when available, but there is a 1-1
// mapping between zone and zoneID so this does not change the number of offerings.
//
// Each requirement on the offering is guaranteed to have a single value. To get the value for a requirement on an
// offering, you can do the following thanks to this invariant:
//
//	offering.Requirements.Get(v1.TopologyLabelZone).Any()
func (p *DefaultProvider) createOfferings(_ context.Context, instanceType string, zones []ZoneData) []cloudprovider.Offering {
	var offerings []cloudprovider.Offering
	for _, zone := range zones {
		odPrice, odOK := p.pricingProvider.OnDemandPrice(instanceType)
		spotPrice, spotOK := p.pricingProvider.SpotPrice(instanceType, zone.ID)

		if odOK {
			isUnavailable := p.unavailableOfferings.IsUnavailable(instanceType, zone.ID, karpv1.CapacityTypeOnDemand)
			offeringAvailable := !isUnavailable && zone.Available

			offerings = append(offerings, p.createOffering(zone.ID, karpv1.CapacityTypeOnDemand, odPrice, offeringAvailable))
		}

		if spotOK {
			isUnavailable := p.unavailableOfferings.IsUnavailable(instanceType, zone.ID, karpv1.CapacityTypeSpot)
			offeringAvailable := !isUnavailable && zone.SpotAvailable

			offerings = append(offerings, p.createOffering(zone.ID, karpv1.CapacityTypeSpot, spotPrice, offeringAvailable))
		}
	}

	return offerings
}

func (p *DefaultProvider) createOffering(zone, capacityType string, price float64, available bool) cloudprovider.Offering {
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

func (p *DefaultProvider) Reset() {
	p.instanceTypesInfo = []*ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType{}
	p.instanceTypesOfferings = map[string]sets.Set[string]{}
	p.instanceTypesCache.Flush()
}
