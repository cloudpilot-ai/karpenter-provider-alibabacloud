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

package main

import (
	"github.com/samber/lo"
	"sigs.k8s.io/karpenter/pkg/cloudprovider/metrics"
	corecontrollers "sigs.k8s.io/karpenter/pkg/controllers"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	coreoperator "sigs.k8s.io/karpenter/pkg/operator"

	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/cloudprovider"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/controllers"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/operator"
)

func main() {
	ctx, op := operator.NewOperator(coreoperator.NewOperator())

	aliCloudProvider := cloudprovider.New(
		op.GetClient(),
		op.EventRecorder,
		op.InstanceTypeProvider,
		op.InstanceProvider,
	)

	lo.Must0(op.AddHealthzCheck("cloud-provider", aliCloudProvider.LivenessProbe))
	cloudProvider := metrics.Decorate(aliCloudProvider)
	clusterState := state.NewCluster(op.Clock, op.GetClient(), cloudProvider)

	op.
		WithControllers(ctx, corecontrollers.NewControllers(
			ctx,
			op.Manager,
			op.Clock,
			op.GetClient(),
			op.EventRecorder,
			cloudProvider,
			clusterState,
		)...).
		WithControllers(ctx, controllers.NewControllers(
			ctx, op.Manager, op.Clock, op.GetConfig(),
			op.GetClient(), op.EventRecorder,
			op.UnavailableOfferingsCache,
			cloudProvider,
			op.InstanceProvider, op.InstanceTypeProvider,
			op.PricingProvider, op.VSwitchProvider,
			op.SecurityGroupProvider, op.ImageProvider,
		)...).
		Start(ctx)
}
