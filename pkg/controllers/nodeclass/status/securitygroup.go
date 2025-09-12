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
package status

import (
	"context"
	"fmt"
	"sort"
	"time"

	ecsclient "github.com/alibabacloud-go/ecs-20140526/v4/client"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/securitygroup"
)

type SecurityGroup struct {
	securityGroupProvider securitygroup.Provider
}

func (sg *SecurityGroup) Reconcile(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass) (reconcile.Result, error) {
	securityGroups, err := sg.securityGroupProvider.List(ctx, nodeClass)
	if err != nil {
		return reconcile.Result{RequeueAfter: time.Second * 30}, fmt.Errorf("getting security groups, %w", err)
	}
	if len(securityGroups) == 0 && len(nodeClass.Spec.SecurityGroupSelectorTerms) > 0 {
		nodeClass.Status.SecurityGroups = nil
		nodeClass.StatusConditions().SetFalse(v1alpha1.ConditionTypeSecurityGroupsReady, "SecurityGroupsNotFound", "SecurityGroupSelector did not match any SecurityGroups")
		// If users have omitted the necessary tags and later add them, we need to reprocess the information.
		// Returning 'ok' in this case means that the ecsnodeclass will remain in an unready state until the component is restarted.
		return reconcile.Result{RequeueAfter: time.Second * 15}, nil
	}

	sort.Slice(securityGroups, func(i, j int) bool {
		return *securityGroups[i].SecurityGroupId < *securityGroups[j].SecurityGroupId
	})

	nodeClass.Status.SecurityGroups = lo.Map(securityGroups, func(securityGroup *ecsclient.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup, _ int) v1alpha1.SecurityGroup {
		return v1alpha1.SecurityGroup{
			ID:   *securityGroup.SecurityGroupId,
			Name: *securityGroup.SecurityGroupName,
		}
	})
	nodeClass.StatusConditions().SetTrue(v1alpha1.ConditionTypeSecurityGroupsReady)
	return reconcile.Result{RequeueAfter: 5 * time.Minute}, nil
}
