package main

import (
	"fmt"
	"os"

	"github.com/flant/kubedog/pkg/kube"
	"github.com/flant/kubedog/pkg/kubedog"
	"github.com/spf13/cobra"
)

func main() {
	err := kube.Init()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to initialize kube: %s", err)
		os.Exit(1)
	}

	var namespace string

	rootCmd := &cobra.Command{Use: "kubedog"}

	watchCmd := &cobra.Command{Use: "watch"}
	rootCmd.AddCommand(watchCmd)

	jobCmd := &cobra.Command{
		Use:   "job NAME",
		Short: "Watch job until job terminated",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			err := kubedog.WatchJobTillDone(name, namespace, kube.Kubernetes)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error watching job `%s` in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	}
	jobCmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "kubernetes namespace")
	watchCmd.AddCommand(jobCmd)

	deploymentCmd := &cobra.Command{
		Use:   "deployment NAME",
		Short: "Watch deployment until deployment is ready",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			err := kubedog.WatchDeploymentTillReady(name, namespace)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error watching deployment `%s` in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	}
	deploymentCmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "kubernetes namespace")
	watchCmd.AddCommand(deploymentCmd)

	err = rootCmd.Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}
