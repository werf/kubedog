package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"k8s.io/klog"
	klog_v2 "k8s.io/klog/v2"

	"github.com/spf13/cobra"
	"github.com/werf/logboek"

	"github.com/werf/kubedog"
	"github.com/werf/kubedog/pkg/kube"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/trackers/follow"
	"github.com/werf/kubedog/pkg/trackers/rollout"
	"github.com/werf/kubedog/pkg/trackers/rollout/multitrack"
)

func main() {
	// set flag.Parsed() for glog
	flag.CommandLine.Parse([]string{})

	var namespace string
	var timeoutSeconds int
	var statusProgressPeriodSeconds int64
	var logsSince string
	var kubeContext string
	var kubeConfig string
	var kubeConfigBase64 string
	var kubeConfigPathMergeList []string
	var outputPrefix string

	makeTrackerOptions := func(mode string) tracker.Options {
		// rollout track defaults
		var timeout uint64
		if timeoutSeconds == -1 {
			// wait forever by default in follow and rollout track modes
			timeout = 0
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

	init := func() {
		if err := SilenceKlog(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "Unable to initialize klog: %s\n", err)
			os.Exit(1)
		}

		if err := SilenceKlogV2(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "Unable to initialize klog_v2: %s\n", err)
			os.Exit(1)
		}

		err := kube.Init(kube.InitOptions{kube.KubeConfigOptions{Context: kubeContext, ConfigPath: kubeConfig, ConfigDataBase64: kubeConfigBase64, ConfigPathMergeList: kubeConfigPathMergeList}})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Unable to initialize kube: %s\n", err)
			os.Exit(1)
		}

		logboek.Context(context.Background()).Streams().EnableStyle()
		if noColorVal := os.Getenv("KUBEDOG_NO_COLOR"); noColorVal != "" {
			noColor := false
			for _, val := range []string{"1", "on", "true"} {
				if noColorVal == val {
					noColor = true
					break
				}
			}

			if noColor {
				logboek.Context(context.Background()).Streams().DisableStyle()
			}
		}

		if terminalWidthStr := os.Getenv("KUBEDOG_TERMINAL_WIDTH"); terminalWidthStr != "" {
			terminalWidth, err := strconv.Atoi(terminalWidthStr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Bad value specified for KUBEDOG_TERMINAL_WIDTH, integer expected: %s\n", err)
				os.Exit(1)
			}

			logboek.Context(context.Background()).Streams().SetWidth(terminalWidth)
		}
	}

	rootCmd := &cobra.Command{Use: "kubedog"}
	rootCmd.PersistentFlags().StringVarP(&namespace, "namespace", "n", "default", "If present, the namespace scope of a resource.")
	rootCmd.PersistentFlags().IntVarP(&timeoutSeconds, "timeout", "t", -1, "Timeout of operation in seconds. 0 is wait forever. Default is 0.")
	rootCmd.PersistentFlags().StringVarP(&logsSince, "logs-since", "", "now", "A duration like 30s, 5m, or 2h to start log records from the past. 'all' to show all logs and 'now' to display only new records (default).")
	rootCmd.PersistentFlags().StringVarP(&kubeContext, "kube-context", "", os.Getenv("KUBEDOG_KUBE_CONTEXT"), "The name of the kubeconfig context to use (can be set with $KUBEDOG_KUBE_CONTEXT).")
	rootCmd.PersistentFlags().StringVarP(&kubeConfig, "kube-config", "", "", "Path to the kubeconfig file (can be set with $KUBEDOG_KUBE_CONFIG or $KUBECONFIG).")
	rootCmd.PersistentFlags().StringVarP(&kubeConfigBase64, "kube-config-base64", "", os.Getenv("KUBEDOG_KUBE_CONFIG_BASE64"), "Content of the kube config, base64-encoded (can be set with $KUBEDOG_KUBE_CONFIG_BASE64).")

	for _, envVar := range []string{"KUBEDOG_KUBE_CONFIG", "KUBECONFIG"} {
		if v := os.Getenv(envVar); v != "" {
			for _, path := range filepath.SplitList(v) {
				kubeConfigPathMergeList = append(kubeConfigPathMergeList, path)
			}

			break
		}
	}

	rootCmd.PersistentFlags().StringVarP(&outputPrefix, "output-prefix", "", "", "Arbitrary string which will be prefixed to kubedog output.")

	versionCmd := &cobra.Command{
		Use: "version",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println(kubedog.Version)
		},
	}
	rootCmd.AddCommand(versionCmd)

	multitrackCmd := &cobra.Command{
		Use:     "multitrack",
		Short:   "Track multiple resources using multitrack tracker",
		Example: `echo '{"Deployments":[{"ResourceName":"mydeploy","Namespace":"myns"},{"ResourceName":"myresource","Namespace":"myns","FailMode":"HopeUntilEndOfDeployProcess","AllowFailuresCount":3,"SkipLogsForContainers":["two", "three"]}], "StatefulSets":[{"ResourceName":"mysts","Namespace":"myns"}]}' | kubedog multitrack`,
		Run: func(cmd *cobra.Command, args []string) {
			init()

			if outputPrefix != "" {
				logboek.Context(context.Background()).Streams().SetPrefix(outputPrefix)
			}

			specsInput, err := ioutil.ReadAll(os.Stdin)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading stdin: %s\n", err)
				os.Exit(1)
			}

			specs := multitrack.MultitrackSpecs{}
			err = json.Unmarshal(specsInput, &specs)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error parsing MultitrackSpecs json: %s\n", err)
				os.Exit(1)
			}

			multitrackOptions := multitrack.MultitrackOptions{
				StatusProgressPeriod: time.Second * time.Duration(statusProgressPeriodSeconds),
				Options:              makeTrackerOptions("track"),
			}
			err = multitrack.Multitrack(kube.Kubernetes, specs, multitrackOptions)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		},
	}
	multitrackCmd.PersistentFlags().Int64VarP(&statusProgressPeriodSeconds, "status-progress-period", "", 5, "Status progress period in seconds. Set -1 to stop showing status progress.")

	rootCmd.AddCommand(multitrackCmd)

	followCmd := &cobra.Command{Use: "follow"}
	rootCmd.AddCommand(followCmd)

	followCmd.AddCommand(&cobra.Command{
		Use:   "job NAME",
		Short: "Follow Job",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			init()
			err := follow.TrackJob(name, namespace, kube.Kubernetes, makeTrackerOptions("follow"))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
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
			init()
			err := follow.TrackDeployment(name, namespace, kube.Kubernetes, makeTrackerOptions("follow"))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
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
			init()
			err := follow.TrackStatefulSet(name, namespace, kube.Kubernetes, makeTrackerOptions("follow"))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
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
			init()
			err := follow.TrackDaemonSet(name, namespace, kube.Kubernetes, makeTrackerOptions("follow"))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
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
			init()
			err := follow.TrackPod(name, namespace, kube.Kubernetes, makeTrackerOptions("follow"))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
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
			init()
			err := rollout.TrackJobTillDone(name, namespace, kube.Kubernetes, makeTrackerOptions("track"))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
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
			init()
			err := rollout.TrackDeploymentTillReady(name, namespace, kube.Kubernetes, makeTrackerOptions("track"))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
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
			init()
			err := rollout.TrackStatefulSetTillReady(name, namespace, kube.Kubernetes, makeTrackerOptions("track"))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
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
			init()
			err := rollout.TrackDaemonSetTillReady(name, namespace, kube.Kubernetes, makeTrackerOptions("track"))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
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
			init()
			err := rollout.TrackPodTillReady(name, namespace, kube.Kubernetes, makeTrackerOptions("track"))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		},
	})

	err := rootCmd.Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func SilenceKlogV2(ctx context.Context) error {
	fs := flag.NewFlagSet("klog", flag.PanicOnError)
	klog_v2.InitFlags(fs)

	if err := fs.Set("logtostderr", "false"); err != nil {
		return err
	}
	if err := fs.Set("alsologtostderr", "false"); err != nil {
		return err
	}
	if err := fs.Set("stderrthreshold", "5"); err != nil {
		return err
	}

	// Suppress info and warnings from client-go reflector
	klog_v2.SetOutputBySeverity("INFO", ioutil.Discard)
	klog_v2.SetOutputBySeverity("WARNING", ioutil.Discard)
	klog_v2.SetOutputBySeverity("ERROR", ioutil.Discard)
	klog_v2.SetOutputBySeverity("FATAL", logboek.Context(ctx).ErrStream())

	return nil
}

func SilenceKlog(ctx context.Context) error {
	fs := flag.NewFlagSet("klog", flag.PanicOnError)
	klog.InitFlags(fs)

	if err := fs.Set("logtostderr", "false"); err != nil {
		return err
	}
	if err := fs.Set("alsologtostderr", "false"); err != nil {
		return err
	}
	if err := fs.Set("stderrthreshold", "5"); err != nil {
		return err
	}

	// Suppress info and warnings from client-go reflector
	klog.SetOutputBySeverity("INFO", ioutil.Discard)
	klog.SetOutputBySeverity("WARNING", ioutil.Discard)
	klog.SetOutputBySeverity("ERROR", ioutil.Discard)
	klog.SetOutputBySeverity("FATAL", logboek.Context(ctx).ErrStream())

	return nil
}
