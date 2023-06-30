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
	"fmt"
	"time"

	"github.com/gorhill/cronexpr"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	autoscalingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/autoscaling/v1alpha1"
	"github.com/karmada-io/karmada/pkg/util"
	"github.com/karmada-io/karmada/pkg/util/helper"
)

type CronFederatedHPAJob struct {
	client        client.Client
	eventRecorder record.EventRecorder

	namespaceName types.NamespacedName
	rule          autoscalingv1alpha1.CronFederatedHPARule
}

func NewCronFederatedHPAJob(client client.Client, eventRecorder record.EventRecorder,
	namespaceName types.NamespacedName, rule autoscalingv1alpha1.CronFederatedHPARule) *CronFederatedHPAJob {
	return &CronFederatedHPAJob{
		client:        client,
		eventRecorder: eventRecorder,
		namespaceName: namespaceName,
		rule:          rule,
	}
}

func (c *CronFederatedHPAJob) Run() {
	klog.V(4).Infof("Start to handle CronFederatedHPA %s", c.namespaceName)
	defer klog.V(4).Infof("End to handle CronFederatedHPA %s", c.namespaceName)

	var err error
	cronFHPA := &autoscalingv1alpha1.CronFederatedHPA{}
	err = c.client.Get(context.TODO(), c.namespaceName, cronFHPA)
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.Infof("CronFederatedHPA %s not found", c.namespaceName)
		} else {
			klog.Errorf("Get CronFederatedHPA failed: %v", err)
		}
		return
	}

	// It shouldn't be true, because we will not add the rule to schedule queue
	if helper.IsCronFederatedHPARuleSuspend(c.rule) {
		klog.V(4).Infof("CronFederatedHPA %s Rule %s is suspended", c.namespaceName, c.rule.Name)
		return
	}

	var scaleErr error
	defer func() {
		if scaleErr != nil {
			c.eventRecorder.Event(cronFHPA, corev1.EventTypeWarning, "ScaleFailed", scaleErr.Error())
			err = c.addFailedExecutionHistory(cronFHPA, scaleErr.Error())
		} else {
			err = c.addSuccessExecutionHistory(cronFHPA, c.rule.TargetReplicas, c.rule.TargetMinReplicas, c.rule.TargetMaxReplicas)
		}
		if err != nil {
			c.eventRecorder.Event(cronFHPA, corev1.EventTypeWarning, "ScaleFailed", err.Error())
		}
	}()

	if cronFHPA.Spec.ScaleTargetRef.APIVersion == autoscalingv1alpha1.GroupVersion.String() {
		if cronFHPA.Spec.ScaleTargetRef.Kind != autoscalingv1alpha1.FederatedHPAKind {
			scaleErr = fmt.Errorf("do not support scale target %s/%s", cronFHPA.Spec.ScaleTargetRef.APIVersion, cronFHPA.Spec.ScaleTargetRef.Kind)
			return
		}

		scaleErr = retry.RetryOnConflict(retry.DefaultRetry, func() (err error) {
			err = c.ScaleFHPA(cronFHPA)
			return err
		})
		return
	}

	// scale workload directly
	scaleErr = retry.RetryOnConflict(retry.DefaultRetry, func() (err error) {
		err = c.ScaleWorkloads(cronFHPA)
		return err
	})
}

func (c *CronFederatedHPAJob) ScaleFHPA(cronFHPA *autoscalingv1alpha1.CronFederatedHPA) error {
	fhpaName := types.NamespacedName{
		Namespace: cronFHPA.Namespace,
		Name:      cronFHPA.Spec.ScaleTargetRef.Name,
	}

	fhpa := &autoscalingv1alpha1.FederatedHPA{}
	err := c.client.Get(context.TODO(), fhpaName, fhpa)
	if err != nil {
		return err
	}

	update := false
	if c.rule.TargetMaxReplicas != nil && fhpa.Spec.MaxReplicas != *c.rule.TargetMaxReplicas {
		fhpa.Spec.MaxReplicas = *c.rule.TargetMaxReplicas
		update = true
	}
	if c.rule.TargetMinReplicas != nil && *fhpa.Spec.MinReplicas != *c.rule.TargetMinReplicas {
		*fhpa.Spec.MinReplicas = *c.rule.TargetMinReplicas
		update = true
	}

	if update {
		err := c.client.Update(context.TODO(), fhpa)
		if err != nil {
			klog.Errorf("Update FederatedHPA(%s/%s) failed: %v", fhpa.Namespace, fhpa.Name, err)
			return err
		}
		klog.V(4).Infof("FederatedHPA(%s/%s) has be scaled successfully", fhpa.Namespace, fhpa.Name)
		return nil
	}

	klog.V(4).Infof("Nothing updated of FederatedHPA(%s/%s), skip it", fhpa.Namespace, fhpa.Name)
	return nil
}

