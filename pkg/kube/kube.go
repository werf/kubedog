package kube

import (
	"fmt"
	"os"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	kubeTokenFilePath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
)

var (
	Kubernetes kubernetes.Interface
)

type InitOptions struct {
	KubeContext string
	KubeConfig  string
}

func Init(opts InitOptions) error {
	var err error
	var config *rest.Config

	if isRunningOutOfKubeCluster() {
		config, err = getConfig(opts.KubeContext, opts.KubeConfig).ClientConfig()
		if err != nil {
			baseErrMsg := fmt.Sprintf("out-of-cluster configuration problem")
			if opts.KubeConfig != "" {
				baseErrMsg += ", custom kube config path is %q"
			}
			if opts.KubeContext != "" {
				baseErrMsg += ", custom kube context is %q"
			}
			return fmt.Errorf("%s: %s", baseErrMsg, err)
		}
	} else {
		config, err = rest.InClusterConfig()
		if err != nil {
			return fmt.Errorf("in-cluster configuration problem: %s", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	Kubernetes = clientset

	return nil
}

func getConfig(context string, kubeconfig string) clientcmd.ClientConfig {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.DefaultClientConfig = &clientcmd.DefaultClientConfig

	overrides := &clientcmd.ConfigOverrides{ClusterDefaults: clientcmd.ClusterDefaults}

	if context != "" {
		overrides.CurrentContext = context
	}

	if kubeconfig != "" {
		rules.ExplicitPath = kubeconfig
	}

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
}

func isRunningOutOfKubeCluster() bool {
	_, err := os.Stat(kubeTokenFilePath)
	return os.IsNotExist(err)
}
