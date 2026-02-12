package kube

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	diskcached "k8s.io/client-go/discovery/cached/disk"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/azure"
	_ "k8s.io/client-go/plugin/pkg/client/auth/exec"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/werf/kubedog/pkg/utils"
)

const (
	kubeTokenFilePath     = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	kubeNamespaceFilePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

var (
	Kubernetes            kubernetes.Interface
	Client                kubernetes.Interface
	DynamicClient         dynamic.Interface
	CachedDiscoveryClient discovery.CachedDiscoveryInterface
	Mapper                meta.RESTMapper
	DefaultNamespace      string
	Context               string
)

type InitOptions struct {
	KubeConfigOptions
}

func Init(opts InitOptions) error {
	config, err := GetKubeConfig(opts.KubeConfigOptions)
	if err != nil {
		return err
	}

	if config != nil {
		clientset, err := kubernetes.NewForConfig(config.Config)
		if err != nil {
			return err
		}
		Kubernetes = clientset
		Client = clientset

		dynamicClient, err := dynamic.NewForConfig(config.Config)
		if err != nil {
			return err
		}
		DynamicClient = dynamicClient

		CachedDiscoveryClient, err = cachedDiscoveryClient(*config.Config)
		if err != nil {
			return fmt.Errorf("error getting cached discovery client: %w", err)
		}

		Mapper = restMapper(&CachedDiscoveryClient)
	}

	return nil
}

type KubeConfigOptions struct {
	Context             string
	ConfigPath          string
	ConfigDataBase64    string
	ConfigPathMergeList []string

	BearerToken     string
	BearerTokenFile string

	APIServerURL string
	Insecure     bool
	CADataBase64 string
}

type KubeConfig struct {
	Config           *rest.Config
	Context          string
	DefaultNamespace string
}

func GetKubeConfig(opts KubeConfigOptions) (*KubeConfig, error) {
	// Try to load from kubeconfig in flags or from ~/.kube/config
	config, outOfClusterErr := getOutOfClusterConfig(
		opts,
	)
	if config == nil {
		if hasInClusterConfig() {
			// Try to configure as inCluster
			if config, err := getInClusterConfig(); err != nil {
				if opts.ConfigPath != "" || opts.Context != "" || opts.ConfigDataBase64 != "" {
					if outOfClusterErr != nil {
						return nil, fmt.Errorf("out-of-cluster config error: %w, in-cluster config error: %w", outOfClusterErr, err)
					}
				} else {
					return nil, err
				}
			} else if config != nil {
				return config, nil
			}
		} else {
			// if not in cluster return outOfCluster error
			if outOfClusterErr != nil {
				return nil, outOfClusterErr
			}
		}

		return nil, nil
	}

	return config, outOfClusterErr
}

type GetAllContextsClientsOptions struct {
	ConfigPath          string
	ConfigDataBase64    string
	ConfigPathMergeList []string
	BearerToken         string
	BearerTokenFile     string

	APIServerURL string
	Insecure     bool
	CADataBase64 string
}

type ContextClient struct {
	ContextName      string
	ContextNamespace string
	Client           kubernetes.Interface
}

func GetAllContextsClients(opts GetAllContextsClientsOptions) ([]*ContextClient, error) {
	// Try to load contexts from kubeconfig in flags or from ~/.kube/config
	var outOfClusterErr error

	contexts, outOfClusterErr := getOutOfClusterContextsClients(KubeConfigOptions{
		ConfigPath:          opts.ConfigPath,
		ConfigDataBase64:    opts.ConfigDataBase64,
		ConfigPathMergeList: opts.ConfigPathMergeList,
	})
	if len(contexts) > 0 {
		return contexts, nil
	}

	if hasInClusterConfig() {
		contextClient, err := getInClusterContextClient()
		if err != nil {
			return nil, err
		}
		return []*ContextClient{contextClient}, nil
	}

	tokenClient, err := getTokenContextClient(KubeConfigOptions{
		ConfigPath:          opts.ConfigPath,
		ConfigDataBase64:    opts.ConfigDataBase64,
		ConfigPathMergeList: opts.ConfigPathMergeList,
		BearerToken:         opts.BearerToken,
		BearerTokenFile:     opts.BearerTokenFile,
		APIServerURL:        opts.APIServerURL,
		Insecure:            opts.Insecure,
		CADataBase64:        opts.CADataBase64,
	})
	if err != nil {
		return nil, err
	}
	if tokenClient != nil {
		return []*ContextClient{tokenClient}, nil
	}

	if outOfClusterErr != nil {
		return nil, outOfClusterErr
	}

	return nil, nil
}

func makeOutOfClusterClientConfigError(configPath, context string, err error) error {
	baseErrMsg := "out-of-cluster configuration problem"

	if configPath != "" {
		baseErrMsg += fmt.Sprintf(", custom kube config path is %q", configPath)
	}

	if context != "" {
		baseErrMsg += fmt.Sprintf(", custom kube context is %q", context)
	}

	return fmt.Errorf("%s: %w", baseErrMsg, err)
}

func setConfigPathMergeListEnvironment(configPathMergeList []string) error {
	configPathEnvVar := strings.Join(configPathMergeList, string(filepath.ListSeparator))
	if err := os.Setenv(clientcmd.RecommendedConfigPathEnvVar, configPathEnvVar); err != nil {
		return fmt.Errorf("unable to set env var %q: %w", clientcmd.RecommendedConfigPathEnvVar, err)
	}
	return nil
}

func GetClientConfig(context, configPath string, configData []byte, configPathMergeList []string, overrides *clientcmd.ConfigOverrides) (clientcmd.ClientConfig, error) {
	if context != "" {
		overrides.CurrentContext = context
	}

	if configData != nil {
		config, err := clientcmd.Load(configData)
		if err != nil {
			return nil, fmt.Errorf("unable to load config data: %w", err)
		}

		return clientcmd.NewDefaultClientConfig(*config, overrides), nil
	}

	if len(configPathMergeList) > 0 {
		if err := setConfigPathMergeListEnvironment(configPathMergeList); err != nil {
			return nil, err
		}
	}

	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.DefaultClientConfig = &clientcmd.DefaultClientConfig
	if configPath != "" {
		rules.ExplicitPath = configPath
	}

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides), nil
}

