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
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"strings"
	"time"

	ecsclient "github.com/alibabacloud-go/ecs-20140526/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	"go.uber.org/multierr"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
	"sigs.k8s.io/karpenter/pkg/utils/resources"

	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/apis"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	kcache "github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/cache"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/operator/options"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/ack"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/imagefamily"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/vswitch"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/utils/alierrors"
)

const (
	// TODO: After that open up the configuration options
	instanceTypeFlexibilityThreshold = 5 // falling back to on-demand without flexibility risks insufficient capacity errors
	maxInstanceTypes                 = 20
	instanceCacheExpiration          = 15 * time.Second
)

type Provider interface {
	Create(context.Context, *v1alpha1.ECSNodeClass, *karpv1.NodeClaim, []*cloudprovider.InstanceType) (*Instance, error)
	Get(context.Context, string) (*Instance, error)
	List(context.Context) ([]*Instance, error)
	Delete(context.Context, string) error
	CreateTags(context.Context, string, map[string]string) error
}

type DefaultProvider struct {
	ecsClient            *ecsclient.Client
	region               string
	instanceCache        *cache.Cache
	unavailableOfferings *kcache.UnavailableOfferings

	imageFamilyResolver imagefamily.Resolver
	vSwitchProvider     vswitch.Provider
	ackProvider         ack.Provider
	createLimiter       *rate.Limiter
}

func NewDefaultProvider(ctx context.Context, region string, ecsClient *ecsclient.Client, unavailableOfferings *kcache.UnavailableOfferings,
	imageFamilyResolver imagefamily.Resolver, vSwitchProvider vswitch.Provider,
	ackProvider ack.Provider,
) *DefaultProvider {
	p := &DefaultProvider{
		ecsClient:            ecsClient,
		region:               region,
		instanceCache:        cache.New(instanceCacheExpiration, instanceCacheExpiration),
		unavailableOfferings: unavailableOfferings,
		createLimiter:        rate.NewLimiter(rate.Limit(1), options.FromContext(ctx).APGCreationQPS),
		imageFamilyResolver:  imageFamilyResolver,
		vSwitchProvider:      vSwitchProvider,
		ackProvider:          ackProvider,
	}

	return p
}

func (p *DefaultProvider) Create(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass, nodeClaim *karpv1.NodeClaim,
	instanceTypes []*cloudprovider.InstanceType,
) (*Instance, error) {
	// Wait for rate limiter
	if err := p.createLimiter.Wait(ctx); err != nil {
		log.FromContext(ctx).Error(err, "rate limit exceeded")
		return nil, fmt.Errorf("rate limit exceeded: %w", err)
	}
	schedulingRequirements := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...)
	// Only filter the instances if there are no minValues in the requirement.
	if !schedulingRequirements.HasMinValues() {
		instanceTypes = p.filterInstanceTypes(nodeClaim, instanceTypes)
	}
	instanceTypes, err := cloudprovider.InstanceTypes(instanceTypes).Truncate(schedulingRequirements, maxInstanceTypes)
	if err != nil {
		return nil, fmt.Errorf("truncating instance types, %w", err)
	}
	tags := getTags(ctx, nodeClass, nodeClaim)
	launchInstance, createAutoProvisioningGroupRequest, err := p.launchInstance(ctx, nodeClass, nodeClaim, instanceTypes, tags)
	if err != nil {
		return nil, err
	}

	return NewInstanceFromProvisioningGroup(launchInstance, createAutoProvisioningGroupRequest, p.region), nil
}

func (p *DefaultProvider) Get(ctx context.Context, id string) (*Instance, error) {
	if instance, ok := p.instanceCache.Get(id); ok {
		return instance.(*Instance), nil
	}

	// List all instances to update the cache
	instances, err := p.List(ctx)
	if err != nil {
		return nil, err
	}
	p.syncAllInstances(instances)

	currentInstance, ok := p.instanceCache.Get(id)
	if ok {
		return currentInstance.(*Instance), nil
	}

	return nil, cloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("instance not found"))
}

