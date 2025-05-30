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
	"fmt"
	"log"
	"strings"

	"github.com/mitchellh/hashstructure/v2"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	VSwitchSelectionPolicyBalanced = "balanced"
)

// ECSNodeClassSpec is the top level specification for the AlibabaCloud Karpenter Provider.
// This will contain the configuration necessary to launch instances in AlibabaCloud.
// +kubebuilder:validation:XValidation:rule="!(has(self.passwordInherit) ? (self.passwordInherit ? has(self.password) : false) : false)",message="password cannot be set when passwordInherit is true"
type ECSNodeClassSpec struct {
	// VSwitchSelectorTerms is a list of or vSwitch selector terms. The terms are ORed.
	// +kubebuilder:validation:XValidation:message="vSwitchSelectorTerms cannot be empty",rule="self.size() != 0"
	// +kubebuilder:validation:XValidation:message="expected at least one, got none, ['tags', 'id']",rule="self.all(x, has(x.tags) || has(x.id))"
	// +kubebuilder:validation:XValidation:message="'id' is mutually exclusive, cannot be set with a combination of other fields in vSwitchSelectorTerms",rule="!self.all(x, has(x.id) && has(x.tags))"
	// +kubebuilder:validation:MaxItems:=30
	// +required
	VSwitchSelectorTerms []VSwitchSelectorTerm `json:"vSwitchSelectorTerms" hash:"ignore"`
	// VSwitchSelectionPolicy is the policy to select the vSwitch.
	// +kubebuilder:validation:Enum:=balanced;cheapest
	// +kubebuilder:default:=cheapest
	VSwitchSelectionPolicy string `json:"vSwitchSelectionPolicy,omitempty"`
	// SecurityGroupSelectorTerms is a list of or security group selector terms. The terms are ORed.
	// +kubebuilder:validation:XValidation:message="securityGroupSelectorTerms cannot be empty",rule="self.size() != 0"
	// +kubebuilder:validation:XValidation:message="expected at least one, got none, ['tags', 'id', 'name']",rule="self.all(x, has(x.tags) || has(x.id) || has(x.name))"
	// +kubebuilder:validation:XValidation:message="'id' is mutually exclusive, cannot be set with a combination of other fields in securityGroupSelectorTerms",rule="!self.all(x, has(x.id) && (has(x.tags) || has(x.name)))"
	// +kubebuilder:validation:XValidation:message="'name' is mutually exclusive, cannot be set with a combination of other fields in securityGroupSelectorTerms",rule="!self.all(x, has(x.name) && (has(x.tags) || has(x.id)))"
	// +kubebuilder:validation:MaxItems:=30
	// +required
	SecurityGroupSelectorTerms []SecurityGroupSelectorTerm `json:"securityGroupSelectorTerms" hash:"ignore"`
	// ImageSelectorTerms is a list of or image selector terms. The terms are ORed.
	// +kubebuilder:validation:XValidation:message="expected at least one, got none, ['id', 'alias']",rule="self.all(x, has(x.id) || has(x.alias))"
	// +kubebuilder:validation:XValidation:message="'id' is mutually exclusive, cannot be set with a combination of other fields in imageSelectorTerms",rule="!self.exists(x, has(x.id) && (has(x.alias)))"
	// +kubebuilder:validation:XValidation:message="'alias' is mutually exclusive, cannot be set with a combination of other fields in imageSelectorTerms",rule="!self.exists(x, has(x.alias) && (has(x.id)))"
	// +kubebuilder:validation:XValidation:message="'alias' is mutually exclusive, cannot be set with a combination of other imageSelectorTerms",rule="!(self.exists(x, has(x.alias)) && self.size() != 1)"
	// +kubebuilder:validation:MinItems:=1
	// +kubebuilder:validation:MaxItems:=30
	// +required
	ImageSelectorTerms []ImageSelectorTerm `json:"imageSelectorTerms" hash:"ignore"`
	// KubeletConfiguration defines args to be used when configuring kubelet on provisioned nodes.
	// They are a vswitch of the upstream types, recognizing not all options may be supported.
	// Wherever possible, the types and names should reflect the upstream kubelet types.
	// +kubebuilder:validation:XValidation:message="imageGCHighThresholdPercent must be greater than imageGCLowThresholdPercent",rule="has(self.imageGCHighThresholdPercent) && has(self.imageGCLowThresholdPercent) ?  self.imageGCHighThresholdPercent > self.imageGCLowThresholdPercent  : true"
	// +kubebuilder:validation:XValidation:message="evictionSoft OwnerKey does not have a matching evictionSoftGracePeriod",rule="has(self.evictionSoft) ? self.evictionSoft.all(e, (e in self.evictionSoftGracePeriod)):true"
	// +kubebuilder:validation:XValidation:message="evictionSoftGracePeriod OwnerKey does not have a matching evictionSoft",rule="has(self.evictionSoftGracePeriod) ? self.evictionSoftGracePeriod.all(e, (e in self.evictionSoft)):true"
	// +optional
	KubeletConfiguration *KubeletConfiguration `json:"kubeletConfiguration,omitempty"`
	// SystemDisk to be applied to provisioned nodes.
	// +optional
	SystemDisk *SystemDisk `json:"systemDisk,omitempty"`
	// DataDisk to be applied to provisioned nodes.
	// +optional
	DataDisks []DataDisk `json:"dataDisks,omitempty"`
	// The category of the data disk (for example, cloud and cloud_ssd).
	// Different ECS is compatible with different disk category, using array to maximize ECS creation success.
	// Valid values:"cloud", "cloud_efficiency", "cloud_ssd", "cloud_essd", "cloud_auto", and "cloud_essd_entry"
	// +kubebuilder:validation:Items=Enum=cloud;cloud_efficiency;cloud_ssd;cloud_essd;cloud_auto;cloud_essd_entry
	// +optional
	DataDisksCategories []string `json:"dataDiskCategories,omitempty"`
	// FormatDataDisk specifies whether to mount data disks to an existing instance when adding it to the cluster. This allows you to add data disks for storing container data and images. If FormatDataDisk is set to true, and the Elastic Compute Service (ECS) instances already have data disks mounted, but the file system on the last data disk is not initialized, the system will automatically format the disk to ext4 and mount it to /var/lib/containerd and /var/lib/kubelet.
	// +kubebuilder:default:=false
	// +optional
	FormatDataDisk bool `json:"formatDataDisk,omitempty"`
	// Tags to be applied on ecs resources like instances and launch templates.
	// +kubebuilder:validation:XValidation:message="empty tag keys aren't supported",rule="self.all(k, k != '')"
	// +kubebuilder:validation:XValidation:message="tag contains a restricted tag matching ecs:ecs-cluster-name",rule="self.all(k, k !='ecs:ecs-cluster-name')"
	// +kubebuilder:validation:XValidation:message="tag contains a restricted tag matching kubernetes.io/cluster/",rule="self.all(k, !k.startsWith('kubernetes.io/cluster') )"
	// +kubebuilder:validation:XValidation:message="tag contains a restricted tag matching karpenter.sh/nodepool",rule="self.all(k, k != 'karpenter.sh/nodepool')"
	// +kubebuilder:validation:XValidation:message="tag contains a restricted tag matching karpenter.sh/nodeclaim",rule="self.all(k, k !='karpenter.sh/nodeclaim')"
	// +kubebuilder:validation:XValidation:message="tag contains a restricted tag matching karpenter.k8s.alibabacloud/ecsnodeclass",rule="self.all(k, k !='karpenter.k8s.alibabacloud/ecsnodeclass')"
	// +optional
	Tags map[string]string `json:"tags,omitempty"`
	// ResourceGroupID is the resource group id in ECS
	// +kubebuilder:validation:Pattern:="rg-[0-9a-z]+"
	// +optional
	ResourceGroupID string `json:"resourceGroupId,omitempty"`
	// UserData to be applied to the provisioned nodes and executed before/after the node is registered.
	// +optional
	UserData *string `json:"userData,omitempty"`
	// Password is the password for ecs for root.
	// +kubebuilder:validation:Pattern=`^[A-Za-z\d~!@#$%^&*()_+\-=\[\]{}|\\:;"'<>,.?/]{8,30}$`
	//+optional
	Password string `json:"password,omitempty"`
	// KeyPairName is the key pair used when creating an ECS instance for root.
	// +kubebuilder:validation:Pattern=`^[A-Za-z][A-Za-z\d._:-]{1,127}$`
	// +optional
	KeyPairName string `json:"keyPairName,omitempty"`
	// If PasswordInherit is true will use the password preset by os image.
	// +kubebuilder:default:=false
	// +optional
	PasswordInherit bool `json:"passwordInherit,omitempty"`
}