func hasInClusterConfig() bool {
	token, _ := utils.FileExists(kubeTokenFilePath)
	ns, _ := utils.FileExists(kubeNamespaceFilePath)
	return token && ns
}

func parseConfigDataBase64(configDataBase64 string) ([]byte, error) {
	var configData []byte

	if configDataBase64 != "" {
		if data, err := base64.StdEncoding.DecodeString(configDataBase64); err != nil {
			return nil, fmt.Errorf("unable to decode base64 config data: %w", err)
		} else {
			configData = data
		}
	}

	return configData, nil
}

func getOutOfClusterConfig(opts KubeConfigOptions) (*KubeConfig, error) {
	res := &KubeConfig{}

	configData, err := parseConfigDataBase64(opts.ConfigDataBase64)
	if err != nil {
		return nil, fmt.Errorf("unable to parse base64 config data: %w", err)
	}

	overrides := &clientcmd.ConfigOverrides{
		ClusterDefaults: clientcmd.ClusterDefaults,
		AuthInfo: api.AuthInfo{
			Token:     opts.BearerToken,
			TokenFile: opts.BearerTokenFile,
		},
	}

	if opts.Context != "" {
		overrides.CurrentContext = opts.Context
	}

	clientConfig, err := GetClientConfig(
		opts.Context,
		opts.ConfigPath,
		configData,
		opts.ConfigPathMergeList,
		overrides,
	)
	if err != nil {
		return nil, makeOutOfClusterClientConfigError(opts.ConfigDataBase64, opts.Context, err)
	}

	if ns, _, err := clientConfig.Namespace(); err != nil {
		return nil, fmt.Errorf("cannot determine default kubernetes namespace: %w", err)
	} else {
		res.DefaultNamespace = ns
	}

	config, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, makeOutOfClusterClientConfigError(opts.ConfigDataBase64, opts.Context, err)
	}
	if config == nil {
		return nil, nil
	}

	res.Config = config

	if opts.Context == "" {
		if rc, err := clientConfig.RawConfig(); err != nil {
			return nil, fmt.Errorf("cannot get raw kubernetes config: %w", err)
		} else {
			res.Context = rc.CurrentContext
		}
	} else {
		res.Context = opts.Context
	}

	return res, nil
}

