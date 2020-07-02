package config

import "time"

// Settings holds application settings
type Settings struct {
	// MinNodes is the minimum number of nodes allowable in the cluster.
	// The grim-reaper will not delete nodes if it will result in fewer than this number being available
	MinNodes int
	// MaxNodesDelete is the maximum number of nodes that can be deleted by the grim-reaper
	MaxNodesDelete int
	// FractionNodesToDelete is the number of nodes to delete [0-1]
	FractionNodesToDelete float64
	// NodeLabelSelector is the selector to apply when considering nodes to delete
	NodeLabelSelector string
	// DealBreakerPodLabels is a selector for pods which, if present on a node, will exclude that node from deletion consideration
	DealBreakerPodLabels string
	// EvictionTimeout is the amount of time allowed to wait for pods to timeout
	EvictionTimeout time.Duration
	// GracefulTermination is the amount of time to give pods to terminate on eviction
	GracefulTermination time.Duration
	// EvictDeletionTimeout is the amount of time to wait for a pod to delete after uncertain eviction status
	EvictDeletionTimeout time.Duration
	// DelayAfterCordon is how long after making the node unschedulable do we initiate the drain
	DelayAfterCordon time.Duration
}
