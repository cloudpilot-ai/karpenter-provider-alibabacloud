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
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	ecsclient "github.com/alibabacloud-go/ecs-20140526/v4/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
	"sigs.k8s.io/karpenter/pkg/utils/resources"

	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/operator/options"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/imagefamily"
)

var (
	instanceTypeScheme = regexp.MustCompile(`^ecs\.([a-z]+)(\-[0-9]+tb)?([0-9]+).*`)
)

const (
	GiBMiBRatio              = 1024
	MiBByteRatio             = 1024 * 1024
	TerwayMinENIRequirements = 11
	BaseHostNetworkPods      = 3
	FlannelDefaultPods       = 256

	ClusterCNITypeTerway  = "terway-eniip"
	ClusterCNITypeFlannel = "Flannel"
)

type ZoneData struct {
	ID string
	// Available represents OnDemand capacity
	Available bool
	// SpotAvailable represents Spot capacity
	SpotAvailable bool
}

func calculateResourceOverhead(pods, cpuM, memoryMi int64) corev1.ResourceList {
	// referring to: https://help.aliyun.com/zh/ack/ack-managed-and-ack-dedicated/user-guide/resource-reservation-policy#0f5ffe176df7q
	// CPU overhead calculation
	cpuOverHead := calculateCPUOverhead(cpuM)

	// TODO: In a real environment, the formula does not produce accurate results,
	// consistently yielding values that are 200MiB larger than expected.
	// Memory overhead: min(11*pods + 255, memoryMi*0.25)
	memoryOverHead := int64(math.Min(float64(11*pods+255), float64(memoryMi)*0.25)) + 200

	return corev1.ResourceList{
		corev1.ResourceCPU:    *resource.NewMilliQuantity(cpuOverHead, resource.DecimalSI),
		corev1.ResourceMemory: *resources.Quantity(fmt.Sprintf("%dMi", memoryOverHead)),
	}
}

// thresholds defines CPU overhead thresholds and their corresponding percentages
var thresholds = [...]struct {
	cores    int64
	overhead float64
}{
	{1000, 0.06},
	{2000, 0.01},
	{3000, 0.005},
	{4000, 0.005},
}

func calculateCPUOverhead(cpuM int64) int64 {
	var cpuOverHead int64

	// Calculate overhead for each threshold
	for _, t := range thresholds {
		if cpuM >= t.cores {
			cpuOverHead += int64(1000 * t.overhead)
		}
	}

	// Additional overhead for CPU > 4 cores (0.25%)
	if cpuM > 4000 {
		cpuOverHead += int64(float64(cpuM-4000) * 0.0025)
	}

	return cpuOverHead
}

func NewInstanceType(ctx context.Context,
	info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType,
	kc *v1alpha1.KubeletConfiguration, region string, systemDisk *v1alpha1.SystemDisk,
	offerings cloudprovider.Offerings, clusterCNI string) *cloudprovider.InstanceType {
	if offerings == nil {
		return nil
	}

	it := &cloudprovider.InstanceType{
		Name:         *info.InstanceTypeId,
		Requirements: computeRequirements(info, offerings, region),
		Offerings:    offerings,
		Capacity:     computeCapacity(ctx, info, kc.MaxPods, kc.PodsPerCore, systemDisk, clusterCNI),
		Overhead: &cloudprovider.InstanceTypeOverhead{
			KubeReserved: corev1.ResourceList{
				// todo
			},
			SystemReserved: corev1.ResourceList{
				// todo
			},
			EvictionThreshold: corev1.ResourceList{},
		},
	}

	// Follow KubeReserved/SystemReserved/EvictionThreshold will be merged, so we can set only one overhead totally
	it.Overhead.KubeReserved = calculateResourceOverhead(it.Capacity.Pods().Value(),
		it.Capacity.Cpu().MilliValue(), extractMemory(info).Value()/MiBByteRatio)
	if it.Requirements.Compatible(scheduling.NewRequirements(scheduling.NewRequirement(corev1.LabelOSStable, corev1.NodeSelectorOpIn, string(corev1.Windows)))) == nil {
		it.Capacity[v1alpha1.ResourcePrivateIPv4Address] = *privateIPv4Address(info)
	}
	return it
}

func extractECSArch(unFormatedArch string) string {
	switch unFormatedArch {
	case "X86":
		return "amd64"
	case "ARM":
		return "arm64"
	default:
		return "amd64"
	}
}