func getOutOfClusterContextsClients(opts KubeConfigOptions) ([]*ContextClient, error) {
	var res []*ContextClient

	configData, err := parseConfigDataBase64(opts.ConfigDataBase64)
	if err != nil {
		return nil, fmt.Errorf("unable to parse base64 config data: %w", err)
	}

	overrides := &clientcmd.ConfigOverrides{
		ClusterDefaults: clientcmd.ClusterDefaults,
		AuthInfo: api.AuthInfo{
			Token:     opts.BearerToken,
			TokenFile: opts.BearerTokenFile,
		},
	}

	clientConfig, err := GetClientConfig(
		"",
		opts.ConfigPath,
		configData,
		opts.ConfigPathMergeList,
		overrides,
	)
	if err != nil {
		return nil, err
	}

	rc, err := clientConfig.RawConfig()
	if err != nil {
		return nil, err
	}

	for contextName, context := range rc.Contexts {
		clientConfig, err := GetClientConfig(
			contextName,
			opts.ConfigPath,
			configData,
			opts.ConfigPathMergeList,
			overrides,
		)
		if err != nil {
			return nil, makeOutOfClusterClientConfigError(opts.ConfigPath, contextName, err)
		}

		config, err := clientConfig.ClientConfig()
		if err != nil {
			return nil, makeOutOfClusterClientConfigError(opts.ConfigPath, contextName, err)
		}

		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			return nil, err
		}

		res = append(res, &ContextClient{
			ContextName:      contextName,
			ContextNamespace: context.Namespace,
			Client:           clientset,
		})
	}

	return res, nil
}

func getInClusterConfig() (*KubeConfig, error) {
	res := &KubeConfig{}

	if config, err := rest.InClusterConfig(); err != nil {
		return nil, fmt.Errorf("in-cluster configuration problem: %w", err)
	} else {
		res.Config = config
	}

	if data, err := os.ReadFile(kubeNamespaceFilePath); err != nil {
		return nil, fmt.Errorf("in-cluster configuration problem: cannot determine default kubernetes namespace: error reading %s: %w", kubeNamespaceFilePath, err)
	} else {
		res.DefaultNamespace = string(data)
	}

	return res, nil
}

func getInClusterContextClient() (*ContextClient, error) {
	kubeConfig, err := getInClusterConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(kubeConfig.Config)
	if err != nil {
		return nil, err
	}

	return &ContextClient{
		ContextName:      "inClusterContext",
		ContextNamespace: kubeConfig.DefaultNamespace,
		Client:           clientset,
	}, nil
}

func GroupVersionResourceByKind(client kubernetes.Interface, kind string) (schema.GroupVersionResource, error) {
	lists, err := client.Discovery().ServerPreferredResources()
	if err != nil {
		return schema.GroupVersionResource{}, err
	}

	for _, list := range lists {
		if len(list.APIResources) == 0 {
			continue
		}

		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			continue
		}

		for _, resource := range list.APIResources {
			if len(resource.Verbs) == 0 {
				continue
			}

			if kind == resource.Kind {
				groupVersionResource := schema.GroupVersionResource{
					Resource: resource.Name,
					Group:    gv.Group,
					Version:  gv.Version,
				}

				return groupVersionResource, nil
			}
		}
	}

	return schema.GroupVersionResource{}, fmt.Errorf("kind %s is not supported", kind)
}

func cachedDiscoveryClient(config rest.Config) (discovery.CachedDiscoveryInterface, error) {
	config.Burst = 100

	cacheDir := defaultCacheDir
	httpCacheDir := filepath.Join(cacheDir, "http")
	discoveryCacheDir := computeDiscoverCacheDir(filepath.Join(cacheDir, "discovery"), config.Host)

	return diskcached.NewCachedDiscoveryClientForConfig(&config, discoveryCacheDir, httpCacheDir, time.Duration(10*time.Minute))
}

func restMapper(cachedDiscoveryClient *discovery.CachedDiscoveryInterface) meta.RESTMapper {
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(*cachedDiscoveryClient)

	return restmapper.NewShortcutExpander(mapper, *cachedDiscoveryClient, func(s string) {
		fmt.Printf(s)
	})
}

func getTokenContextClient(opts KubeConfigOptions) (*ContextClient, error) {
	if opts.BearerToken == "" {
		return nil, fmt.Errorf("missing bearer token")
	}
	if opts.APIServerURL == "" {
		return nil, fmt.Errorf("missing API server URL")
	}

	var caData []byte
	var err error

	if opts.CADataBase64 != "" {
		caData, err = base64.StdEncoding.DecodeString(opts.CADataBase64)
		if err != nil {
			return nil, fmt.Errorf("invalid CADataBase64: %w", err)
		}
	}

	cfg := &rest.Config{
		Host:        opts.APIServerURL,
		BearerToken: opts.BearerToken,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: opts.Insecure,
			CAData:   caData,
		},
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("cannot create kubernetes client: %w", err)
	}

	return &ContextClient{
		ContextName:      "token",
		ContextNamespace: "",
		Client:           clientset,
	}, nil
}