// VSwitchSelectorTerm defines selection logic for a vSwitch used by Karpenter to launch nodes.
type VSwitchSelectorTerm struct {
	// Tags is a map of key/value tags used to select vSwitches
	// Specifying '*' for a value selects all values for a given tag key.
	// +kubebuilder:validation:XValidation:message="empty tag keys aren't supported",rule="self.all(k, k != '')"
	// +kubebuilder:validation:MaxProperties:=20
	// +optional
	Tags map[string]string `json:"tags,omitempty"`
	// ID is the vSwitch id in ECS
	// +kubebuilder:validation:Pattern="vsw-[0-9a-z]+"
	// +optional
	ID string `json:"id,omitempty"`
}

// SecurityGroupSelectorTerm defines selection logic for a security group used by Karpenter to launch nodes.
// If multiple fields are used for selection, the requirements are ANDed.
type SecurityGroupSelectorTerm struct {
	// Tags is a map of key/value tags used to select vSwitches
	// Specifying '*' for a value selects all values for a given tag key.
	// +kubebuilder:validation:XValidation:message="empty tag keys aren't supported",rule="self.all(k, k != '')"
	// +kubebuilder:validation:MaxProperties:=20
	// +optional
	Tags map[string]string `json:"tags,omitempty"`
	// ID is the security group id in ECS
	// +kubebuilder:validation:Pattern:="sg-[0-9a-z]+"
	// +optional
	ID string `json:"id,omitempty"`
	// Name is the security group name in ECS.
	// This value is the name field, which is different from the name tag.
	Name string `json:"name,omitempty"`
}

