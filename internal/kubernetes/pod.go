package kubernetes

import (
	"time"

	"github.com/briankopp/grim-reaper/internal/config"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/api/policy/v1beta1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// PodEvictor is responsible for pod eviction
type PodEvictor interface {
	shouldEvict(pod v1.Pod) bool
	evict(pod v1.Pod, abort <-chan struct{}) error
}

// kubernetesPodEvictor implements pod eviction with kubernetes client
type kubernetesPodEvictor struct {
	dryRun   bool
	client   kubernetes.Interface
	settings config.Settings
}

func (m *kubernetesPodEvictor) shouldEvict(pod v1.Pod) bool {
	// don't evict if daemonset
	isDS, daemonSetName := getPodDaemonSet(pod)
	if isDS {
		exists, err := m.daemonsetExists(pod.Namespace, daemonSetName)
		if exists || err != nil {
			return false
		}
	}

	// don't evict if statefulset
}

func getPodDaemonSet(pod v1.Pod) (bool, string) {
	c := metav1.GetControllerOf(&pod)
	if c != nil && c.Kind == "DaemonSet" {
		return true, c.Name
	}

	return false, ""
}

func (m *kubernetesPodEvictor) daemonsetExists(namespace string, name string) (bool, error) {
	_, err := m.client.ExtensionsV1beta1().DaemonSets(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		if apiErrors.IsNotFound(err) {
			return false, nil
		} else {
			log.Error().Err(err).Str("namespace", namespace).Str("name", name).Msg("error getting daemonset")
			return true, err
		}
	}
	return true, nil
}

func (m *kubernetesPodEvictor) evict(pod v1.Pod, abort <-chan struct{}) error {
	terminationGracePeriod := int64(m.settings.GracefulTermination.Seconds())
	if pod.Spec.TerminationGracePeriodSeconds != nil && *pod.Spec.TerminationGracePeriodSeconds < terminationGracePeriod {
		terminationGracePeriod = *pod.Spec.TerminationGracePeriodSeconds
	}

	// evict the pod, keep trying until it errors or we get an abort signal
	for {
		select {
		case <-abort:
			return errors.New("pod eviction aborted")
		default:
			evictOptions := v1beta1.Eviction{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: pod.Namespace,
					Name:      pod.Name,
				},
				DeleteOptions: &metav1.DeleteOptions{
					GracePeriodSeconds: terminationGracePeriod,
				},
			}
			err := m.client.CoreV1().Pods(pod.Namespace).Evict(&evictOptions)

			if err == nil {
				// TODO wait for a while to see if the pod deletes
				log.Error().Err(err).Str("namespace", pod.Namespace).Str("podName", pod.Name).Msg("unexpected eviction response, unclear whether pod evicted")
				return errors.Errorf("unable to tell if pod %v in namespace %v was evicted", pod.Name, pod.Namespace)
			}

			// api should return not found if success
			if apiErrors.IsNotFound(err) {
				return nil
			}

			// if not 429 throttle, something else is wrong
			if !apiErrors.IsTooManyRequests(err) {
				log.Error().Err(err).Str("namespace", pod.Namespace).Str("podName", pod.Name).Msg("error evicting pod")
				return errors.Wrapf(err, "error evicting pod %v from namespace %v", pod.Name, pod.Namespace)
			}

			// else, 429 throttle, come back later
			time.Sleep(5 * time.Second)
		}
	}
	return nil
}

func (m *kubernetesPodEvictor) waitToSeeIfPodDeletes(pod v1.Pod, now time.Time) error {
	timeoutTime := now.Add(m.settings.DeletionTimeout)
	for {
		delPod, err := m.client.CoreV1().Pods(pod.Namespace).Get(pod.Name, metav1.GetOptions{})
		if err != nil && apiErrors.IsNotFound(err) {
			return nil
		}

		if err != nil {
			log.Error().Err(err).Str("namespace", pod.Namespace).Str("podName", pod.Name).Msg("error checking if pod exists")
		}

	}
}
