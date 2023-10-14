package v1alpha2

const (
	// ResourceBindingIDLabel is the ID of ResourceBinding object.
	ResourceBindingIDLabel = "resourcebinding.karmada.io/id"

	// ClusterResourceBindingIDLabel is the id of ClusterResourceBinding object.
	ClusterResourceBindingIDLabel = "clusterresourcebinding.karmada.io/id"

	// WorkNamespaceAnnotationKey is added to objects to specify associated Work's namespace.
	WorkNamespaceAnnotationKey = "work.karmada.io/namespace"

	// WorkNameAnnotationKey is added to objects to specify associated Work's name.
	WorkNameAnnotationKey = "work.karmada.io/name"

	// WorkIDLabel is the id of Work object.
	WorkIDLabel = "work.karmada.io/id"

	// ResourceBindingNamespaceAnnotationKey is added to object to describe the associated ResourceBinding's namespace.
	// It is added to:
	// - Work object: describes the namespace of ResourceBinding which the Work derived from.
	// - Manifest in Work object: describes the namespace of ResourceBinding which the manifest derived from.
	ResourceBindingNamespaceAnnotationKey = "resourcebinding.karmada.io/namespace"

	// ResourceBindingNameAnnotationKey is added to object to describe the associated ResourceBinding's name.
	// It is added to:
	// - Work object: describes the name of ResourceBinding which the Work derived from.
	// - Manifest in Work object: describes the name of ResourceBinding which the manifest derived from.
	ResourceBindingNameAnnotationKey = "resourcebinding.karmada.io/name"

	// ClusterResourceBindingAnnotationKey is added to object to describe associated ClusterResourceBinding's name.
	// It is added to:
	// - Work object: describes the name of ClusterResourceBinding which the Work derived from.
	// - Manifest in Work object: describes the name of ClusterResourceBinding which the manifest derived from.
	ClusterResourceBindingAnnotationKey = "clusterresourcebinding.karmada.io/name"
)

// Define resource conflict resolution
const (
	// ResourceConflictResolutionAnnotation is added to the resource template to specify how to resolve the conflict
	// in case of resource already existing in member clusters.
	// The valid value is:
	//   - overwrite: always overwrite the resource if already exist. The resource will be overwritten with the
	//     configuration from control plane.
	//   - abort: do not resolve the conflict and stop propagating to avoid unexpected overwrites (default value)
	// Note: Propagation of the resource template without this annotation will fail in case of already exists.
	ResourceConflictResolutionAnnotation = "work.karmada.io/conflict-resolution"

	// ResourceConflictResolutionOverwrite is a value of ResourceConflictResolutionAnnotation, indicating the overwrite strategy.
	ResourceConflictResolutionOverwrite = "overwrite"

	// ResourceConflictResolutionAbort is a value of ResourceConflictResolutionAnnotation, indicating stop propagating.
	ResourceConflictResolutionAbort = "abort"
)

// Define annotations that are added to the resource template.
const (
	// ResourceTemplateUIDAnnotation is the annotation that is added to the manifest in the Work object.
	// The annotation is used to identify the resource template which the manifest is derived from.
	// The annotation can also be used to fire events when syncing Work to member clusters.
	// For more details about UID, please refer to:
	// https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#uids
	ResourceTemplateUIDAnnotation = "resourcetemplate.karmada.io/uid"
	// ManagedLabels is the annotation that is added to the manifest in the Work object.
	// It is used to identify the label keys that are present in the resource template.
	// With this annotation, Karmada is able to accurately synchronize label changes
	// against resource template and avoid the problem of accidentally retaining
	// the deleted labels.
	// E.g. "resourcetemplate.karmada.io/managed-labels: bar,foo".
	// Note: the keys will be sorted in alphabetical order.
	ManagedLabels = "resourcetemplate.karmada.io/managed-labels"

	// ManagedAnnotation is the annotation that is added to the manifest in the Work object.
	// It is used to identify the annotation keys that are present in the resource template.
	// With this annotation, Karmada is able to accurately synchronize annotation changes
	// against resource template and avoid the problem of accidentally retaining
	// the deleted annotations.
	// E.g. "resourcetemplate.karmada.io/managed-annotations: bar,foo".
	// Note: the keys will be sorted in alphabetical order.
	ManagedAnnotation = "resourcetemplate.karmada.io/managed-annotations"
)

// Define eviction reasons.
const (
	// EvictionReasonTaintUntolerated describes the eviction is triggered
	// because can not tolerate taint or exceed toleration period of time.
	EvictionReasonTaintUntolerated = "TaintUntolerated"

	// EvictionReasonApplicationFailure describes the eviction is triggered
	// because the application fails and reaches the condition of ApplicationFailoverBehavior.
	EvictionReasonApplicationFailure = "ApplicationFailure"
)

// Define eviction producers.
const (
	// EvictionProducerTaintManager represents the name of taint manager.
	EvictionProducerTaintManager = "TaintManager"
)
