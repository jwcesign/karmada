package v1alpha1

const (
	// PropagationPolicyNamespaceAnnotation is added to objects to specify associated PropagationPolicy namespace.
	ResourceBindingNamespaceAnnotation = "resourcebinding.karmada.io/namespace"

	// ResourceBindingNamespaceLabel is added to objects to specify associated ResourceBinding's namespace.
	ResourceBindingNamespaceLabel = "resourcebinding.karmada.io/namespace"

	// ResourceBindingNameAnnotation is added to objects to specify associated ResourceBinding's name.
	ResourceBindingNameAnnotation = "resourcebinding.karmada.io/name"

	// ResourceBindingNameLabel is added to objects to specify associated ResourceBinding's name.
	ResourceBindingNameLabel = "resourcebinding.karmada.io/name"

	// ClusterResourceBindingAnnotation is added to objects to specify associated ClusterResourceBinding.
	ClusterResourceBindingAnnotation = "clusterresourcebinding.karmada.io/name"

	// ClusterResourceBindingLabel is added to objects to specify associated ClusterResourceBinding.
	ClusterResourceBindingLabel = "clusterresourcebinding.karmada.io/name"

	// WorkNamespaceAnnotation is added to objects to specify associated Work's namespace.
	WorkNamespaceAnnotation = "work.karmada.io/namespace"

	// WorkNamespaceLabel is added to objects to specify associated Work's namespace.
	WorkNamespaceLabel = "work.karmada.io/namespace"

	// WorkNameAnnotation is added to objects to specify associated Work's name.
	WorkNameAnnotation = "work.karmada.io/name"

	// WorkNameLabel is added to objects to specify associated Work's name.
	WorkNameLabel = "work.karmada.io/name"

	// ResourceConflictResolutionAnnotation is added to the workload in member clusters to show the reference
	WorkRefrenceKey = "work.karmada.io/key"
)
