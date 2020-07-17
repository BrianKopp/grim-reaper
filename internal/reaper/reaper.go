package reaper

import (
	"time"

	"github.com/briankopp/grim-reaper/internal/config"
	"github.com/briankopp/grim-reaper/internal/kubernetes"
	"github.com/rs/zerolog/log"
)

// GrimReaper prepares nodes for being deleted
type GrimReaper interface {
	// GetNodesToReap determines which nodes will be deleted
	GetNodesToReap() (reap []string, passover []string, err error)
	// MarkNodesForDestruction marks nodes to be deleted
	MarkNodesForDestruction(nodes []string) error
	// Harvest actually deletes a node
	Harvest(node string) error
}

type theGrimReaper struct {
	config     config.Settings
	nodeClient kubernetes.NodeInterface
}

// NewGrimReaper makes a new implementation of the grim reaper
func NewGrimReaper(config config.Settings, nodeClient kubernetes.NodeInterface) GrimReaper {
	return &theGrimReaper{
		config:     config,
		nodeClient: nodeClient,
	}
}

// GetNodesToReap determines which nodes will be deleted
func (m *theGrimReaper) GetNodesToReap() (reap []string, passover []string, err error) {
	allNodes, err := m.nodeClient.ListNodes(m.config.NodeLabelSelector)
	if err != nil {
		return nil, nil, err
	}

	// TODO grab nodes that are already marked for destruction

	// get the number of nodes to try to reap
	numNodes := len(allNodes.Items)
	nodesToReap := int(m.config.FractionNodesToDelete * float64(numNodes))
	if (numNodes - nodesToReap) < m.config.MinNodes {
		nodesToReap = numNodes - m.config.MinNodes
	}
	if nodesToReap > m.config.MaxNodesDelete {
		nodesToReap = m.config.MaxNodesDelete
	}

	// loop over the nodes, looking for the lowest ones
	consideredNodes := []string{}
	for range allNodes.Items {
		// get the oldest next node
		oldestCreateDate := time.Now()
		lowestIndex := -1
		for i, n := range allNodes.Items {
			if n.CreationTimestamp.Time.Before(oldestCreateDate) {
				// make sure we haven't already considered it
				considered := false
				for _, c := range consideredNodes {
					if n.Name == c {
						considered = true
					}
				}
				if !considered {
					lowestIndex = i
					oldestCreateDate = n.CreationTimestamp.Time
				}
			}
		}

		node := allNodes.Items[lowestIndex]

		consideredNodes = append(consideredNodes)
		// check if node is eligible for deletion
		dealBreak, err := m.nodeClient.HasDealBreakerPods(node.Name)
		if err != nil {
			return nil, nil, err
		}
		if dealBreak {
			log.Info().Str("nodeName", node.Name).Msg("skipping node since has deal breaker pods on it")
			passover = append(passover, node.Name)
		} else {
			reap = append(reap, node.Name)
		}

		if len(reap) == nodesToReap {
			return reap, passover, nil
		}
	}

	return
}

func (m *theGrimReaper) MarkNodesForDestruction(nodes []string) error {
	for _, n := range nodes {
		log.Debug().Str("nodeName", n).Msg("marking node for deletion")
		err := m.nodeClient.MarkNodeToDrain(n)
		if err != nil {
			log.Error().Err(err).Str("nodeName", n).Msg("error marking node for deletion")
			return err
		}
	}

	return nil
}

func (m *theGrimReaper) Harvest(node string) error {
	err := m.nodeClient.CordonNode(node)
	if err != nil {
		log.Error().Err(err).Str("nodeName", node).Msg("error cordoning node")
		return err
	}

	log.Info().Str("nodeName", node).Msg("successfully cordoned node")
	time.Sleep(m.config.DelayAfterCordon)
	err = m.nodeClient.DrainNode(node)
	if err != nil {
		log.Error().Err(err).Str("nodeName", node).Msg("error draining node")
		return err
	}

	return nil
}
