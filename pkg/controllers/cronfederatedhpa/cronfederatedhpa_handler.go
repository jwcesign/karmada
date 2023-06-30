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
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	autoscalingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/autoscaling/v1alpha1"
)

type RuleCron struct {
	*cron.Cron
	autoscalingv1alpha1.CronFederatedHPARule
}

type CronHandler struct {
	client        client.Client
	eventRecorder record.EventRecorder

	// cronExecutorMap is [cronFederatedHPA name][rule name]RuleCron
	cronExecutorMap map[string]map[string]RuleCron
	executorLock    sync.RWMutex

	cronFHPAScaleTargetMap map[string]autoscalingv2.CrossVersionObjectReference
	scaleTargetLock        sync.RWMutex
}

// NewCronHandler create new cron handler
func NewCronHandler(client client.Client, eventRecorder record.EventRecorder) *CronHandler {
	return &CronHandler{
		client:                 client,
		eventRecorder:          eventRecorder,
		cronExecutorMap:        make(map[string]map[string]RuleCron),
		cronFHPAScaleTargetMap: make(map[string]autoscalingv2.CrossVersionObjectReference),
	}
}

// CronFHPAScaleTargetRefUpdates check if scale target changed
func (c *CronHandler) CronFHPAScaleTargetRefUpdates(cronFHPAKey string, scaleTarget autoscalingv2.CrossVersionObjectReference) bool {
	c.scaleTargetLock.Lock()
	defer c.scaleTargetLock.Unlock()

	origTarget, ok := c.cronFHPAScaleTargetMap[cronFHPAKey]
	if !ok {
		c.cronFHPAScaleTargetMap[cronFHPAKey] = scaleTarget
		return false
	}

	return equality.Semantic.DeepEqual(origTarget, scaleTarget)
}

// AddCronExecutorIfNotExist create the executor for CronFederatedHPA if not exist
func (c *CronHandler) AddCronExecutorIfNotExist(cronFHPAKey string) {
	c.executorLock.Lock()
	defer c.executorLock.Unlock()

	if _, ok := c.cronExecutorMap[cronFHPAKey]; ok {
		return
	}

	c.cronExecutorMap[cronFHPAKey] = make(map[string]RuleCron)
}

func (c *CronHandler) RuleCronExecutorExists(cronFHPAKey string, ruleName string) (autoscalingv1alpha1.CronFederatedHPARule, bool) {
	c.executorLock.RLock()
	defer c.executorLock.RUnlock()

	if _, ok := c.cronExecutorMap[cronFHPAKey]; !ok {
		return autoscalingv1alpha1.CronFederatedHPARule{}, false
	}
	cronRule, exists := c.cronExecutorMap[cronFHPAKey][ruleName]
	return cronRule.CronFederatedHPARule, exists
}

// StopRuleExecutor stops the executor for specific CronFederatedHPA rule
func (c *CronHandler) StopRuleExecutor(cronFHPAKey string, ruleName string) {
	c.executorLock.Lock()
	defer c.executorLock.Unlock()

	if _, ok := c.cronExecutorMap[cronFHPAKey]; !ok {
		return
	}
	if _, ok := c.cronExecutorMap[cronFHPAKey][ruleName]; !ok {
		return
	}
	ctx := c.cronExecutorMap[cronFHPAKey][ruleName].Stop()
	<-ctx.Done()
	delete(c.cronExecutorMap[cronFHPAKey], ruleName)
}

// StopCronFHPAExecutor stops the executor for specific CronFederatedHPA
func (c *CronHandler) StopCronFHPAExecutor(cronFHPAKey string) {
	c.executorLock.Lock()
	defer c.executorLock.Unlock()

	if _, ok := c.cronExecutorMap[cronFHPAKey]; !ok {
		return
	}
	for _, executor := range c.cronExecutorMap[cronFHPAKey] {
		ctx := executor.Stop()
		<-ctx.Done()
	}

	delete(c.cronExecutorMap, cronFHPAKey)
}

func (c *CronHandler) CreateCronJobForExecutor(namespaceName types.NamespacedName, rule autoscalingv1alpha1.CronFederatedHPARule) error {
	var err error
	timeZone := time.Now().Location()

	if rule.TimeZone != nil {
		timeZone, err = time.LoadLocation(*rule.TimeZone)
		if err != nil {
			// This should not happen because there is validation in webhook
			klog.Infof("Invalid time zone(%s):%v", *rule.TimeZone, err)
			return err
		}
	}
	cronJobExecutor := cron.New(cron.WithLocation(timeZone))

	cronJob := NewCronFederatedHPAJob(c.client, c.eventRecorder, namespaceName, rule)
	if _, err := cronJobExecutor.AddJob(rule.Schedule, cronJob); err != nil {
		klog.Errorf("Create cron job error:%v", err)
		return err
	}
	cronJobExecutor.Start()

	c.executorLock.Lock()
	defer c.executorLock.Unlock()
	ruleExecutorMap := c.cronExecutorMap[namespaceName.String()]
	ruleExecutorMap[rule.Name] = RuleCron{Cron: cronJobExecutor, CronFederatedHPARule: rule}
	return nil
}