//nolint:gocyclo
func computeRequirements(info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType, offerings cloudprovider.Offerings, region string) scheduling.Requirements {
	requirements := scheduling.NewRequirements(
		// Well Known Upstream
		scheduling.NewRequirement(corev1.LabelInstanceTypeStable, corev1.NodeSelectorOpIn, tea.StringValue(info.InstanceTypeId)),
		scheduling.NewRequirement(corev1.LabelArchStable, corev1.NodeSelectorOpIn, extractECSArch(tea.StringValue(info.CpuArchitecture))),
		scheduling.NewRequirement(corev1.LabelOSStable, corev1.NodeSelectorOpIn, string(corev1.Linux)),
		scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, lo.Map(offerings.Available(), func(o cloudprovider.Offering, _ int) string {
			return o.Requirements.Get(corev1.LabelTopologyZone).Any()
		})...),
		scheduling.NewRequirement(corev1.LabelTopologyRegion, corev1.NodeSelectorOpIn, region),
		scheduling.NewRequirement(corev1.LabelWindowsBuild, corev1.NodeSelectorOpDoesNotExist),
		// Well Known to Karpenter
		scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, lo.Map(offerings.Available(), func(o cloudprovider.Offering, _ int) string {
			return o.Requirements.Get(karpv1.CapacityTypeLabelKey).Any()
		})...),
		// Well Known to AlibabaCloud
		scheduling.NewRequirement(v1alpha1.LabelInstanceCPU, corev1.NodeSelectorOpIn, fmt.Sprint(tea.Int32Value(info.CpuCoreCount))),
		scheduling.NewRequirement(v1alpha1.LabelInstanceCPUModel, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceMemory, corev1.NodeSelectorOpIn, fmt.Sprint(tea.Float32Value(info.MemorySize)*GiBMiBRatio)),
		scheduling.NewRequirement(v1alpha1.LabelInstanceCategory, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceFamily, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceGeneration, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceSize, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceGPUName, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceGPUManufacturer, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceGPUCount, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha1.LabelInstanceGPUMemory, corev1.NodeSelectorOpDoesNotExist),
	)
	// Only add zone-id label when available in offerings. It may not be available if a user has upgraded from a
	// previous version of Karpenter w/o zone-id support and the nodeclass vswitch status has not yet updated.
	if zoneIDs := lo.FilterMap(offerings.Available(), func(o cloudprovider.Offering, _ int) (string, bool) {
		zoneID := o.Requirements.Get(v1alpha1.LabelTopologyZoneID).Any()
		return zoneID, zoneID != ""
	}); len(zoneIDs) != 0 {
		requirements.Add(scheduling.NewRequirement(v1alpha1.LabelTopologyZoneID, corev1.NodeSelectorOpIn, zoneIDs...))
	}

	// Instance Type Labels
	instanceFamilyParts := instanceTypeScheme.FindStringSubmatch(tea.StringValue(info.InstanceTypeId))
	if len(instanceFamilyParts) == 4 {
		requirements[v1alpha1.LabelInstanceCategory].Insert(instanceFamilyParts[1])
		requirements[v1alpha1.LabelInstanceGeneration].Insert(instanceFamilyParts[3])
	}
	instanceTypeParts := strings.Split(tea.StringValue(info.InstanceTypeId), ".")
	// The format is ecs.c1m1.xlarge
	if len(instanceTypeParts) == 3 {
		requirements.Get(v1alpha1.LabelInstanceFamily).Insert(strings.Join(instanceTypeParts[0:2], "."))
		requirements.Get(v1alpha1.LabelInstanceSize).Insert(instanceTypeParts[2])
	}

	// GPU Labels
	if tea.Int32Value(info.GPUAmount) != 0 {
		requirements.Get(v1alpha1.LabelInstanceGPUName).Insert(lowerKabobCase(tea.StringValue(info.GPUSpec)))
		requirements.Get(v1alpha1.LabelInstanceGPUManufacturer).Insert(getGPUManufacturer(tea.StringValue(info.GPUSpec)))
		requirements.Get(v1alpha1.LabelInstanceGPUCount).Insert(fmt.Sprint(tea.Int32Value(info.GPUAmount)))
		requirements.Get(v1alpha1.LabelInstanceGPUMemory).Insert(fmt.Sprint(tea.Float32Value(info.GPUMemorySize)))
	}

	// CPU Manufacturer, valid options: intel, amd
	if info.PhysicalProcessorModel != nil {
		requirements.Get(v1alpha1.LabelInstanceCPUModel).Insert(getCPUModel(tea.StringValue(info.PhysicalProcessorModel)))
	}

	return requirements
}

func computeCapacity(ctx context.Context,
	info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType,
	maxPods *int32, podsPerCore *int32, systemDisk *v1alpha1.SystemDisk, clusterCNI string) corev1.ResourceList {

	resourceList := corev1.ResourceList{
		corev1.ResourceCPU:              *cpu(info),
		corev1.ResourceMemory:           *memory(ctx, info),
		corev1.ResourceEphemeralStorage: *ephemeralStorage(systemDisk),
		corev1.ResourcePods:             *pods(ctx, info, maxPods, podsPerCore, clusterCNI),
		v1alpha1.ResourceNVIDIAGPU:      *nvidiaGPUs(info),
		v1alpha1.ResourceAMDGPU:         *amdGPUs(info),
	}
	return resourceList
}