// ImageSelectorTerm defines selection logic for an image used by Karpenter to launch nodes.
// If multiple fields are used for selection, the requirements are ANDed.
type ImageSelectorTerm struct {
	// Alias specifies which ACK image to select.
	// Each alias consists of a family and an image version, specified as "family@version".
	// Valid families include: AlibabaCloudLinux3,ContainerOS
	// Currently only supports version pinning to the latest image release, with that images version format (ex: "aliyun3@latest").
	// Setting the version to latest will result in drift when a new Image is released. This is **not** recommended for production environments.
	// +kubebuilder:validation:XValidation:message="'alias' is improperly formatted, must match the format 'family'",rule="self.matches('^[a-zA-Z0-9]+@.+$')"
	// +kubebuilder:validation:XValidation:message="family is not supported, must be one of the following: 'AlibabaCloudLinux3,ContainerOS'",rule="self.find('^[^@]+') in ['AlibabaCloudLinux3', 'ContainerOS']"
	// +kubebuilder:validation:MaxLength=30
	// +optional
	Alias string `json:"alias,omitempty"`
	// ID is the image id in ECS
	// +optional
	ID string `json:"id,omitempty"`
}

// KubeletConfiguration defines args to be used when configuring kubelet on provisioned nodes.
// They are a vswitch of the upstream types, recognizing not all options may be supported.
// Wherever possible, the types and names should reflect the upstream kubelet types.
// https://pkg.go.dev/k8s.io/kubelet/config/v1beta1#KubeletConfiguration
// https://github.com/kubernetes/kubernetes/blob/9f82d81e55cafdedab619ea25cabf5d42736dacf/cmd/kubelet/app/options/options.go#L53
type KubeletConfiguration struct {
	// clusterDNS is a list of IP addresses for the cluster DNS server.
	// Note that not all providers may use all addresses.
	//+optional
	ClusterDNS []string `json:"clusterDNS,omitempty"`
	// MaxPods is an override for the maximum number of pods that can run on
	// a worker node instance.
	// +kubebuilder:validation:Minimum:=0
	// +optional
	MaxPods *int32 `json:"maxPods,omitempty"`
	// PodsPerCore is an override for the number of pods that can run on a worker node
	// instance based on the number of cpu cores. This value cannot exceed MaxPods, so, if
	// MaxPods is a lower value, that value will be used.
	// +kubebuilder:validation:Minimum:=0
	// +optional
	PodsPerCore *int32 `json:"podsPerCore,omitempty"`
	// SystemReserved contains resources reserved for OS system daemons and kernel memory.
	// +kubebuilder:validation:XValidation:message="valid keys for systemReserved are ['cpu','memory','ephemeral-storage','pid']",rule="self.all(x, x=='cpu' || x=='memory' || x=='ephemeral-storage' || x=='pid')"
	// +kubebuilder:validation:XValidation:message="systemReserved value cannot be a negative resource quantity",rule="self.all(x, !self[x].startsWith('-'))"
	// +optional
	SystemReserved map[string]string `json:"systemReserved,omitempty"`
	// KubeReserved contains resources reserved for Kubernetes system components.
	// +kubebuilder:validation:XValidation:message="valid keys for kubeReserved are ['cpu','memory','ephemeral-storage','pid']",rule="self.all(x, x=='cpu' || x=='memory' || x=='ephemeral-storage' || x=='pid')"
	// +kubebuilder:validation:XValidation:message="kubeReserved value cannot be a negative resource quantity",rule="self.all(x, !self[x].startsWith('-'))"
	// +optional
	KubeReserved map[string]string `json:"kubeReserved,omitempty"`
	// EvictionHard is the map of signal names to quantities that define hard eviction thresholds
	// +kubebuilder:validation:XValidation:message="valid keys for evictionHard are ['memory.available','nodefs.available','nodefs.inodesFree','imagefs.available','imagefs.inodesFree','pid.available']",rule="self.all(x, x in ['memory.available','nodefs.available','nodefs.inodesFree','imagefs.available','imagefs.inodesFree','pid.available'])"
	// +optional
	EvictionHard map[string]string `json:"evictionHard,omitempty"`
	// EvictionSoft is the map of signal names to quantities that define soft eviction thresholds
	// +kubebuilder:validation:XValidation:message="valid keys for evictionSoft are ['memory.available','nodefs.available','nodefs.inodesFree','imagefs.available','imagefs.inodesFree','pid.available']",rule="self.all(x, x in ['memory.available','nodefs.available','nodefs.inodesFree','imagefs.available','imagefs.inodesFree','pid.available'])"
	// +optional
	EvictionSoft map[string]string `json:"evictionSoft,omitempty"`
	// EvictionSoftGracePeriod is the map of signal names to quantities that define grace periods for each eviction signal
	// +kubebuilder:validation:XValidation:message="valid keys for evictionSoftGracePeriod are ['memory.available','nodefs.available','nodefs.inodesFree','imagefs.available','imagefs.inodesFree','pid.available']",rule="self.all(x, x in ['memory.available','nodefs.available','nodefs.inodesFree','imagefs.available','imagefs.inodesFree','pid.available'])"
	// +optional
	EvictionSoftGracePeriod map[string]metav1.Duration `json:"evictionSoftGracePeriod,omitempty"`
	// EvictionMaxPodGracePeriod is the maximum allowed grace period (in seconds) to use when terminating pods in
	// response to soft eviction thresholds being met.
	// +optional
	EvictionMaxPodGracePeriod *int32 `json:"evictionMaxPodGracePeriod,omitempty"`
	// ImageGCHighThresholdPercent is the percent of disk usage after which image
	// garbage collection is always run. The percent is calculated by dividing this
	// field value by 100, so this field must be between 0 and 100, inclusive.
	// When specified, the value must be greater than ImageGCLowThresholdPercent.
	// +kubebuilder:validation:Minimum:=0
	// +kubebuilder:validation:Maximum:=100
	// +optional
	ImageGCHighThresholdPercent *int32 `json:"imageGCHighThresholdPercent,omitempty"`
	// ImageGCLowThresholdPercent is the percent of disk usage before which image
	// garbage collection is never run. Lowest disk usage to garbage collect to.
	// The percent is calculated by dividing this field value by 100,
	// so the field value must be between 0 and 100, inclusive.
	// When specified, the value must be less than imageGCHighThresholdPercent
	// +kubebuilder:validation:Minimum:=0
	// +kubebuilder:validation:Maximum:=100
	// +optional
	ImageGCLowThresholdPercent *int32 `json:"imageGCLowThresholdPercent,omitempty"`
	// CPUCFSQuota enables CPU CFS quota enforcement for containers that specify CPU limits.
	// +optional
	CPUCFSQuota *bool `json:"cpuCFSQuota,omitempty"`
}

