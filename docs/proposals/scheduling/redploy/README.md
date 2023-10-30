---
title: 支持用户批量变更应用部署规则
authors:
- "@jwcesign" # Authors' github accounts here.
reviewers:
- TBD
approvers:
- TBD

creation-date: 2023-10-31

---

# 支持用户批量变更应用部署规则

<!--
This is the title of your KEP. Keep it short, simple, and descriptive. A good
title can help communicate what the KEP is and should be considered as part of
any review.
-->

## Summary

<!--
This section is incredibly important for producing high-quality, user-focused
documentation such as release notes or a development roadmap.

A good summary is probably at least a paragraph in length.
-->

在用户使用场景中，存在默认的部署规则无法满足，需要自定义部署规则的场景，如默认为均匀部署到多个集群中，而深度学习需要部署到同一个集群。此时，就需要批量变更用户已经被默认部署规则分发的应用。

当前，`ClusterPropagationPolicy/PropagationPolicy` 支持用户通过配置优先级和中断策略，变更应用部署规则，如使用如下yaml可以变更之前匹配更低优先级 `ClusterPropagationPolicy`(假设此为默认部署规则) 的Deployment：
```yaml
apiVersion: policy.karmada.io/v1alpha1
kind: ClusterPropagationPolicy
metadata:
  name: nginx-pp
spec:
  placement:
    clusterAffinity:
      clusterNames:
      - member1
  priority: 10
  preemption: Always
  resourceSelectors:
  - apiVersion: apps/v1
    kind: Deployment
    name: nginx-deploy
```

但是，在配置中断情况下，需要具体指定资源名字，无法批量变更已部署的应用，也无法匹配未来将要部署的应用，部署效率较低。

此Proposal提出了一种支持批量变更应用部署的设计，提升用户使用效率。

## Motivation

<!--
This section is for explicitly listing the motivation, goals, and non-goals of
this KEP.  Describe why the change is important and the benefits to users.
-->

作为使用Karmada对其他团队提供平台服务的管理员，为了让其他团队用户快捷的将部署的应用下发成员集群，会通过配置默认CPP（通用匹配所有用户资源），使用默认策略分发用户应用到成员集群。

```yaml
apiVersion: policy.karmada.io/v1alpha1
kind: ClusterPropagationPolicy
metadata:
  name: default-cpp
spec:
  placement:
    clusterAffinity:
      clusterNames:
      - member1
      - member2
  priority: 0
  resourceSelectors:
  - apiVersion: apps/v1
    kind: Deployment
```

而某些团队有自定义应用分发需求，如果Karmada支持用户批量变更应用部署规则（已有的以及未来用户需要部署的应用），将极大提升用户自定义应用部署效率。


### Goals

<!--
List the specific goals of the KEP. What is it trying to achieve? How will we
know that this has succeeded?
-->

1. 支持用户批量变更/自定义应用部署策略。
1. 变更部署策略时，影响范围要尽量小，如只影响当前操作用户的资源。

### Non-Goals

<!--
What is out of scope for this KEP? Listing non-goals helps to focus discussion
and make progress.
-->

## Proposal

<!--
This is where we get down to the specifics of what the proposal actually is.
This should have enough detail that reviewers can understand exactly what
you're proposing, but should not include things like API designs or
implementation. What is the desired outcome and how do we measure success?.
The "Design Details" section below is for the real
nitty-gritty.
-->

### User Stories (Optional)

<!--
Detail the things that people will be able to do if this KEP is implemented.
Include as much detail as possible so that people can understand the "how" of
the system. The goal here is to make this feel real for users without getting
bogged down.
-->

#### Story 1
作为 Karmada 多租平台用户，管理员使用 Karmada 对多个团队提供平台服务，其会默认配置一个CPP，用于默认分发部署。在某些时候，我希望我能对自己的应用（现在和未来要部署的），批量变更部署策略。

### Notes/Constraints/Caveats (Optional)

<!--
What are the caveats to the proposal?
What are some important details that didn't come across above?
Go in to as much detail as necessary here.
This might be a good place to talk about core concepts and how they relate.
-->

现有Policy抢占策略：
1. 隐式优先级不支持抢占（CPP的 `.spec.priority` 配置为空）。
1. 在抢占(Policy的`.spec.preemption`为 true)开启情况下，PP会抢占CPP，不论PP的优先级是否高于CPP。
1. 在抢占(Policy的`.spec.preemption`为 true)开启情况下，PP会抢占更低优先级的PP,CPP会抢占更低优先级的CPP。