func (p *DefaultProvider) list(ctx context.Context) ([]*Instance, error) {
	var instances []*Instance

	describeInstancesRequest := &ecsclient.DescribeInstancesRequest{
		Tag: []*ecsclient.DescribeInstancesRequestTag{
			// TODO: add karpenter.xxx.xxx tags
			{
				Key:   tea.String(fmt.Sprintf("kubernetes.io/cluster/%s", options.FromContext(ctx).ClusterID)),
				Value: tea.String("owned"),
			},
		},
		RegionId: tea.String(p.region),
	}

	runtime := &util.RuntimeOptions{}

	for {
		// TODO: limit 1000
		/* Refer https://api.aliyun.com/api/Ecs/2014-05-26/DescribeInstances
		If you use one tag to filter resources, the number of resources queried under that tag cannot exceed 1000;
		if you use multiple tags to filter resources, the number of resources queried with multiple tags bound at the
		same time cannot exceed 1000. If the number of resources exceeds 1000, use the ListTagResources interface to query.
		*/
		resp, err := p.ecsClient.DescribeInstancesWithOptions(describeInstancesRequest, runtime)
		if err != nil {
			return nil, err
		}

		if resp == nil || resp.Body == nil || resp.Body.Instances == nil || len(resp.Body.Instances.Instance) == 0 {
			break
		}

		describeInstancesRequest.NextToken = resp.Body.NextToken
		for i := range resp.Body.Instances.Instance {
			instances = append(instances, NewInstance(resp.Body.Instances.Instance[i]))
		}

		if resp.Body.NextToken == nil || *resp.Body.NextToken == "" {
			break
		}
	}

	return instances, nil
}

func (p *DefaultProvider) List(ctx context.Context) ([]*Instance, error) {
	instances, err := p.list(ctx)
	if err != nil {
		return nil, err
	}
	p.syncAllInstances(instances)

	return instances, nil
}

func (p *DefaultProvider) Delete(ctx context.Context, id string) error {
	instance, err := p.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting instance, %w", err)
	}

	// For follow state, the API will return an error, let's return NotSupportedError
	if instance.Status == InstanceStatusStarting || instance.Status == InstanceStatusPending {
		return NewInstanceStateOperationNotSupportedError(id)
	}

	deleteInstanceRequest := &ecsclient.DeleteInstanceRequest{
		InstanceId:            tea.String(id),
		Force:                 tea.Bool(true),
		TerminateSubscription: tea.Bool(true),
	}

	runtime := &util.RuntimeOptions{}
	if _, err := p.ecsClient.DeleteInstanceWithOptions(deleteInstanceRequest, runtime); err != nil {
		if alierrors.IsNotFound(err) {
			return cloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("instance already terminated"))
		}

		if _, e := p.Get(ctx, id); e != nil {
			if cloudprovider.IsNodeClaimNotFoundError(e) {
				return e
			}
			err = multierr.Append(err, e)
		}

		return fmt.Errorf("terminating instance id: %s, %w", id, err)
	}

	p.instanceCache.Delete(id)
	return nil
}

func (p *DefaultProvider) CreateTags(ctx context.Context, id string, tags map[string]string) error {
	ecsTags := make([]*ecsclient.AddTagsRequestTag, 0, len(tags))
	for k, v := range tags {
		ecsTags = append(ecsTags, &ecsclient.AddTagsRequestTag{
			Key:   tea.String(k),
			Value: tea.String(v),
		})
	}

	addTagsRequest := &ecsclient.AddTagsRequest{
		RegionId:     tea.String(p.region),
		ResourceType: tea.String("instance"),
		ResourceId:   tea.String(id),
		Tag:          ecsTags,
	}

	runtime := &util.RuntimeOptions{}
	if _, err := p.ecsClient.AddTagsWithOptions(addTagsRequest, runtime); err != nil {
		if alierrors.IsNotFound(err) {
			return cloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("tagging instance, %w", err))
		}
		return fmt.Errorf("tagging instance, %w", err)
	}

	instances, err := p.list(ctx)
	if err != nil {
		return err
	}
	p.syncAllInstances(instances)

	return err
}

