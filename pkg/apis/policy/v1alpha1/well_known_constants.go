package v1alpha1

const (
	// PropagationPolicyNamespaceAnnotation is added to objects to specify associated PropagationPolicy namespace.
	PropagationPolicyNamespaceAnnotation = "propagationpolicy.karmada.io/namespace"

	// PropagationPolicyNamespaceLabel is added to objects to specify associated PropagationPolicy namespace.
	PropagationPolicyNamespaceLabel = "propagationpolicy.karmada.io/namespace"

	// PropagationPolicyNameAnnotation is added to objects to specify associated PropagationPolicy name.
	PropagationPolicyNameAnnotation = "propagationpolicy.karmada.io/name"

	// PropagationPolicyNameLabel is added to objects to specify associated PropagationPolicy's name.
	PropagationPolicyNameLabel = "propagationpolicy.karmada.io/name"

	// PropagationPolicyReferenceKey is the key of PropagationPolicy object.
	PropagationPolicyReferenceKey = "propagationpolicy.karmada.io/key"

	// ClusterPropagationPolicyAnnotation is added to objects to specify associated ClusterPropagationPolicy.
	ClusterPropagationPolicyAnnotation = "clusterpropagationpolicy.karmada.io/name"

	// ClusterPropagationPolicyLabel is added to objects to specify associated ClusterPropagationPolicy.
	ClusterPropagationPolicyLabel = "clusterpropagationpolicy.karmada.io/name"

	// ClusterPropagationPolicyReferenceKey is the key of ClusterPropagationPolicy object.
	ClusterPropagationPolicyReferenceKey = "clusterpropagationpolicy.karmada.io/key"

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
