package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"time"

	"github.com/briankopp/grim-reaper/internal/config"
	intk8s "github.com/briankopp/grim-reaper/internal/kubernetes"
	"github.com/briankopp/grim-reaper/internal/reaper"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

func main() {
	// read command line flags into config
	var (
		debugLog      = flag.Bool("debug", false, "whether to use debug logs")
		dryRun        = flag.Bool("dry-run", false, "whether to run in dry-run mode")
		lockNamespace = flag.String("namespace", "default", "the namespace where grim-reaper is deployed")
		name          = flag.String("name", "grim-reaper", "the grim reaper name - used for locking purposes")

		kubeConfigPath = flag.String("kube-config-path", "", "the file path to the kubeconfig file, leave blank if incluster config")
		kubeAPIServer  = flag.String("kube-api-server", "", "the kube api server, leave blank if incluster config")

		minNodes            = flag.Int("min-nodes", 3, "minimum number of nodes, grim-reaper won't reap if it means going below this number")
		maxNodesDelete      = flag.Int("max-nodes-delete", 2, "maximum number of nodes grim-reaper will harvest")
		fractionNodesDelete = flag.Float64("harvest-fraction", 0.05, "the fraction of the nodes to harvest")

		nodeLabelSelector    = flag.String("node-selector", "", "a selector used to filter nodes for considerations")
		dealBreakerPodLabels = flag.String("deal-breaker-pods", "", "a pod selector which prevents harvest on a node if present")

		evictionTimeout     = flag.Duration("eviction-timeout", 300*time.Second, "the amount of time to wait for pods to evict")
		deletionTimeout     = flag.Duration("deletion-timeout", 300*time.Second, "how long to wait for a pod to delete after an indeterminate eviction result")
		gracefulTermination = flag.Duration("termination-timeout", 60*time.Second, "the default graceful termination period if none present")
		drainDelay          = flag.Duration("drain-delay", 60*time.Second, "the amount of time to wait after cordoning to start draining")

		leaderElectionLeaseDuration = flag.Duration("leader-lease-duration", 30*time.Second, "leader lease duration time")
		leaderElectionRetryPeriod   = flag.Duration("leader-retry-period", 2*time.Second, "how often to retry leader lock")
		leaderElectionRenewDeadline = flag.Duration("leader-renew-deadline", 20*time.Second, "leader election renewal deadline")
	)
	flag.Parse()
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	if *debugLog {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	config := config.Settings{
		DryRun:                *dryRun,
		MinNodes:              *minNodes,
		MaxNodesDelete:        *maxNodesDelete,
		FractionNodesToDelete: *fractionNodesDelete,
		NodeLabelSelector:     *nodeLabelSelector,
		DealBreakerPodLabels:  *dealBreakerPodLabels,
		EvictionTimeout:       *evictionTimeout,
		GracefulTermination:   *gracefulTermination,
		EvictDeletionTimeout:  *deletionTimeout,
		DelayAfterCordon:      *drainDelay,
	}
	err := assertConfigValid(config)
	if err != nil {
		panic("configuration invalid")
	}

	// make the kubernetes config
	k8sClient, err := clientcmd.BuildConfigFromFlags(*kubeAPIServer, *kubeConfigPath)
	if err != nil {
		log.Fatal().Err(err).Msg("error creating client")
		os.Exit(1)
	}
	clientSet, err := kubernetes.NewForConfig(k8sClient)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// acquire lock
	id, err := os.Hostname()
	if err != nil {
		panic("error getting hostname")
	}
	lock, err := resourcelock.New(
		resourcelock.ConfigMapsResourceLock,
		*lockNamespace,
		*name,
		clientSet.CoreV1(),
		clientSet.CoordinationV1(),
		resourcelock.ResourceLockConfig{
			Identity: id, // TODO event notifier
		},
	)

	if err != nil {
		log.Fatal().Err(err).Msg("error acquiring leader election lock")
		os.Exit(1)
	}

	leaderelection.RunOrDie(
		ctx,
		leaderelection.LeaderElectionConfig{
			Lock:          lock,
			LeaseDuration: *leaderElectionLeaseDuration,
			RenewDeadline: *leaderElectionRenewDeadline,
			RetryPeriod:   *leaderElectionRetryPeriod,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: func(cx context.Context) {
					log.Info().Msg("grim-reaper started leading")
					runGrimReaper(config, clientSet)
					log.Info().Msg("grim-reaper finished running, keep alive for another minute to allow metric collection")
					time.Sleep(60 * time.Minute)
				},
				OnStoppedLeading: func() {
					log.Fatal().Msg("grim-reaper lost leader election")
					os.Exit(1)
				},
			},
		},
	)
}

// assertConfigValid makes sure the configuration is valid
func assertConfigValid(config config.Settings) error {
	configInvalid := false
	if config.MinNodes <= 0 {
		log.Fatal().Msgf("config min nodes must be positive and non-zero, got %v", config.MinNodes)
		configInvalid = true
	}
	if config.FractionNodesToDelete < 0 || config.FractionNodesToDelete >= 1 {
		log.Fatal().Msgf("config fraction nodes to delete must be less than 1 and greater than or equal to 0, got %v", config.FractionNodesToDelete)
		configInvalid = true
	}

	if configInvalid {
		return errors.New("configuration is invalid")
	}

	return nil
}

func runGrimReaper(config config.Settings, client *kubernetes.Clientset) error {
	nodeClient := intk8s.NewNodeInterface(config, client)
	reaper := reaper.NewGrimReaper(config, nodeClient)

	reap, passover, err := reaper.GetNodesToReap()
	if err != nil {
		log.Error().Err(err).Msg("error getting nodes to reap")
		return err
	}

	log.Info().Strs("passoverNodes", passover).Strs("reapNodes", reap).Msg("obtained nodes to reap")
	err = reaper.MarkNodesForDestruction(reap)
	if err != nil {
		log.Error().Err(err).Strs("nodeNames", reap).Msg("error marking nodes for deletion")
		return err
	}
	for _, node := range reap {
		err = reaper.Harvest(node)
		if err != nil {
			log.Error().Err(err).Str("nodeName", node).Msg("error deleting node")
			return err
		}
		log.Info().Str("nodeName", node).Msg("successfully deleted node")
	}

	return nil
}