### Risks and Mitigations

<!--
What are the risks of this proposal, and how do we mitigate?

How will security be reviewed, and by whom?

How will UX be reviewed, and by whom?

Consider including folks who also work outside the SIG or subproject.
-->

## Design Details

<!--
This section should contain enough information that the specifics of your
change are understandable. This may include API specs (though not always
required) or even code snippets. If there's any ambiguity about HOW your
proposal will be implemented, this is the place to discuss them.
-->

### 方案一：扩展PP/CPP抢占特性，支持namespace正则匹配

对于之前的抢占设计，Kamrada 会限制到具体的资源（指定apiVersion/kind/name），避免大量抢占出现性能和较大影响范围的问题。

同时，在K8s多租设计下，不同租户通常通过不同ns实现资源和权限隔离（K8s官方推荐），并且多租系统下，用户的ns存在一定的命名规则，如 `tenantid-xxx`，存在固定前缀。

所以，此方案在限制影响范围的情况下（限制到单用户namespace）,支持如下能力：
1. PP支持抢占此命名空间下的其他更低优先级的PP，同时支持抢占所有其他CPP（匹配此命名的资源的CPP）
1. CPP支持配置ns正则匹配，抢占其他更低优先级的CPP。

示例配置：
```yaml
apiVersion: policy.karmada.io/v1alpha1
kind: ClusterPropagationPolicy
metadata:
  name: custom-cpp
spec:
  placement:
    clusterAffinity:
      clusterNames:
      - member1
  priority: 3
  preemption: Always
  resourceSelectors:
  - apiVersion: apps/v1
    kind: Deployment
    namespace: user1-*
```

其中，中断流程流程总览如下：

![preemption_process](../policy-preemption/preemption_process.PNG)

#### Components change

##### karmada-controller-manager

1. 在CPP创建/更新时，根据 resoruceSelector 寻找资源，如果存在资源匹配并且满足当前CPP优先级大于此资源已匹配的CPP，更新其RB，最终触发应用重新分发。
1. 在PP创建/更新时，根据 resoruceSelector 寻找资源，如果存在资源匹配并且满足当前PP优先级大于此资源已匹配的CPP，更新其RB，最终触发应用重新分发；如果其资源匹配的是CPP，则直接抢占并更新RB，最终触发应用重新分发。

##### karmada-webhook

1. 开放允许配置resoruceSelector中的name为空，同时不允许命名空间配置为空。
1. 如果配置正则匹配，只允许 {prefix}* 的模式，其他匹配模式不支持。

#### 存在问题

##### 异常配置可影响范围较大

在支持通配的情况下，如果出现异常匹配，仍然可能出现较大影响范围，如正则表达式匹配到其他租户。
如使用如下配置匹配所有 namespace，最后可能造成所有租户应用被重新部署：
```yaml
apiVersion: policy.karmada.io/v1alpha1
kind: ClusterPropagationPolicy
metadata:
  name: custom-cpp
spec:
  placement:
    clusterAffinity:
      clusterNames:
      - member1
  priority: 3
  preemption: Always
  resourceSelectors:
  - apiVersion: apps/v1
    kind: Deployment
    namespace: *
```
可以通过限制到具体的namespace实现：
```yaml
apiVersion: policy.karmada.io/v1alpha1
kind: ClusterPropagationPolicy
metadata:
  name: custom-cpp
spec:
  placement:
    clusterAffinity:
      clusterNames:
      - member1
  priority: 3
  preemption: Always
  resourceSelectors:
  - apiVersion: apps/v1
    kind: Deployment
    namespace: namespace1,namespace2,namespace3
```

##### CPP众多导致优先级较难管理

如果支持通配符，用户可能不断用更高优先级的CPP去定制化不同用用部署规则，最后导致优先级管理非常困难。
如存在如下的 CPP，不断定制化更细粒度的部署规则，最后可能导致优先级管理非常困难：
```yaml
apiVersion: policy.karmada.io/v1alpha1
kind: ClusterPropagationPolicy
metadata:
  name: default-cpp
spec:
  placement:
    clusterAffinity:
      clusterNames:
      - member1
      - ...
  priority: 0
  resourceSelectors:
  - apiVersion: apps/v1
    kind: Deployment
---
# 对大团队设置自定义部署规则
apiVersion: policy.karmada.io/v1alpha1
kind: ClusterPropagationPolicy
metadata:
  name: gourp1-custom-cpp
spec:
  placement:
    clusterAffinity:
      clusterNames:
      - ...
  priority: 1
  preemption: Always
  resourceSelectors:
  - apiVersion: apps/v1
    kind: Deployment
    namespace: group1-*
---
# 对子团队设置自定义部署规则
apiVersion: policy.karmada.io/v1alpha1
kind: ClusterPropagationPolicy
metadata:
  name: gourp1-1-custom-cpp
spec:
  placement:
    clusterAffinity:
      clusterNames:
      - ...
  priority: 2
  preemption: Always
  resourceSelectors:
  - apiVersion: apps/v1
    kind: Deployment
    namespace: group1-1-*
---
# 更多的CPP...
```


