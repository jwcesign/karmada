/*
Copyright 2023 The Karmada Authors.
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

package cronfederatedhpa

import (
	"context"
	"time"

	"github.com/gorhill/cronexpr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	autoscalingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/autoscaling/v1alpha1"
	"github.com/karmada-io/karmada/pkg/sharedcli/ratelimiterflag"
	"github.com/karmada-io/karmada/pkg/util/helper"
)

const (
	// ControllerName is the controller name that will be used when reporting events.
	ControllerName = "cronfederatedhpa-controller"
)

// CronFHPAController is used to operate CronFederatedHPA.
type CronFHPAController struct {
	client.Client // used to operate Cron resources.
	EventRecorder record.EventRecorder

	RateLimiterOptions ratelimiterflag.Options
	CronHandler        *CronHandler
}

// Reconcile performs a full reconciliation for the object referred to by the Request.
// The Controller will requeue the Request to be processed again if an error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (c *CronFHPAController) Reconcile(ctx context.Context, req controllerruntime.Request) (controllerruntime.Result, error) {
	klog.V(4).Infof("Reconciling CronFederatedHPA %s", req.NamespacedName.String())

	cronFHPA := &autoscalingv1alpha1.CronFederatedHPA{}
	if err := c.Client.Get(ctx, req.NamespacedName, cronFHPA); err != nil {
		if apierrors.IsNotFound(err) {
			klog.V(4).Infof("Begin to cleanup the cron jobs for CronFederatedHPA:%s", req.NamespacedName)
			c.CronHandler.StopCronFHPAExecutor(req.NamespacedName.String())
			return controllerruntime.Result{}, nil
		}

		klog.Error("Failed to query CronFederatedHPA(%s):", req.NamespacedName.String(), err)
		return controllerruntime.Result{Requeue: true}, err
	}

	var err error
	defer func() {
		if err != nil {
			c.EventRecorder.Event(cronFHPA, corev1.EventTypeWarning, "ProcessCronFederatedHPAFailed", err.Error())
		}
	}()

	if !cronFHPA.DeletionTimestamp.IsZero() {
		c.CronHandler.StopCronFHPAExecutor(req.NamespacedName.String())
		return controllerruntime.Result{}, nil
	}

	origRuleMap := make(map[string]struct{})
	for _, history := range cronFHPA.Status.ExecutionHistories {
		origRuleMap[history.RuleName] = struct{}{}
	}

	// If scale target is updated, stop all the rule executors
	if c.CronHandler.CronFHPAScaleTargetRefUpdates(req.NamespacedName.String(), cronFHPA.Spec.ScaleTargetRef) {
		c.CronHandler.StopCronFHPAExecutor(req.NamespacedName.String())
	}

	c.CronHandler.AddCronExecutorIfNotExist(req.NamespacedName.String())

	newRuleMap := make(map[string]struct{})
	for _, rule := range cronFHPA.Spec.Rules {
		if err = c.processCronRule(cronFHPA, req.NamespacedName, rule); err != nil {
			return controllerruntime.Result{}, err
		}
		newRuleMap[rule.Name] = struct{}{}
	}

	// If rule is deleted, remove the rule executor from the handler
	for name := range origRuleMap {
		if _, ok := newRuleMap[name]; !ok {
			c.CronHandler.StopRuleExecutor(req.NamespacedName.String(), name)
			if err = c.removeCronFHPAHistory(cronFHPA, name); err != nil {
				return controllerruntime.Result{}, err
			}
		}
	}

	return controllerruntime.Result{}, nil
}

// SetupWithManager creates a controller and register to controller manager.
func (c *CronFHPAController) SetupWithManager(mgr controllerruntime.Manager) error {
	c.CronHandler = NewCronHandler(mgr.GetClient(), mgr.GetEventRecorderFor(ControllerName))
	return controllerruntime.NewControllerManagedBy(mgr).
		For(&autoscalingv1alpha1.CronFederatedHPA{}).
		WithOptions(controller.Options{RateLimiter: ratelimiterflag.DefaultControllerRateLimiter(c.RateLimiterOptions)}).
		Complete(c)
}

// processCronRule processes the cron rule
func (c *CronFHPAController) processCronRule(cronFHPA *autoscalingv1alpha1.CronFederatedHPA,
	namespaceName types.NamespacedName, rule autoscalingv1alpha1.CronFederatedHPARule) error {
	ruleOld, exists := c.CronHandler.RuleCronExecutorExists(namespaceName.String(), rule.Name)
	if exists {
		if !equality.Semantic.DeepEqual(ruleOld, rule) {
			c.CronHandler.StopRuleExecutor(namespaceName.String(), rule.Name)
		} else {
			// rule is not changed, ignore it
			return nil
		}
	}

	if helper.IsCronFederatedHPARuleSuspend(rule) {
		return nil
	}

	if err := c.CronHandler.CreateCronJobForExecutor(namespaceName, rule); err != nil {
		klog.Error("Failed to start cron job for executor:", err)
		return err
	}
	return c.updateRuleNextExecutionTime(cronFHPA, rule)
}

// updateRuleNextExecutionTime updates the next execution time of the rule
func (c *CronFHPAController) updateRuleNextExecutionTime(cronFHPA *autoscalingv1alpha1.CronFederatedHPA, rule autoscalingv1alpha1.CronFederatedHPARule) error {
	ctx := context.Background()
	exists := false

	nextExecutionTime := cronexpr.MustParse(rule.Schedule).Next(time.Now())
	for index, history := range cronFHPA.Status.ExecutionHistories {
		if history.RuleName == rule.Name {
			exists = true
			cronFHPA.Status.ExecutionHistories[index].NextExecutionTime = &metav1.Time{Time: nextExecutionTime}
		}
	}

	if !exists {
		ruleHistory := autoscalingv1alpha1.ExecutionHistory{
			RuleName: rule.Name,
			NextExecutionTime: &metav1.Time{
				Time: nextExecutionTime,
			},
		}
		cronFHPA.Status.ExecutionHistories = append(cronFHPA.Status.ExecutionHistories, ruleHistory)
	}

	if err := c.Client.Status().Update(ctx, cronFHPA); err != nil {
		klog.Errorf("Failed to update rule's next execution time:%v", err)
		return err
	}

	return nil
}

// removeCronFHPAHistory removes the rule history in status
func (c *CronFHPAController) removeCronFHPAHistory(cronFHPA *autoscalingv1alpha1.CronFederatedHPA, ruleName string) error {
	ctx := context.Background()
	exists := false

	for index, history := range cronFHPA.Status.ExecutionHistories {
		if history.RuleName == ruleName {
			exists = true
			cronFHPA.Status.ExecutionHistories = append(cronFHPA.Status.ExecutionHistories[:index], cronFHPA.Status.ExecutionHistories[index+1:]...)
			break
		}
	}

	if !exists {
		return nil
	}
	if err := c.Client.Status().Update(ctx, cronFHPA); err != nil {
		klog.Errorf("Failed to remove CronFederatedHPA(%s/%s) rule(%s) history:%v", cronFHPA.Namespace, cronFHPA.Name, ruleName, err)
		return err
	}

	return nil
}
