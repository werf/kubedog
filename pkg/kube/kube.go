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
		clientConfig := getClientConfig(opts.KubeContext, opts.KubeConfig)

		ns, _, err := clientConfig.Namespace()
		if err != nil {
			return fmt.Errorf("cannot determine default kubernetes namespace: %s", err)
		}
		DefaultNamespace = ns

		config, err = clientConfig.ClientConfig()
		if err != nil {
			return makeOutOfClusterClientConfigError(opts.KubeConfig, opts.KubeContext, err)
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

type GetClientsOptions struct {
	KubeConfig string
}

func GetAllClients(opts GetClientsOptions) ([]kubernetes.Interface, error) {
	var res []kubernetes.Interface

	if isRunningOutOfKubeCluster() {
		rc, err := getClientConfig("", opts.KubeConfig).RawConfig()
		if err != nil {
			return nil, err
		}

		for contextName := range rc.Contexts {
			clientConfig := getClientConfig(contextName, opts.KubeConfig)

			config, err := clientConfig.ClientConfig()
			if err != nil {
				return nil, makeOutOfClusterClientConfigError(opts.KubeConfig, contextName, err)
			}

			clientset, err := kubernetes.NewForConfig(config)
			if err != nil {
				return nil, err
			}

			res = append(res, clientset)
		}
	} else {
		config, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("in-cluster configuration problem: %s", err)
		}

		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			return nil, err
		}

		res = append(res, clientset)
	}

	return res, nil
}

func makeOutOfClusterClientConfigError(kubeConfig, kubeContext string, err error) error {
	baseErrMsg := fmt.Sprintf("out-of-cluster configuration problem")

	if kubeConfig != "" {
		baseErrMsg += fmt.Sprintf(", custom kube config path is %q", kubeConfig)
	}

	if kubeContext != "" {
		baseErrMsg += fmt.Sprintf(", custom kube context is %q", kubeContext)
	}

	return fmt.Errorf("%s: %s", baseErrMsg, err)
}

func getClientConfig(context string, kubeconfig string) clientcmd.ClientConfig {
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