##### 对组件性能影响较大

如果使用通配符，在寻找匹配资源时，需要遍历资源，执行正则匹配，比较耗费性能，伪代码如下：
```
  resources = client.List(gvr)
  for _, r = range resources {
    if re.match(r.namespace) {
      matchedResources = append(matchedResources, r)
    }
  }
```


### 方案二：支持CPP黑名单，触发资源黑名单资源重部署

为了减小影响范围，此方案通过支持CPP黑名单，触发资源黑名单资源重部署，实现批量变更应用部署规则，示例yaml如下：
```yaml
# api definition option 1
apiVersion: policy.karmada.io/v1alpha1
kind: ClusterPropagationPolicy
metadata:
  name: custom-cpp
spec:
  placement:
    clusterAffinity:
      clusterNames:
      - member1
      - ...
  priority: 1
  preemption: Always
  resourceSelectors:
  - apiVersion: apps/v1
    kind: Deployment
    exclude:
      namespace: user1-* # or user1-1,user1-2
      name: x # optional
---
# api definition option 2
apiVersion: policy.karmada.io/v1alpha1
kind: ClusterPropagationPolicy
metadata:
  name: custom-cpp
spec:
  placement:
    clusterAffinity:
      clusterNames:
      - member1
      - ...
  priority: 0
  preemption: Always
  resourceSelectors:
  - apiVersion: apps/v1
    kind: Deployment
  excludeResourceSelectors:
  - apiVersion: apps/v1
    kind: Deployment
    namespace: user1-* # or user1-1,user1-2
    name: x #optional
```

最终的处理流程如下：
1. 用户部署自己应用的PP/CPP。
1. 通知平台管理员修改排除用户所属应用。
1. karmada-controller-manager检测到修改变化，重新匹配用户应用所属策略，最终触发应用批量重部署。

#### Components change

##### karmada-controller-manager

1. 在CPP/PP创建/更新时，根据resoruceSelector寻找资源，如果存在已匹配资源并且在黑名单中，执行重新策略匹配，更新其RB，最终触发应用重新分发。
1. 在PP创建/更新时，根据resoruceSelector寻找资源，如果存在已匹配资源并且在黑名单中，执行重新策略匹配，更新其RB。

##### karmada-webhook

1. 如果配置正则匹配，只允许 {prefix}* 的模式，其他匹配模式不支持。
1. 在采用api option 1的情况下，如果exclude中的name为空，不允许配置exclude中的namespace为空。

#### 存在问题

##### 用户部署变更流程较长

实现此方案后，用户的部署变更流程如下：
1. 部署自己的CPP，定义自己的部署规则。
2. 通知管理员修改排除自己的应用。

##### API定义存在定义不一致

现在我们的 PP/CPP 的 `spec.resourceSelectors.namespace` 不知道 `prefix-*` 和 `ns-1,ns2` 配置模式，和新增加的 API 未保持一致，建议后续修改支持相同的匹配模式。

### Test Plan

<!--
**Note:** *Not required until targeted at a release.*

Consider the following in developing a test plan for this enhancement:
- Will there be e2e and integration tests, in addition to unit tests?
- How will it be tested in isolation vs with other components?

No need to outline all test cases, just the general strategy. Anything
that would count as tricky in the implementation, and anything particularly
challenging to test, should be called out.

-->

- All current testing should be passed, no break change would be involved by this feature.
- Add new E2E tests to cover the feature, the scope should include:
  * preemption between high-priority pp/cpp and low-priority pp/cpp
  * preemption between pp and cpp
  * preemption is disabled.

## Alternatives

<!--
What other approaches did you consider, and why did you rule them out? These do
not need to be as detailed as the proposal, but should include enough
information to express the idea and why it was not acceptable.
-->

<!--
Note: This is a simplified version of kubernetes enhancement proposal template.
https://github.com/kubernetes/enhancements/tree/3317d4cb548c396a430d1c1ac6625226018adf6a/keps/NNNN-kep-template
-->