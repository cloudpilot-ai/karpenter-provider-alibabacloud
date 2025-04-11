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

package operator

import (
	"context"
	"os"

	ackclient "github.com/alibabacloud-go/cs-20151215/v5/client"
	ecs "github.com/alibabacloud-go/ecs-20140526/v4/client"
	vpc "github.com/alibabacloud-go/vpc-20160428/v6/client"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
	v1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/operator"

	alicache "github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/cache"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/operator/options"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/cluster"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/imagefamily"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/pricing"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/securitygroup"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/version"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/vswitch"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/utils/client"
)

func init() {
	v1.NormalizedLabels = lo.Assign(v1.NormalizedLabels, map[string]string{"topology.diskplugin.csi.alibabacloud.com/zone": corev1.LabelTopologyZone})
}

// Operator is injected into the AliCloud CloudProvider's factories
type Operator struct {
	*operator.Operator

	UnavailableOfferingsCache *alicache.UnavailableOfferings
	InstanceProvider          instance.Provider
	PricingProvider           pricing.Provider
	VSwitchProvider           vswitch.Provider
	SecurityGroupProvider     securitygroup.Provider
	ImageProvider             imagefamily.Provider
	ImageResolver             imagefamily.Resolver
	VersionProvider           version.Provider
	InstanceTypeProvider      instancetype.Provider
}

func NewOperator(ctx context.Context, operator *operator.Operator) (context.Context, *Operator) {
	clientConfig, err := client.NewClientConfig(ctx, options.FromContext(ctx).RegionID, options.FromContext(ctx).AliNetwork)
	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to create client config")
		os.Exit(1)
	}
	ecsClient, err := ecs.NewClient(clientConfig)
	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to create ECS client")
		os.Exit(1)
	}
	vpcClient, err := vpc.NewClient(clientConfig)
	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to create VPC client")
		os.Exit(1)
	}
	ackClient, err := ackclient.NewClient(clientConfig)
	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to create ACK client")
		os.Exit(1)
	}

	region := *ecsClient.RegionId

	pricingProvider, err := pricing.NewDefaultProvider(ctx, region)
	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to create pricing provider")
		os.Exit(1)
	}

	versionProvider := version.NewDefaultProvider(operator.KubernetesInterface, cache.New(alicache.KubernetesVersionTTL, alicache.DefaultCleanupInterval))
	vSwitchProvider := vswitch.NewDefaultProvider(region, vpcClient, cache.New(alicache.DefaultTTL, alicache.DefaultCleanupInterval), cache.New(alicache.AvailableIPAddressTTL, alicache.DefaultCleanupInterval))
	securityGroupProvider := securitygroup.NewDefaultProvider(region, ecsClient, cache.New(alicache.DefaultTTL, alicache.DefaultCleanupInterval))
	clusterProvider := cluster.NewClusterProvider(ctx, ackClient, region)
	imageProvider := imagefamily.NewDefaultProvider(region, ecsClient, clusterProvider, versionProvider, cache.New(alicache.DefaultTTL, alicache.DefaultCleanupInterval))
	imageResolver := imagefamily.NewDefaultResolver(region, ecsClient, cache.New(alicache.InstanceTypeAvailableDiskTTL, alicache.DefaultCleanupInterval))

	unavailableOfferingsCache := alicache.NewUnavailableOfferings()
	instanceTypeProvider := instancetype.NewDefaultProvider(
		*ecsClient.RegionId, ecsClient,
		cache.New(alicache.InstanceTypesAndZonesTTL, alicache.DefaultCleanupInterval),
		unavailableOfferingsCache,
		pricingProvider, clusterProvider)

	instanceProvider := instance.NewDefaultProvider(
		ctx,
		region,
		ecsClient,
		unavailableOfferingsCache,
		imageResolver,
		vSwitchProvider,
		clusterProvider,
	)

	return ctx, &Operator{
		Operator: operator,

		UnavailableOfferingsCache: unavailableOfferingsCache,
		InstanceProvider:          instanceProvider,
		PricingProvider:           pricingProvider,
		VSwitchProvider:           vSwitchProvider,
		SecurityGroupProvider:     securityGroupProvider,
		ImageProvider:             imageProvider,
		ImageResolver:             imageResolver,
		VersionProvider:           versionProvider,
		InstanceTypeProvider:      instanceTypeProvider,
	}
}
