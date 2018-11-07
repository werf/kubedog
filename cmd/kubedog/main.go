package main

import (
	"fmt"
	"os"
	"time"

	"github.com/flant/kubedog/pkg/kube"
	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/trackers/rollout"
	"github.com/spf13/cobra"
)

func main() {
	err := kube.Init()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to initialize kube: %s", err)
		os.Exit(1)
	}

	var namespace string
	var timeoutSeconds uint
	var subCmd *cobra.Command

	makeTrackerOptions := func() tracker.Options {
		return tracker.Options{Timeout: time.Second * time.Duration(timeoutSeconds)}
	}

	rootCmd := &cobra.Command{Use: "kubedog"}
	rootCmd.PersistentFlags().StringVarP(&namespace, "namespace", "n", "default", "kubernetes namespace")
	rootCmd.PersistentFlags().UintVarP(&timeoutSeconds, "timeout", "t", 300, "watch timeout in seconds")

	subCmd = &cobra.Command{Use: "rollout"}
	rootCmd.AddCommand(subCmd)

	jobCmd := &cobra.Command{
		Use:   "job NAME",
		Short: "Track Job rollout (until Job done)",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			err := rollout.TrackJob(name, namespace, kube.Kubernetes, makeTrackerOptions())
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error tracking Job `%s` rollout in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	}
	subCmd.AddCommand(jobCmd)

	deploymentCmd := &cobra.Command{
		Use:   "deployment NAME",
		Short: "Track Deployment rollout (until Deployment ready)",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			err := rollout.TrackDeployment(name, namespace, kube.Kubernetes, makeTrackerOptions())
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error tracking Deployment `%s` rollout in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	}
	subCmd.AddCommand(deploymentCmd)

	podCmd := &cobra.Command{
		Use:   "pod NAME",
		Short: "Track Pod rollout (until Pod ready)",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			err := rollout.TrackPod(name, namespace, kube.Kubernetes, makeTrackerOptions())
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error tracking Pod `%s` rollout in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	}
	subCmd.AddCommand(podCmd)

	err = rootCmd.Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}
