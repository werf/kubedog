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
	err := kube.Init(kube.InitOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to initialize kube: %s\n", err)
		os.Exit(1)
	}

	var namespace string
	var timeoutSeconds int
	var logsSince string

	makeTrackerOptions := func(mode string) tracker.Options {
		// rollout track defaults
		var timeout uint64
		if timeoutSeconds == -1 {
			if mode == "follow" {
				timeout = 0
			}
			if mode == "track" {
				timeout = 300
			}
		} else {
			timeout = uint64(timeoutSeconds)
		}

		logsFromTime := time.Now()
		if logsSince != "now" {
			if logsSince == "all" {
				logsFromTime = time.Time{}
			} else {
				since, err := time.ParseDuration(logsSince)
				if err == nil {
					logsFromTime = time.Now().Add(-since)
				}
			}
		}

		opts := tracker.Options{
			Timeout:      time.Second * time.Duration(timeout),
			LogsFromTime: logsFromTime,
		}

		return opts
	}

	rootCmd := &cobra.Command{Use: "kubedog"}
	rootCmd.PersistentFlags().StringVarP(&namespace, "namespace", "n", "default", "kubernetes namespace")
	rootCmd.PersistentFlags().IntVarP(&timeoutSeconds, "timeout", "t", -1, "watch timeout in seconds") // default is 0 for follow
	rootCmd.PersistentFlags().StringVarP(&logsSince, "logs-since", "", "now", "logs newer than a relative duration like 30s, 5m, or 2h")

	followCmd := &cobra.Command{Use: "follow"}
	rootCmd.AddCommand(followCmd)

	followCmd.AddCommand(&cobra.Command{
		Use:   "job NAME",
		Short: "Follow Job",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			err := follow.TrackJob(name, namespace, kube.Kubernetes, makeTrackerOptions("follow"))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error following Job `%s` in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	})
	followCmd.AddCommand(&cobra.Command{
		Use:   "deployment NAME",
		Short: "Follow Deployment",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			err := follow.TrackDeployment(name, namespace, kube.Kubernetes, makeTrackerOptions("follow"))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error following Deployment `%s` in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	})
	followCmd.AddCommand(&cobra.Command{
		Use:   "statefulset NAME",
		Short: "Follow StatefulSet",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			err := follow.TrackStatefulSet(name, namespace, kube.Kubernetes, makeTrackerOptions("follow"))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error following Statefulset `%s` in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	})
	followCmd.AddCommand(&cobra.Command{
		Use:   "daemonset NAME",
		Short: "Follow DaemonSet",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			err := follow.TrackDaemonSet(name, namespace, kube.Kubernetes, makeTrackerOptions("follow"))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error following DaemonSet `%s` in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	})
	followCmd.AddCommand(&cobra.Command{
		Use:   "pod NAME",
		Short: "Follow Pod",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			err := follow.TrackPod(name, namespace, kube.Kubernetes, makeTrackerOptions("follow"))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error following Pod `%s` in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	})

	rolloutCmd := &cobra.Command{Use: "rollout"}
	rootCmd.AddCommand(rolloutCmd)
	trackCmd := &cobra.Command{Use: "track"}
	rolloutCmd.AddCommand(trackCmd)

	trackCmd.AddCommand(&cobra.Command{
		Use:   "job NAME",
		Short: "Track Job till job is done",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			err := rollout.TrackJobTillDone(name, namespace, kube.Kubernetes, makeTrackerOptions("track"))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error tracking Job `%s` in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	})

	trackCmd.AddCommand(&cobra.Command{
		Use:   "deployment NAME",
		Short: "Track Deployment till ready",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			err := rollout.TrackDeploymentTillReady(name, namespace, kube.Kubernetes, makeTrackerOptions("track"))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error tracking Deployment `%s` in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	})

	trackCmd.AddCommand(&cobra.Command{
		Use:   "statefulset NAME",
		Short: "Track Statefulset till ready",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			err := rollout.TrackStatefulSetTillReady(name, namespace, kube.Kubernetes, makeTrackerOptions("track"))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error tracking StatefulSet `%s` in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	})

	trackCmd.AddCommand(&cobra.Command{
		Use:   "daemonset NAME",
		Short: "Track DaemonSet till ready",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			err := rollout.TrackDaemonSetTillReady(name, namespace, kube.Kubernetes, makeTrackerOptions("track"))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error tracking DaemonSet `%s` in namespace `%s`: %s\n", name, namespace, err)
				os.Exit(1)
			}
		},
	})

	trackCmd.AddCommand(&cobra.Command{
		Use:   "pod NAME",
		Short: "Track Pod till ready",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			err := rollout.TrackPodTillReady(name, namespace, kube.Kubernetes, makeTrackerOptions("track"))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error tracking Pod `%s` in namespace `%s`: %s\n", name, namespace, err)
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