type SystemDisk struct {
	// The category of the system disk (for example, cloud and cloud_ssd).
	// Different ECS is compatible with different disk category, using array to maximize ECS creation success.
	// Valid values:"cloud", "cloud_efficiency", "cloud_ssd", "cloud_essd", "cloud_auto", and "cloud_essd_entry"
	// +kubebuilder:validation:Items=Enum=cloud;cloud_efficiency;cloud_ssd;cloud_essd;cloud_auto;cloud_essd_entry
	// +kubebuilder:default:={"cloud","cloud_efficiency","cloud_ssd","cloud_essd","cloud_auto","cloud_essd_entry"}
	// +optional
	Categories []string `json:"categories,omitempty"`
	// Size in `Gi`, `G`, `Ti`, or `T`. You must specify either a snapshot ID or
	// a volume size.
	// + TODO: Add the CEL resources.quantity type after k8s 1.29
	// + https://github.com/kubernetes/apiserver/commit/b137c256373aec1c5d5810afbabb8932a19ecd2a#diff-838176caa5882465c9d6061febd456397a3e2b40fb423ed36f0cabb1847ecb4dR190
	// +kubebuilder:validation:Pattern:="^((?:[1-9][0-9]{0,3}|[1-4][0-9]{4}|[5][0-8][0-9]{3}|59000)Gi|(?:[1-9][0-9]{0,3}|[1-5][0-9]{4}|[6][0-3][0-9]{3}|64000)G|([1-9]||[1-5][0-7]|58)Ti|([1-9]||[1-5][0-9]|6[0-3]|64)T)$"
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:Type:=string
	// +optional
	VolumeSize *resource.Quantity `json:"volumeSize,omitempty" hash:"string"`
	// The size of the system disk. Unit: GiB.
	// Valid values:
	//   * If you set Category to cloud: 20 to 500.
	//   * If you set Category to other disk categories: 20 to 2048.
	//
	// +kubebuilder:validation:XValidation:message="size invalid",rule="self >= 20"
	// +optional
	Size *int32 `json:"size,omitempty"`
	// The performance level of the ESSD to use as the system disk. Default value: PL0.
	// Valid values:
	//   * PL0: A single ESSD can deliver up to 10,000 random read/write IOPS.
	//   * PL1: A single ESSD can deliver up to 50,000 random read/write IOPS.
	//   * PL2: A single ESSD can deliver up to 100,000 random read/write IOPS.
	//   * PL3: A single ESSD can deliver up to 1,000,000 random read/write IOPS.
	// This will be supported soon
	// +kubebuilder:validation:Enum:={PL0,PL1,PL2,PL3}
	// +kubebuilder:default:=PL0
	PerformanceLevel *string `json:"performanceLevel,omitempty"`
}