// filterInstanceTypes is used to provide filtering on the list of potential instance types to further limit it to those
// that make the most sense given our specific Alibaba Cloud cloudprovider.
func (p *DefaultProvider) filterInstanceTypes(nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType) []*cloudprovider.InstanceType {
	instanceTypes = filterExoticInstanceTypes(instanceTypes)
	// If we could potentially launch either a spot or on-demand node, we want to filter out the spot instance types that
	// are more expensive than the cheapest on-demand type.
	if p.isMixedCapacityLaunch(nodeClaim, instanceTypes) {
		instanceTypes = filterUnwantedSpot(instanceTypes)
	}
	return instanceTypes
}

// filterExoticInstanceTypes is used to eliminate less desirable instance types (like GPUs) from the list of possible instance types when
// a set of more appropriate instance types would work. If a set of more desirable instance types is not found, then the original slice
// of instance types are returned.
func filterExoticInstanceTypes(instanceTypes []*cloudprovider.InstanceType) []*cloudprovider.InstanceType {
	var genericInstanceTypes []*cloudprovider.InstanceType
	for _, it := range instanceTypes {
		// deprioritize metal even if our opinionated filter isn't applied due to something like an instance family
		// requirement
		if _, ok := lo.Find(it.Requirements.Get(v1alpha1.LabelInstanceSize).Values(), func(size string) bool { return strings.Contains(size, "metal") }); ok {
			continue
		}
		if !resources.IsZero(it.Capacity[v1alpha1.ResourceAMDGPU]) ||
			!resources.IsZero(it.Capacity[v1alpha1.ResourceNVIDIAGPU]) {
			continue
		}
		genericInstanceTypes = append(genericInstanceTypes, it)
	}
	// if we got some subset of instance types, then prefer to use those
	if len(genericInstanceTypes) != 0 {
		return genericInstanceTypes
	}
	return instanceTypes
}

// isMixedCapacityLaunch returns true if nodepools and available offerings could potentially allow either a spot or
// and on-demand node to launch
func (p *DefaultProvider) isMixedCapacityLaunch(nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType) bool {
	requirements := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...)
	// requirements must allow both
	if !requirements.Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeSpot) ||
		!requirements.Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeOnDemand) {
		return false
	}
	hasSpotOfferings := false
	hasODOffering := false
	if requirements.Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeSpot) {
		for _, instanceType := range instanceTypes {
			for _, offering := range instanceType.Offerings.Available() {
				if requirements.Compatible(offering.Requirements, scheduling.AllowUndefinedWellKnownLabels) != nil {
					continue
				}
				if offering.Requirements.Get(karpv1.CapacityTypeLabelKey).Any() == karpv1.CapacityTypeSpot {
					hasSpotOfferings = true
				} else {
					hasODOffering = true
				}
			}
		}
	}
	return hasSpotOfferings && hasODOffering
}

// filterUnwantedSpot is used to filter out spot types that are more expensive than the cheapest on-demand type that we
// could launch during mixed capacity-type launches
func filterUnwantedSpot(instanceTypes []*cloudprovider.InstanceType) []*cloudprovider.InstanceType {
	cheapestOnDemand := math.MaxFloat64
	// first, find the price of our cheapest available on-demand instance type that could support this node
	for _, it := range instanceTypes {
		for _, o := range it.Offerings.Available() {
			if o.Requirements.Get(karpv1.CapacityTypeLabelKey).Any() == karpv1.CapacityTypeOnDemand && o.Price < cheapestOnDemand {
				cheapestOnDemand = o.Price
			}
		}
	}

	// Filter out any types where the cheapest offering, which should be spot, is more expensive than the cheapest
	// on-demand instance type that would have worked. This prevents us from getting a larger more-expensive spot
	// instance type compared to the cheapest sufficiently large on-demand instance type
	instanceTypes = lo.Filter(instanceTypes, func(item *cloudprovider.InstanceType, index int) bool {
		available := item.Offerings.Available()
		if len(available) == 0 {
			return false
		}
		return available.Cheapest().Price <= cheapestOnDemand
	})
	return instanceTypes
}

