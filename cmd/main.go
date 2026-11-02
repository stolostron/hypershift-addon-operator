package main

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	goflag "flag"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stolostron/hypershift-addon-operator/pkg/agent"
	"github.com/stolostron/hypershift-addon-operator/pkg/manager"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	"go.uber.org/zap"
	utilflag "k8s.io/component-base/cli/flag"
	"open-cluster-management.io/addon-framework/pkg/version"
	ctrl "sigs.k8s.io/controller-runtime"
)

func main() {
	rand.Seed(time.Now().UTC().UnixNano())

	// ACM-49899: client-go v0.35.1 enables WatchListClient by default; waiting for
	// initial-events bookmarks can prevent informers from syncing under apiserver load.
	if os.Getenv("KUBE_FEATURE_WatchListClient") == "" {
		_ = os.Setenv("KUBE_FEATURE_WatchListClient", "false")
	}

	pflag.CommandLine.SetNormalizeFunc(utilflag.WordSepNormalizeFunc)
	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	var logger logr.Logger

	zapLog, err := zap.NewDevelopment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start up logger %v\n", err)
		os.Exit(1)
	}

	logger = zapr.NewLogger(zapLog)
	ctrl.SetLogger(logger)

	command := newCommand(logger)

	if err := command.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func newCommand(logger logr.Logger) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hypershift-addon",
		Short: "hypershift addon for acm.",
		Run: func(cmd *cobra.Command, args []string) {
			if err := cmd.Help(); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
			}
			os.Exit(1)
		},
	}

	if v := version.Get().String(); len(v) == 0 {
		cmd.Version = "<unknown>"
	} else {
		cmd.Version = v
	}

	cmd.AddCommand(manager.NewManagerCommand(util.AddonControllerName, logger.WithName("manager")))
	cmd.AddCommand(agent.NewAgentCommand(util.AddonControllerName, logger.WithName("agent")))
	cmd.AddCommand(agent.NewCleanupCommand(util.AddonControllerName, logger.WithName("cleanup")))

	return cmd
}
