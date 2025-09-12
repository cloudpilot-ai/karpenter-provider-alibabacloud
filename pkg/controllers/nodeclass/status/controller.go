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

	"github.com/awslabs/operatorpkg/reasonable"
	"go.uber.org/multierr"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/karpenter/pkg/operator/injection"
	"sigs.k8s.io/karpenter/pkg/utils/result"

	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/imagefamily"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/securitygroup"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/vswitch"
)

type nodeClassStatusReconciler interface {
	Reconcile(context.Context, *v1alpha1.ECSNodeClass) (reconcile.Result, error)
}

type Controller struct {
	kubeClient client.Client

	vSwitch       *VSwitch
	securityGroup *SecurityGroup
	image         *Image
}

func NewController(kubeClient client.Client, vSwitchProvider vswitch.Provider,
	securityGroupProvider securitygroup.Provider, imageProvider imagefamily.Provider) *Controller {
	return &Controller{
		kubeClient: kubeClient,

		vSwitch:       &VSwitch{vSwitchProvider: vSwitchProvider},
		securityGroup: &SecurityGroup{securityGroupProvider: securityGroupProvider},
		image:         &Image{imageProvider: imageProvider},
	}
}

func (c *Controller) Reconcile(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass) (reconcile.Result, error) {
	ctx = injection.WithControllerName(ctx, "nodeclass.status")

	if !controllerutil.ContainsFinalizer(nodeClass, v1alpha1.TerminationFinalizer) {
		stored := nodeClass.DeepCopy()
		controllerutil.AddFinalizer(nodeClass, v1alpha1.TerminationFinalizer)

		// We use client.MergeFromWithOptimisticLock because patching a list with a JSON merge patch
		// can cause races due to the fact that it fully replaces the list on a change
		// Here, we are updating the finalizer list
		if err := c.kubeClient.Patch(ctx, nodeClass, client.MergeFromWithOptions(stored, client.MergeFromWithOptimisticLock{})); err != nil {
			if errors.IsConflict(err) {
				return reconcile.Result{Requeue: true}, nil
			}
			return reconcile.Result{}, err
		}
	}
	stored := nodeClass.DeepCopy()

	// TODO: implement different conditions setup
	nodeClass.StatusConditions().SetTrue(v1alpha1.ConditionTypeInstanceRAMReady)

	var results []reconcile.Result
	var errs error
	for _, reconciler := range []nodeClassStatusReconciler{
		c.vSwitch,
		c.securityGroup,
		c.image,
	} {
		res, err := reconciler.Reconcile(ctx, nodeClass)
		errs = multierr.Append(errs, err)
		results = append(results, res)
	}

	if !equality.Semantic.DeepEqual(stored, nodeClass) {
		// We use client.MergeFromWithOptimisticLock because patching a list with a JSON merge patch
		// can cause races due to the fact that it fully replaces the list on a change
		// Here, we are updating the status condition list
		if err := c.kubeClient.Status().Patch(ctx, nodeClass, client.MergeFromWithOptions(stored, client.MergeFromWithOptimisticLock{})); err != nil {
			if errors.IsConflict(err) {
				return reconcile.Result{Requeue: true}, nil
			}
			errs = multierr.Append(errs, client.IgnoreNotFound(err))
		}
	}
	if result.Min(results...).RequeueAfter > 0 {
		return result.Min(results...), nil
	}

	return reconcile.Result{}, errs
}

func (c *Controller) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("nodeclass.status").
		For(&v1alpha1.ECSNodeClass{}).
		WithOptions(controller.Options{
			RateLimiter:             reasonable.RateLimiter(),
			MaxConcurrentReconciles: 10,
		}).
		Complete(reconcile.AsReconciler(m.GetClient(), c))
}
