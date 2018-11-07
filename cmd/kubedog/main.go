package main

import (
	"fmt"
	"os"
	"time"

	"github.com/flant/kubedog/pkg/kube"
	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/trackers/follow"
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
	var followMode, rolloutMode bool

	makeTrackerOptions := func() tracker.Options {
		return tracker.Options{Timeout: time.Second * time.Duration(timeoutSeconds)}
	}
	setTrackMode := func() {
		if followMode && rolloutMode {
			fmt.Fprintf(os.Stderr, "Options --follow and --rollout cannot be used at the same time!\n")
			os.Exit(1)
		}

		if !followMode && !rolloutMode {
			rolloutMode = true
		}
	}

	rootCmd := &cobra.Command{Use: "kubedog"}
	rootCmd.PersistentFlags().StringVarP(&namespace, "namespace", "n", "default", "kubernetes namespace")
	rootCmd.PersistentFlags().UintVarP(&timeoutSeconds, "timeout", "t", 300, "watch timeout in seconds")

	trackCmd := &cobra.Command{Use: "track"}
	rootCmd.AddCommand(trackCmd)

	trackCmd.PersistentFlags().BoolVarP(&followMode, "follow", "f", false, `Follow tracking mode.
In the follow mode simply print resource events infinitely.
Rollout tracking mode used by default.
Options --follow and --rollout cannot be used at the same time.`)

	trackCmd.PersistentFlags().BoolVarP(&rolloutMode, "rollout", "r", false, `Rollout tracking mode.
In the rollout mode track resource until it is ready or done.
This is default tracking mode.
Options --follow and --rollout cannot be used at the same time.`)

	trackCmd.AddCommand(&cobra.Command{
		Use:   "job NAME",
		Short: "Track Job",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			setTrackMode()

			var err error
			name := args[0]

			if rolloutMode {
				err = rollout.TrackJob(name, namespace, kube.Kubernetes, makeTrackerOptions())
			} else {
				err = follow.TrackJob(name, namespace, kube.Kubernetes, makeTrackerOptions())
			}

			if err != nil {
				fmt.Fprintf(os.Stderr, "Error tracking Job `%s` in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	})

	trackCmd.AddCommand(&cobra.Command{
		Use:   "deployment NAME",
		Short: "Track Deployment in the follow mode",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			setTrackMode()

			var err error
			name := args[0]

			if rolloutMode {
				err = rollout.TrackDeployment(name, namespace, kube.Kubernetes, makeTrackerOptions())
			} else {
				err = follow.TrackDeployment(name, namespace, kube.Kubernetes, makeTrackerOptions())
			}

			if err != nil {
				fmt.Fprintf(os.Stderr, "Error following Deployment `%s` in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	})

	trackCmd.AddCommand(&cobra.Command{
		Use:   "pod NAME",
		Short: "Track Pod in the follow mode",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			setTrackMode()

			var err error
			name := args[0]

			if rolloutMode {
				err = rollout.TrackPod(name, namespace, kube.Kubernetes, makeTrackerOptions())
			} else {
				err = follow.TrackPod(name, namespace, kube.Kubernetes, makeTrackerOptions())
			}

			if err != nil {
				fmt.Fprintf(os.Stderr, "Error following Pod `%s` in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	})

	err = rootCmd.Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}