type DataDisk struct {
	// Size in `Gi`, `G`, `Ti`, or `T`. You must specify either a snapshot ID or
	// a volume size.
	// + TODO: Add the CEL resources.quantity type after k8s 1.29
	// + https://github.com/kubernetes/apiserver/commit/b137c256373aec1c5d5810afbabb8932a19ecd2a#diff-838176caa5882465c9d6061febd456397a3e2b40fb423ed36f0cabb1847ecb4dR190
	// +kubebuilder:validation:Pattern:="^((?:[1-9][0-9]{0,3}|[1-4][0-9]{4}|[5][0-8][0-9]{3}|59000)Gi|(?:[1-9][0-9]{0,3}|[1-5][0-9]{4}|[6][0-3][0-9]{3}|64000)G|([1-9]||[1-5][0-7]|58)Ti|([1-9]||[1-5][0-9]|6[0-3]|64)T)$"
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:Type:=string
	// +kubebuilder:default:="20Gi"
	// +optional
	VolumeSize *resource.Quantity `json:"volumeSize,omitempty" hash:"string"`
	// Mount point of the data disk.
	// +optional
	Device *string `json:"device,omitempty"`
}

// ECSNodeClass is the Schema for the ECSNodeClass API
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:path=ecsnodeclasses,scope=Cluster,categories=karpenter,shortName={ecsnc,ecsncs}
// +kubebuilder:subresource:status
type ECSNodeClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ECSNodeClassSpec   `json:"spec,omitempty"`
	Status ECSNodeClassStatus `json:"status,omitempty"`
}

