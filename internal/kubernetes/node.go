package kubernetes

import (
	"time"

	"github.com/briankopp/grim-reaper/internal/config"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
)

// NodeInterface handles node commands
type NodeInterface interface {
	ListNodes(labelSelector string) (*v1.NodeList, error)
	HasDealBreakerPods(nodeName string) (bool, error)
	CordonNode(name string) error
	MarkNodeToDrain(name string) error
	DrainNode(name string) error
}

// kubernetesNodeInterface implements the NodeInterface using the standard golang client
type kubernetesNodeInterface struct {
	dryRun   bool
	client   kubernetes.Interface
	evictor  PodEvictor
	settings config.Settings
}

// ListNodes fetches nodes using a label selector
func (m *kubernetesNodeInterface) ListNodes(labelSelector string) (*v1.NodeList, error) {
	nodes, err := m.client.CoreV1().Nodes().List(metav1.ListOptions{LabelSelector: labelSelector})

	if err != nil {
		log.Error().Err(err).Msg("error listing nodes")
		return nil, errors.Wrap(err, "error listing nodes")
	}

	log.Debug().Msg("successfully fetched nodes")
	return nodes, nil
}

// DrainNode handles node drain
func (m *kubernetesNodeInterface) DrainNode(name string) error {
	// if dry run, don't do anything
	if m.dryRun {
		log.Info().Str("nodeName", name).Msg("skipping drain node since dry run")
		return nil
	}

	log.Debug().Str("nodeName", name).Msg("draining node")
	podsToDrain, err := m.listPodsToEvict(name)
	if err != nil {
		return err
	}

	err = m.evictPods(name, podsToDrain)
	if err != nil {
		log.Error().Err(err).Str("nodeName", name).Msg("error evicting pods from node")
	} else {
		log.Info().Str("nodeName", name).Msg("successfully drained node")
	}

	return err
}

// listPodsToEvict gets all pods on a particular node
func (m *kubernetesNodeInterface) listPodsToEvict(nodeName string) ([]v1.Pod, error) {
	pods, err := m.client.CoreV1().Pods(metav1.NamespaceAll).List(metav1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName}).String(),
	})

	if err != nil {
		log.Error().Err(err).Str("node", nodeName).Msg("error listing pods")
		return nil, err
	}

	// filter out pods to evict
	evictPods := []v1.Pod{}
	for _, pod := range pods.Items {
		if m.evictor.shouldEvict(pod) {
			evictPods = append(evictPods, pod)
		}
	}

	return evictPods, nil
}

func (m *kubernetesNodeInterface) evictPods(nodeName string, pods []v1.Pod) error {
	// make an abort channel which will stop the eviction process
	abort := make(chan struct{})
	defer close(abort)

	// make an channel to collect results, either nil, or error
	results := make(chan error, 1)

	for _, pod := range pods {
		p := pod
		go func() {
			results <- m.evictor.evict(p, abort)
		}()
	}

	timeout := time.After(m.settings.EvictionTimeout)

	// expect N results
	for range pods {
		select {
		case err := <-results:
			if err != nil {
				log.Error().Err(err).Str("node", nodeName).Msg("error evicting pods")
				return errors.Wrap(err, "error evicting pod")
			}
		case <-timeout:
			return errors.New("error evicting pods, timed out")
		}
	}

	return nil
}