func getTags(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass, nodeClaim *karpv1.NodeClaim) map[string]string {
	staticTags := map[string]string{
		fmt.Sprintf("kubernetes.io/cluster/%s", options.FromContext(ctx).ClusterID): "owned",
		karpv1.NodePoolLabelKey:     nodeClaim.Labels[karpv1.NodePoolLabelKey],
		v1alpha1.ECSClusterIDTagKey: options.FromContext(ctx).ClusterID,
		v1alpha1.LabelNodeClass:     nodeClass.Name,
	}
	return lo.Assign(nodeClass.Spec.Tags, staticTags)
}

func (p *DefaultProvider) launchInstance(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass, nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType,
	tags map[string]string,
) (*ecsclient.CreateAutoProvisioningGroupResponseBodyLaunchResultsLaunchResult, *ecsclient.CreateAutoProvisioningGroupRequest, error) {
	if err := p.checkODFallback(nodeClaim, instanceTypes); err != nil {
		log.FromContext(ctx).Error(err, "failed while checking on-demand fallback")
	}
	capacityType := p.getCapacityType(nodeClaim, instanceTypes)
	zonalVSwitchs, err := p.vSwitchProvider.ZonalVSwitchesForLaunch(ctx, nodeClass, instanceTypes, capacityType)
	if err != nil {
		return nil, nil, fmt.Errorf("getting vSwitches, %w", err)
	}

	createAutoProvisioningGroupRequest, err := p.getProvisioningGroup(ctx, nodeClass, nodeClaim, instanceTypes, zonalVSwitchs, capacityType, tags)
	if err != nil {
		return nil, nil, fmt.Errorf("getting provisioning group, %w", err)
	}

	runtime := &util.RuntimeOptions{}
	resp, err := p.ecsClient.CreateAutoProvisioningGroupWithOptions(createAutoProvisioningGroupRequest, runtime)
	if err != nil {
		return nil, nil, fmt.Errorf("creating auto provisioning group, %w", err)
	}

	p.updateUnavailableOfferingsCache(ctx, resp, capacityType)

	if err := createAutoProvisioningGroupResponseHandler(resp); err != nil {
		return nil, nil, err
	}

	return resp.Body.LaunchResults.LaunchResult[0], createAutoProvisioningGroupRequest, nil
}

const (
	ErrCodeNoInstanceStock        = "NoInstanceStock"
	ErrCodeOperationDeniedNoStock = "OperationDenied.NoStock"
)

func (p *DefaultProvider) updateUnavailableOfferingsCache(ctx context.Context, resp *ecsclient.CreateAutoProvisioningGroupResponse, capacityType string) {
	if resp == nil || resp.Body == nil || resp.Body.LaunchResults == nil || len(resp.Body.LaunchResults.LaunchResult) == 0 {
		return
	}

	for lri := range resp.Body.LaunchResults.LaunchResult {
		if resp.Body.LaunchResults.LaunchResult[lri] == nil {
			continue
		}

		if (tea.StringValue(resp.Body.LaunchResults.LaunchResult[lri].ErrorCode) == ErrCodeNoInstanceStock || tea.StringValue(resp.Body.LaunchResults.LaunchResult[lri].ErrorCode) == ErrCodeOperationDeniedNoStock) &&
			tea.StringValue(resp.Body.LaunchResults.LaunchResult[lri].InstanceType) != "" &&
			tea.StringValue(resp.Body.LaunchResults.LaunchResult[lri].ZoneId) != "" {
			p.unavailableOfferings.MarkUnavailable(
				ctx,
				tea.StringValue(resp.Body.LaunchResults.LaunchResult[lri].ErrorMsg),
				tea.StringValue(resp.Body.LaunchResults.LaunchResult[lri].InstanceType),
				tea.StringValue(resp.Body.LaunchResults.LaunchResult[lri].ZoneId),
				capacityType)
		}
	}
}

