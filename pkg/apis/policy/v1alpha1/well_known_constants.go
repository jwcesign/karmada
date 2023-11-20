package v1alpha1

const (
	// PropagationPolicyPermanentIDLabel is the identifier of a PropagationPolicy object.
	// Karmada generates a unique identifier, such as metadata.UUID, for each PropagationPolicy object.
	// This identifier will be used as a label selector to locate corresponding resources, such as ResourceBinding.
	// The reason for generating a new unique identifier instead of simply using metadata.UUID is because:
	// In backup scenarios, when applying the backup resource manifest in a new cluster, the UUID may change.
	PropagationPolicyPermanentIDLabel = "propagationpolicy.karmada.io/permanent-id"

	// ClusterPropagationPolicyPermanentIDLabel is the identifier of a ClusterPropagationPolicy object.
	// Karmada generates a unique identifier, such as metadata.UUID, for each ClusterPropagationPolicy object.
	// This identifier will be used as a label selector to locate corresponding resources, such as ResourceBinding.
	// The reason for generating a new unique identifier instead of simply using metadata.UUID is because:
	// In backup scenarios, when applying the backup resource manifest in a new cluster, the UUID may change.
	ClusterPropagationPolicyPermanentIDLabel = "clusterpropagationpolicy.karmada.io/permanent-id"

	// PropagationPolicyUIDLabel is the uid of PropagationPolicy object.
	PropagationPolicyUIDLabel = "propagationpolicy.karmada.io/uid"

	// PropagationPolicyNamespaceAnnotation is added to objects to specify associated PropagationPolicy namespace.
	PropagationPolicyNamespaceAnnotation = "propagationpolicy.karmada.io/namespace"

	// PropagationPolicyNameAnnotation is added to objects to specify associated PropagationPolicy name.
	PropagationPolicyNameAnnotation = "propagationpolicy.karmada.io/name"

	// ClusterPropagationPolicyUIDLabel is the uid of ClusterPropagationPolicy object.
	ClusterPropagationPolicyUIDLabel = "clusterpropagationpolicy.karmada.io/uid"

	// ClusterPropagationPolicyAnnotation is added to objects to specify associated ClusterPropagationPolicy name.
	ClusterPropagationPolicyAnnotation = "clusterpropagationpolicy.karmada.io/name"

	// PropagationPolicyNamespaceLabel is added to objects to specify associated PropagationPolicy namespace.
	PropagationPolicyNamespaceLabel = "propagationpolicy.karmada.io/namespace"

	// PropagationPolicyNameLabel is added to objects to specify associated PropagationPolicy's name.
	PropagationPolicyNameLabel = "propagationpolicy.karmada.io/name"

	// ClusterPropagationPolicyLabel is added to objects to specify associated ClusterPropagationPolicy.
	ClusterPropagationPolicyLabel = "clusterpropagationpolicy.karmada.io/name"

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
