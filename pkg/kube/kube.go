package kube

import (
	"fmt"
	"io/ioutil"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/flant/kubedog/pkg/utils"
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

	// Try to load from kubeconfig in flags or from ~/.kube/config
	config, outOfClusterErr := getOutOfClusterConfig(opts.KubeContext, opts.KubeConfig)

	if config == nil {
		if hasInClusterConfig() {
			// Try to configure as inCluster
			config, err = getInClusterConfig()
			if err != nil {
				if opts.KubeConfig != "" || opts.KubeContext != "" {
					if outOfClusterErr != nil {
						return fmt.Errorf("out-of-cluster config error: %v, in-cluster config error: %v", outOfClusterErr, err)
					}
				} else {
					return err
				}
			}
		} else {
			// if not in cluster return outOfCluster error
			if outOfClusterErr != nil {
				return outOfClusterErr
			}
		}
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
	// Try to load contexts from kubeconfig in flags or from ~/.kube/config
	var outOfClusterErr error
	contexts, outOfClusterErr := getOutOfClusterContexts(opts.KubeConfig)
	// return if contexts are loaded successfully
	if contexts != nil {
		return contexts, nil
	}
	if hasInClusterConfig() {
		context, err := getInClusterContext()
		if err != nil {
			return nil, err
		}
		return []kubernetes.Interface{context}, nil
	}
	// if not in cluster return outOfCluster error
	if outOfClusterErr != nil {
		return nil, outOfClusterErr
	}

	return nil, nil
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

func hasInClusterConfig() bool {
	token, _ := utils.FileExists(kubeTokenFilePath)
	ns, _ := utils.FileExists(kubeNamespaceFilePath)
	return token && ns
}

func getOutOfClusterConfig(contextName string, configPath string) (config *rest.Config, err error) {
	clientConfig := getClientConfig(contextName, configPath)

	ns, _, err := clientConfig.Namespace()
	if err != nil {
		return nil, fmt.Errorf("cannot determine default kubernetes namespace: %s", err)
	}
	DefaultNamespace = ns

	config, err = clientConfig.ClientConfig()
	if err != nil {
		return nil, makeOutOfClusterClientConfigError(configPath, contextName, err)
	}

	return
}

func getOutOfClusterContexts(configPath string) (contexts []kubernetes.Interface, err error) {
	contexts = make([]kubernetes.Interface, 0)

	rc, err := getClientConfig("", configPath).RawConfig()
	if err != nil {
		return nil, err
	}

	for contextName := range rc.Contexts {
		clientConfig := getClientConfig(contextName, configPath)

		config, err := clientConfig.ClientConfig()
		if err != nil {
			return nil, makeOutOfClusterClientConfigError(configPath, contextName, err)
		}

		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			return nil, err
		}

		contexts = append(contexts, clientset)
	}

	return
}

func getInClusterConfig() (config *rest.Config, err error) {
	config, err = rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster configuration problem: %s", err)
	}

	data, err := ioutil.ReadFile(kubeNamespaceFilePath)
	if err != nil {
		return nil, fmt.Errorf("in-cluster configuration problem: cannot determine default kubernetes namespace: error reading %s: %s", kubeNamespaceFilePath, err)
	}
	DefaultNamespace = string(data)

	return
}

func getInClusterContext() (context kubernetes.Interface, err error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster configuration problem: %s", err)
	}

	context, err = kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return
}