func createAutoProvisioningGroupResponseHandler(resp *ecsclient.CreateAutoProvisioningGroupResponse) error {
	if resp == nil || resp.Body == nil || resp.Body.LaunchResults == nil {
		return fmt.Errorf("invalid response when creating auto provision group: %s", tea.Prettify(resp))
	}

	if tea.Int32Value(resp.StatusCode) != http.StatusOK {
		return fmt.Errorf("unexpected status code %d when creating auto provision group: %s",
			tea.Int32Value(resp.StatusCode), tea.Prettify(resp))
	}

	launchResults := resp.Body.LaunchResults.LaunchResult
	if len(launchResults) == 0 {
		return fmt.Errorf("no launch results found in response: %s", tea.Prettify(resp))
	}

	launchResult := launchResults[0]

	if tea.StringValue(launchResult.ErrorCode) == ErrCodeNoInstanceStock || tea.StringValue(launchResult.ErrorCode) == ErrCodeOperationDeniedNoStock {
		return cloudprovider.NewInsufficientCapacityError(alierrors.WithRequestID(tea.StringValue(resp.Body.RequestId),
			fmt.Errorf("failed to launch instance: errorCode=%s, errorMessage=%s",
				tea.StringValue(launchResult.ErrorCode), tea.StringValue(launchResult.ErrorMsg))))
	}

	if launchResult.InstanceIds == nil || len(launchResult.InstanceIds.InstanceId) == 0 {
		return alierrors.WithRequestID(tea.StringValue(resp.Body.RequestId),
			fmt.Errorf("failed to launch instance: errorCode=%s, errorMessage=%s",
				tea.StringValue(launchResult.ErrorCode), tea.StringValue(launchResult.ErrorMsg)))
	}

	return nil
}

// getCapacityType selects spot if both constraints are flexible and there is an
// available offering. The Alibaba Cloud Provider defaults to [ on-demand ], so spot
// must be explicitly included in capacity type requirements.
func (p *DefaultProvider) getCapacityType(nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType) string {
	requirements := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...)
	if requirements.Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeSpot) {
		requirements[karpv1.CapacityTypeLabelKey] = scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeSpot)
		for _, instanceType := range instanceTypes {
			for _, offering := range instanceType.Offerings.Available() {
				if requirements.Compatible(offering.Requirements, scheduling.AllowUndefinedWellKnownLabels) == nil {
					return karpv1.CapacityTypeSpot
				}
			}
		}
	}
	return karpv1.CapacityTypeOnDemand
}

// mapToInstanceTypes returns a map of ImageIDs that are the most recent on creationDate to compatible instancetypes
func mapToInstanceTypes(instanceTypes []*cloudprovider.InstanceType, images []v1alpha1.Image) map[string]string {
	imageIDs := map[string]string{}
	for _, instanceType := range instanceTypes {
		for _, img := range images {
			if err := instanceType.Requirements.Compatible(
				scheduling.NewNodeSelectorRequirements(img.Requirements...),
				scheduling.AllowUndefinedWellKnownLabels,
			); err == nil {
				imageIDs[instanceType.Name] = img.ID
				break
			}
		}
	}
	return imageIDs
}

func resolveKubeletConfiguration(nodeClass *v1alpha1.ECSNodeClass) *v1alpha1.KubeletConfiguration {
	kubeletConfig := nodeClass.Spec.KubeletConfiguration
	if kubeletConfig == nil {
		kubeletConfig = &v1alpha1.KubeletConfiguration{}
	}

	return kubeletConfig
}

