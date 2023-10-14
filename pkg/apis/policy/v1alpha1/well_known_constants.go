package v1alpha1

const (
	// PropagationPolicyIDLabel is the id of PropagationPolicy object.
	PropagationPolicyIDLabel = "propagationpolicy.karmada.io/id"

	// PropagationPolicyNamespaceAnnotation is added to objects to specify associated PropagationPolicy namespace.
	PropagationPolicyNamespaceAnnotation = "propagationpolicy.karmada.io/namespace"

	// PropagationPolicyNameAnnotation is added to objects to specify associated PropagationPolicy name.
	PropagationPolicyNameAnnotation = "propagationpolicy.karmada.io/name"

	// ClusterPropagationPolicyIDLabel is the uid of ClusterPropagationPolicy object.
	ClusterPropagationPolicyIDLabel = "clusterpropagationpolicy.karmada.io/id"

	// ClusterPropagationPolicyAnnotation is added to objects to specify associated ClusterPropagationPolicy name.
	ClusterPropagationPolicyAnnotation = "clusterpropagationpolicy.karmada.io/name"

	// NamespaceSkipAutoPropagationLabel is added to namespace objects to indicate if
	// the namespace should be skipped from propagating by the namespace controller.
	// For example, a namespace with the following label will be skipped:
	//   labels:
	//     namespace.karmada.io/skip-auto-propagation: "true"
	//
	// NOTE: If create a ns without this label, then patch it with this label, the ns will not be
	// synced to new member clusters, but old member clusters still have it.
	NamespaceSkipAutoPropagationLabel = "namespace.karmada.io/skip-auto-propagation"
)