func (c *CronFederatedHPAJob) ScaleWorkloads(cronFHPA *autoscalingv1alpha1.CronFederatedHPA) error {
	ctx := context.Background()

	scaleClient := c.client.SubResource("scale")

	targetGV, err := schema.ParseGroupVersion(cronFHPA.Spec.ScaleTargetRef.APIVersion)
	targetGVK := schema.GroupVersionKind{
		Group:   targetGV.Group,
		Kind:    cronFHPA.Spec.ScaleTargetRef.Kind,
		Version: targetGV.Version,
	}
	targetResource := &unstructured.Unstructured{}
	targetResource.SetGroupVersionKind(targetGVK)
	err = c.client.Get(ctx, types.NamespacedName{Namespace: cronFHPA.Namespace, Name: cronFHPA.Spec.ScaleTargetRef.Name}, targetResource)
	if err != nil {
		klog.Errorf("Get Resource(%s/%s) failed: %v", cronFHPA.Namespace, cronFHPA.Spec.ScaleTargetRef.Name, err)
		return err
	}

	scaleObj := &unstructured.Unstructured{}
	err = scaleClient.Get(ctx, targetResource, scaleObj)
	if err != nil {
		klog.Errorf("Get Scale for resource(%s/%s) failed: %v", cronFHPA.Namespace, cronFHPA.Spec.ScaleTargetRef.Name, err)
		return err
	}

	scale := &autoscalingv1.Scale{}
	err = helper.ConvertToTypedObject(scaleObj, scale)
	if err != nil {
		klog.Errorf("Convert Scale failed: %v", err)
		return err
	}

	if scale.Spec.Replicas != *c.rule.TargetReplicas {
		if err := helper.ApplyReplica(scaleObj, int64(*c.rule.TargetReplicas), util.ReplicasField); err != nil {
			klog.Errorf("Apply Replicas for %s/%s failed: %v", cronFHPA.Namespace, cronFHPA.Spec.ScaleTargetRef.Name, err)
			return err
		}
		err := scaleClient.Update(ctx, targetResource, client.WithSubResourceBody(scaleObj))
		if err != nil {
			klog.Errorf("Update Scale failed: %v", err)
			return err
		}
		klog.V(4).Infof("Resource(%s/%s) has be scaled successfully", cronFHPA.Namespace, cronFHPA.Spec.ScaleTargetRef.Name)
		return nil
	}
	return nil
}

func (c *CronFederatedHPAJob) addFailedExecutionHistory(
	cronFHPA *autoscalingv1alpha1.CronFederatedHPA, errMsg string) error {
	exists := false

	nextExecutionTime := cronexpr.MustParse(c.rule.Schedule).Next(time.Now())
	for index, rule := range cronFHPA.Status.ExecutionHistories {
		if rule.RuleName == c.rule.Name {
			failedExecution := autoscalingv1alpha1.FailedExecution{
				ScheduleTime:  rule.NextExecutionTime,
				ExecutionTime: &metav1.Time{Time: time.Now()},
				Message:       errMsg,
			}
			historyLimits := helper.CronFederatedHPAFailedHistoryLimits(c.rule)
			if len(rule.FailedExecutions) > historyLimits-1 {
				rule.FailedExecutions = rule.FailedExecutions[:historyLimits-1]
			}
			cronFHPA.Status.ExecutionHistories[index].FailedExecutions =
				append([]autoscalingv1alpha1.FailedExecution{failedExecution}, rule.FailedExecutions...)
			cronFHPA.Status.ExecutionHistories[index].NextExecutionTime = &metav1.Time{Time: nextExecutionTime}
			exists = true
			break
		}
	}

	// If this history not exist, it means the rule is suspended or deleted, so just ignore it.
	if exists {
		updateErr := retry.RetryOnConflict(retry.DefaultRetry, func() (err error) {
			return c.client.Status().Update(context.Background(), cronFHPA)
		})

		if updateErr != nil {
			klog.Errorf("Failed to update rule's history fail:%v", updateErr)
			return updateErr
		}
		klog.V(4).Infof("FederatedHPA(%s/%s) status has been updated successfully", cronFHPA.Namespace, cronFHPA.Name)
	}

	return nil
}

func (c *CronFederatedHPAJob) addSuccessExecutionHistory(
	cronFHPA *autoscalingv1alpha1.CronFederatedHPA,
	appliedReplicas, appliedMaxReplicas, appliedMinReplicas *int32) error {
	exists := false
	nextExecutionTime := cronexpr.MustParse(c.rule.Schedule).Next(time.Now())

	for index, rule := range cronFHPA.Status.ExecutionHistories {
		if rule.RuleName == c.rule.Name {
			successExecution := autoscalingv1alpha1.SuccessfulExecution{
				ScheduleTime:       rule.NextExecutionTime,
				ExecutionTime:      &metav1.Time{Time: time.Now()},
				AppliedReplicas:    appliedReplicas,
				AppliedMaxReplicas: appliedMaxReplicas,
				AppliedMinReplicas: appliedMinReplicas,
			}
			historyLimits := helper.CronFederatedHPASuccessHistoryLimits(c.rule)
			if len(rule.SuccessfulExecutions) > historyLimits-1 {
				rule.SuccessfulExecutions = rule.SuccessfulExecutions[:historyLimits-1]
			}
			cronFHPA.Status.ExecutionHistories[index].SuccessfulExecutions =
				append([]autoscalingv1alpha1.SuccessfulExecution{successExecution}, rule.SuccessfulExecutions...)
			cronFHPA.Status.ExecutionHistories[index].NextExecutionTime = &metav1.Time{Time: nextExecutionTime}
			exists = true
			break
		}
	}

	// If this history not exist, it means the rule is suspended or deleted, so just ignore it.
	if exists {
		updateErr := retry.RetryOnConflict(retry.DefaultRetry, func() (err error) {
			return c.client.Status().Update(context.Background(), cronFHPA)
		})

		if updateErr != nil {
			klog.Errorf("Failed to update rule's history fail:%v", updateErr)
			return updateErr
		}
		klog.V(4).Infof("FederatedHPA(%s/%s) status has been updated successfully", cronFHPA.Namespace, cronFHPA.Name)
	}

	return nil
}