// computeEvictionSignal computes the resource quantity value for an eviction signal value, computed off the
// base capacity value if the signal value is a percentage or as a resource quantity if the signal value isn't a percentage
func computeEvictionSignal(capacity resource.Quantity, signalValue string) resource.Quantity {
	if strings.HasSuffix(signalValue, "%") {
		p := mustParsePercentage(signalValue)

		// Calculation is node.capacity * signalValue if percentage
		// From https://kubernetes.io/docs/concepts/scheduling-eviction/node-pressure-eviction/#eviction-signals
		return resource.MustParse(fmt.Sprint(math.Ceil(capacity.AsApproximateFloat64() / 100 * p)))
	}
	return resource.MustParse(signalValue)
}

func mustParsePercentage(v string) float64 {
	p, err := strconv.ParseFloat(strings.Trim(v, "%"), 64)
	if err != nil {
		panic(fmt.Sprintf("expected percentage value to be a float but got %s, %v", v, err))
	}
	// Setting percentage value to 100% is considered disabling the threshold according to
	// https://kubernetes.io/docs/reference/config-api/kubelet-config.v1beta1/
	if p == 100 {
		p = 0
	}
	return p
}

func cpu(info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType) *resource.Quantity {
	return resources.Quantity(fmt.Sprint(*info.CpuCoreCount))
}

func extractMemory(info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType) *resource.Quantity {
	sizeInGib := tea.Float32Value(info.MemorySize)
	return resources.Quantity(fmt.Sprintf("%fGi", sizeInGib))
}

func memory(ctx context.Context, info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType) *resource.Quantity {
	mem := extractMemory(info)
	if mem.IsZero() {
		return mem
	}
	// Account for VM overhead in calculation
	mem.Sub(resource.MustParse(fmt.Sprintf("%dMi", int64(math.Ceil(float64(mem.Value())*options.FromContext(ctx).VMMemoryOverheadPercent/MiBByteRatio)))))
	return mem
}

func pods(_ context.Context,
	info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType,
	maxPods *int32, podsPerCore *int32, clusterCNI string) *resource.Quantity {
	var count int64
	switch {
	case maxPods != nil:
		count = int64(lo.FromPtr(maxPods))
	// TODO: support other network type, please check https://help.aliyun.com/zh/ack/ack-managed-and-ack-dedicated/user-guide/container-network/?spm=a2c4g.11186623.help-menu-85222.d_2_4_3.6d501109uQI315&scm=20140722.H_195424._.OR_help-V_1
	case clusterCNI == ClusterCNITypeTerway:
		maxENIPods := (tea.Int32Value(info.EniQuantity) - 1) * tea.Int32Value(info.EniPrivateIpAddressQuantity)
		count = int64(maxENIPods + BaseHostNetworkPods)
	case clusterCNI == ClusterCNITypeFlannel:
		count = FlannelDefaultPods
	default:
		count = v1alpha1.KubeletMaxPods
	}
	if lo.FromPtr(podsPerCore) > 0 {
		count = lo.Min([]int64{int64(lo.FromPtr(podsPerCore) * lo.FromPtr(info.CpuCoreCount)), count})
	}

	return resources.Quantity(fmt.Sprint(count))
}

func nvidiaGPUs(info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType) *resource.Quantity {
	if strings.ToLower(getGPUManufacturer(*info.GPUSpec)) == "nvidia" {
		return resources.Quantity(fmt.Sprint(*info.GPUAmount))
	}

	return resources.Quantity("0")
}

func amdGPUs(info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType) *resource.Quantity {
	if strings.ToLower(getGPUManufacturer(*info.GPUSpec)) == "amd" {
		return resources.Quantity(fmt.Sprint(*info.GPUAmount))
	}

	return resources.Quantity("0")
}

func lowerKabobCase(s string) string {
	ret := strings.ReplaceAll(s, " ", "-")
	ret = strings.ReplaceAll(ret, "/", "-")
	return strings.ToLower(strings.ReplaceAll(ret, "*", "-"))
}

func getGPUManufacturer(gpuName string) string {
	return strings.Split(gpuName, " ")[0]
}

func getCPUModel(cpuName string) string {
	if strings.Contains(cpuName, v1alpha1.ECSAMDCPUModelValue) {
		return strings.ToLower(v1alpha1.ECSAMDCPUModelValue)
	}
	if strings.Contains(cpuName, v1alpha1.ECSIntelCPUModelValue) {
		return strings.ToLower(v1alpha1.ECSIntelCPUModelValue)
	}

	// In other scenarios, it's necessary to show the manufacturer
	return ""
}

func ephemeralStorage(systemDisk *v1alpha1.SystemDisk) *resource.Quantity {
	if systemDisk == nil || systemDisk.VolumeSize == nil {
		return imagefamily.DefaultSystemDisk.VolumeSize
	}

	return systemDisk.VolumeSize
}

func privateIPv4Address(info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType) *resource.Quantity {
	return resources.Quantity(fmt.Sprint(*info.EniPrivateIpAddressQuantity * (*info.EniQuantity)))
}

func getInstanceBandwidth(info *ecsclient.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType) int32 {
	return max(lo.FromPtr(info.InstanceBandwidthRx), lo.FromPtr(info.InstanceBandwidthTx))
}
