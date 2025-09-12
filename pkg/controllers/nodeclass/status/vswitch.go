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

package status

import (
	"context"
	"fmt"
	"sort"
	"time"

	vpc "github.com/alibabacloud-go/vpc-20160428/v6/client"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/vswitch"
)

type VSwitch struct {
	vSwitchProvider vswitch.Provider
}

func (v *VSwitch) Reconcile(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass) (reconcile.Result, error) {
	vSwitches, err := v.vSwitchProvider.List(ctx, nodeClass)
	if err != nil {
		return reconcile.Result{RequeueAfter: time.Second * 30}, fmt.Errorf("getting vSwitches, %w", err)
	}
	if len(vSwitches) == 0 {
		nodeClass.Status.VSwitches = nil
		nodeClass.StatusConditions().SetFalse(v1alpha1.ConditionTypeVSwitchesReady, "vSwitchesNotFound", "VSwitchSelector did not match any VSwitches")
		// If users have omitted the necessary tags and later add them, we need to reprocess the information.
		// Returning 'ok' in this case means that the ecsnodeclass will remain in an unready state until the component is restarted.
		return reconcile.Result{RequeueAfter: time.Second * 15}, nil
	}
	sort.Slice(vSwitches, func(i, j int) bool {
		if int(*vSwitches[i].AvailableIpAddressCount) != int(*vSwitches[j].AvailableIpAddressCount) {
			return int(*vSwitches[i].AvailableIpAddressCount) > int(*vSwitches[j].AvailableIpAddressCount)
		}
		return *vSwitches[i].VSwitchId < *vSwitches[j].VSwitchId
	})
	nodeClass.Status.VSwitches = lo.Map(vSwitches, func(ecsvSwitch *vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch, _ int) v1alpha1.VSwitch {
		return v1alpha1.VSwitch{
			ID:     *ecsvSwitch.VSwitchId,
			ZoneID: *ecsvSwitch.ZoneId,
		}
	})
	nodeClass.StatusConditions().SetTrue(v1alpha1.ConditionTypeVSwitchesReady)
	return reconcile.Result{RequeueAfter: time.Minute}, nil
}
