package kube

import (
	"fmt"
	"io/ioutil"
	"os"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	kubeTokenFilePath     = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	kubeNamespaceFilePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

var (
	Kubernetes       kubernetes.Interface
	DefaultNamespace string
)

type InitOptions struct {
	KubeContext string
	KubeConfig  string
}

func Init(opts InitOptions) error {
	var err error
	var config *rest.Config

	if isRunningOutOfKubeCluster() {
		clientConfig := getConfig(opts.KubeContext, opts.KubeConfig)

		ns, _, err := clientConfig.Namespace()
		if err != nil {
			return fmt.Errorf("cannot determine default kubernetes namespace: %s", err)
		}
		DefaultNamespace = ns

		config, err = clientConfig.ClientConfig()
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

		data, err := ioutil.ReadFile(kubeNamespaceFilePath)
		if err != nil {
			return fmt.Errorf("in-cluster cofiguration problem: error reading %s: %s", kubeNamespaceFilePath, err)
		}
		DefaultNamespace = string(data)
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