const (
	KubeletMaxPods = 110

	// We need to bump the ECSNodeClassHashVersion when we make an update to the ECSNodeClass CRD under these conditions:
	// 1. A field changes its default value for an existing field that is already hashed
	// 2. A field is added to the hash calculation with an already-set value
	// 3. A field is removed from the hash calculations
	ECSNodeClassHashVersion = "v3"
)

func (in *ECSNodeClass) Hash() string {
	return fmt.Sprint(lo.Must(hashstructure.Hash([]interface{}{
		in.Spec,
	}, hashstructure.FormatV2, &hashstructure.HashOptions{
		SlicesAsSets:    true,
		IgnoreZeroValue: true,
		ZeroNil:         true,
	})))
}

func (in *ECSNodeClass) Alias() *Alias {
	term, ok := lo.Find(in.Spec.ImageSelectorTerms, func(term ImageSelectorTerm) bool {
		return term.Alias != ""
	})
	if !ok {
		return nil
	}
	return NewAlias(term.Alias)
}

type Alias struct {
	Family  string
	Version string
}

const (
	AliasVersionLatest = "latest"
)

func NewAlias(item string) *Alias {
	return &Alias{
		Family:  imageFamilyFromAlias(item),
		Version: imageVersionFromAlias(item),
	}
}

func (a *Alias) String() string {
	return fmt.Sprintf("%s@%s", a.Family, a.Version)
}

// ECSNodeClassList contains a list of ECSNodeClass
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ECSNodeClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ECSNodeClass `json:"items"`
}

func imageFamilyFromAlias(alias string) string {
	components := strings.Split(alias, "@")
	if len(components) > 2 {
		log.Fatalf("failed to parse Image alias %q, invalid format", alias)
	}
	family, ok := lo.Find([]string{
		ImageFamilyAlibabaCloudLinux3,
		ImageFamilyContainerOS,
	}, func(family string) bool {
		return family == components[0]
	})
	if !ok {
		log.Fatalf("%q is an invalid alias family", components[0])
	}
	return family
}

func imageVersionFromAlias(alias string) string {
	components := strings.Split(alias, "@")
	if len(components) != 2 {
		return AliasVersionLatest
	}
	return components[1]
}

func (sd *SystemDisk) GetGiBSize() int32 {
	if sd.VolumeSize != nil {
		return int32(sd.VolumeSize.Value() / (1024 * 1024 * 1024)) // #nosec G115
	}
	if sd.Size != nil {
		return *sd.Size
	}
	return 0
}

func (dd *DataDisk) GetGiBSize() int32 {
	if dd.VolumeSize != nil {
		return int32(dd.VolumeSize.Value() / (1024 * 1024 * 1024)) // #nosec G115
	}
	return 0
}