func (p *DefaultProvider) getProvisioningGroup(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass, nodeClaim *karpv1.NodeClaim,
	instanceTypes []*cloudprovider.InstanceType, zonalVSwitchs map[string]*vswitch.VSwitch, capacityType string, tags map[string]string,
) (*ecsclient.CreateAutoProvisioningGroupRequest, error) {
	requirements := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...)

	instanceTypes = p.imageFamilyResolver.FilterInstanceTypesBySystemDisk(ctx, nodeClass, instanceTypes)
	if len(instanceTypes) == 0 {
		return nil, errors.New("no instance types match the system disk requirements")
	}

	mappedImages := mapToInstanceTypes(instanceTypes, nodeClass.Status.Images)

	requirements[karpv1.CapacityTypeLabelKey] = scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, capacityType)
	var launchTemplateConfigs []*ecsclient.CreateAutoProvisioningGroupRequestLaunchTemplateConfig
	for _, instanceType := range instanceTypes {
		if len(launchTemplateConfigs) > maxInstanceTypes-1 {
			break
		}

		vSwitchID := p.getVSwitchID(instanceType, zonalVSwitchs, requirements, capacityType, nodeClass.Spec.VSwitchSelectionPolicy)
		if vSwitchID == "" {
			continue
		}

		launchTemplateConfig := &ecsclient.CreateAutoProvisioningGroupRequestLaunchTemplateConfig{
			InstanceType:     tea.String(instanceType.Name),
			VSwitchId:        &vSwitchID,
			WeightedCapacity: tea.Float64(1),
		}

		launchTemplateConfigs = append(launchTemplateConfigs, launchTemplateConfig)
	}

	if len(launchTemplateConfigs) == 0 {
		return nil, errors.New("no capacity offerings are currently available given the constraints")
	}

	reqTags := make([]*ecsclient.CreateAutoProvisioningGroupRequestLaunchConfigurationTag, 0, len(tags))
	for k, v := range tags {
		reqTags = append(reqTags, &ecsclient.CreateAutoProvisioningGroupRequestLaunchConfigurationTag{
			Key:   tea.String(k),
			Value: tea.String(v),
		})
	}

	kubeletCfg := resolveKubeletConfiguration(nodeClass)
	labels := lo.Assign(nodeClaim.Labels, map[string]string{karpv1.CapacityTypeLabelKey: capacityType})
	userData, err := p.ackProvider.GetNodeRegisterScript(ctx, labels, kubeletCfg)
	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to resolve user data for node")
		return nil, err
	}

	securityGroupIDs := lo.Map(nodeClass.Status.SecurityGroups, func(item v1alpha1.SecurityGroup, index int) *string {
		return tea.String(item.ID)
	})

	systemDisk := nodeClass.Spec.SystemDisk
	if systemDisk == nil {
		systemDisk = imagefamily.DefaultSystemDisk.DeepCopy()
	}

	imageID, ok := mappedImages[instanceTypes[0].Name]
	if !ok {
		return nil, errors.New("matching image not found")
	}

	createAutoProvisioningGroupRequest := &ecsclient.CreateAutoProvisioningGroupRequest{
		RegionId:                        tea.String(p.region),
		TotalTargetCapacity:             tea.String("1"),
		SpotAllocationStrategy:          tea.String("lowest-price"),
		PayAsYouGoAllocationStrategy:    tea.String("lowest-price"),
		LaunchTemplateConfig:            launchTemplateConfigs,
		ExcessCapacityTerminationPolicy: tea.String("termination"),
		AutoProvisioningGroupType:       tea.String("instant"),
		LaunchConfiguration: &ecsclient.CreateAutoProvisioningGroupRequestLaunchConfiguration{
			// TODO: we should set image id for each instance types after alibabacloud supports
			ImageId:                    tea.String(imageID),
			UserData:                   tea.String(userData),
			ResourceGroupId:            tea.String(nodeClass.Spec.ResourceGroupID),
			SecurityGroupIds:           securityGroupIDs,
			SystemDiskSize:             systemDisk.Size,
			SystemDiskPerformanceLevel: systemDisk.PerformanceLevel,
			Tag:                        reqTags,
		},
		// Add this tag to auto-provisioning-group, alibabacloud will monitor the requests and enhance the stability
		Tag: []*ecsclient.CreateAutoProvisioningGroupRequestTag{
			{Key: tea.String(apis.Group + "/autoprovisiongroup"), Value: tea.String("true")},
		},

		SystemDiskConfig: lo.Map(systemDisk.Categories, func(category string, _ int) *ecsclient.CreateAutoProvisioningGroupRequestSystemDiskConfig {
			return &ecsclient.CreateAutoProvisioningGroupRequestSystemDiskConfig{
				DiskCategory: &category,
			}
		}),
	}

	if capacityType == karpv1.CapacityTypeSpot {
		createAutoProvisioningGroupRequest.SpotTargetCapacity = tea.String("1")
		createAutoProvisioningGroupRequest.PayAsYouGoTargetCapacity = tea.String("0")
	} else {
		createAutoProvisioningGroupRequest.SpotTargetCapacity = tea.String("0")
		createAutoProvisioningGroupRequest.PayAsYouGoTargetCapacity = tea.String("1")
	}

	return createAutoProvisioningGroupRequest, nil
}

