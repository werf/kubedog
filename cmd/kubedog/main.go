package main

import (
	"fmt"
	"os"
	"time"

	"github.com/flant/kubedog/pkg/kube"
	"github.com/flant/kubedog/pkg/kubedog"
	"github.com/flant/kubedog/pkg/monitor"
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

	rootCmd := &cobra.Command{Use: "kubedog"}
	rootCmd.PersistentFlags().StringVarP(&namespace, "namespace", "n", "default", "kubernetes namespace")
	rootCmd.PersistentFlags().UintVarP(&timeoutSeconds, "timeout", "t", 300, "watch timeout in seconds")

	watchCmd := &cobra.Command{Use: "watch"}
	rootCmd.AddCommand(watchCmd)

	jobCmd := &cobra.Command{
		Use:   "job NAME",
		Short: "Watch job until job terminated",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			err := kubedog.WatchJobTillDone(name, namespace, kube.Kubernetes, monitor.WatchOptions{Timeout: time.Second * time.Duration(timeoutSeconds)})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error watching job `%s` in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	}
	watchCmd.AddCommand(jobCmd)

	deploymentCmd := &cobra.Command{
		Use:   "deployment NAME",
		Short: "Watch deployment until deployment is ready",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			err := kubedog.WatchDeploymentTillReady(name, namespace, kube.Kubernetes)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error watching deployment `%s` in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	}
	watchCmd.AddCommand(deploymentCmd)

	podCmd := &cobra.Command{
		Use:   "pod NAME",
		Short: "Watch pod",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			err := kubedog.WatchPodTillDone(name, namespace, kube.Kubernetes, monitor.WatchOptions{Timeout: time.Second * time.Duration(timeoutSeconds)})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error watching pod `%s` in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	}
	watchCmd.AddCommand(podCmd)

	err = rootCmd.Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}