func (p *DefaultProvider) checkODFallback(nodeClaim *karpv1.NodeClaim, instanceTypes []*cloudprovider.InstanceType) error {
	// only evaluate for on-demand fallback if the capacity type for the request is OD and both OD and spot are allowed in requirements
	if p.getCapacityType(nodeClaim, instanceTypes) != karpv1.CapacityTypeOnDemand ||
		!scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...).Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeSpot) {
		return nil
	}

	if len(instanceTypes) < instanceTypeFlexibilityThreshold {
		return fmt.Errorf("at least %d instance types are recommended when flexible to spot but requesting on-demand, "+
			"the current provisioning request only has %d instance type options", instanceTypeFlexibilityThreshold, len(instanceTypes))
	}
	return nil
}

func (p *DefaultProvider) getVSwitchID(instanceType *cloudprovider.InstanceType,
	zonalVSwitchs map[string]*vswitch.VSwitch, reqs scheduling.Requirements, capacityType string, vSwitchSelectionPolicy string,
) string {
	cheapestVSwitchID := ""
	cheapestPrice := math.MaxFloat64

	if capacityType == karpv1.CapacityTypeOnDemand || vSwitchSelectionPolicy == v1alpha1.VSwitchSelectionPolicyBalanced {
		// For on-demand, randomly select a zone's vswitch
		zoneIDs := lo.Keys(zonalVSwitchs)
		if len(zoneIDs) > 0 {
			randomIndex, _ := rand.Int(rand.Reader, big.NewInt(int64(len(zoneIDs))))
			return zonalVSwitchs[zoneIDs[randomIndex.Int64()]].ID
		}
	}

	// For different AZ, the spot price may differ. So we need to get the cheapest vSwitch in the zone
	for i := range instanceType.Offerings {
		if reqs.Compatible(instanceType.Offerings[i].Requirements, scheduling.AllowUndefinedWellKnownLabels) != nil {
			continue
		}

		vswitch, ok := zonalVSwitchs[instanceType.Offerings[i].Requirements.Get(corev1.LabelTopologyZone).Any()]
		if !ok {
			continue
		}
		if instanceType.Offerings[i].Price < cheapestPrice {
			cheapestVSwitchID = vswitch.ID
			cheapestPrice = instanceType.Offerings[i].Price
		}
	}

	return cheapestVSwitchID
}

func (p *DefaultProvider) syncAllInstances(instances []*Instance) {
	for _, instance := range instances {
		p.instanceCache.Set(instance.ID, instance, cache.DefaultExpiration)
	}
}
