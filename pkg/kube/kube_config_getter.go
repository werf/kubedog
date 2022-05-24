package kube

import (
	"encoding/base64"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	memory "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

type KubeConfigGetterOptions struct {
	KubeConfigOptions

	Namespace        string
	BearerToken      string
	APIServer        string
	CAFile           string
	Impersonate      string
	ImpersonateGroup []string
}

func NewKubeConfigGetter(opts KubeConfigGetterOptions) (genericclioptions.RESTClientGetter, error) {
	var configGetter genericclioptions.RESTClientGetter

	if opts.ConfigDataBase64 != "" {
		if getter, err := NewClientGetterFromConfigData(opts.Context, opts.ConfigDataBase64); err != nil {
			return nil, fmt.Errorf("unable to create kube client getter (context=%q, config-data-base64=%q): %w", opts.Context, opts.ConfigPath, err)
		} else {
			configGetter = getter
		}
	} else {
		configFlags := genericclioptions.NewConfigFlags(true)

		if len(opts.ConfigPathMergeList) > 0 {
			if err := setConfigPathMergeListEnvironment(opts.ConfigPathMergeList); err != nil {
				return nil, err
			}
		}

		configFlags.Context = new(string)
		*configFlags.Context = opts.Context

		configFlags.KubeConfig = new(string)
		*configFlags.KubeConfig = opts.ConfigPath

		if opts.Namespace != "" {
			configFlags.Namespace = new(string)
			*configFlags.Namespace = opts.Namespace
		}

		if opts.BearerToken != "" {
			configFlags.BearerToken = new(string)
			*configFlags.BearerToken = opts.BearerToken
		}

		if opts.APIServer != "" {
			configFlags.APIServer = new(string)
			*configFlags.APIServer = opts.APIServer
		}

		if opts.CAFile != "" {
			configFlags.CAFile = new(string)
			*configFlags.CAFile = opts.CAFile
		}

		if opts.Impersonate != "" {
			configFlags.Impersonate = new(string)
			*configFlags.Impersonate = opts.Impersonate
		}

		if opts.ImpersonateGroup != nil {
			configFlags.ImpersonateGroup = new([]string)
			*configFlags.ImpersonateGroup = append(*configFlags.ImpersonateGroup, opts.ImpersonateGroup...)
		}

		configGetter = configFlags
	}

	return configGetter, nil
}

type ClientGetterFromConfigData struct {
	Context          string
	ConfigDataBase64 string

	ClientConfig clientcmd.ClientConfig
}

func NewClientGetterFromConfigData(context, configDataBase64 string) (*ClientGetterFromConfigData, error) {
	getter := &ClientGetterFromConfigData{Context: context, ConfigDataBase64: configDataBase64}

	if clientConfig, err := getter.getRawKubeConfigLoader(); err != nil {
		return nil, err
	} else {
		getter.ClientConfig = clientConfig
	}

	return getter, nil
}

func (getter *ClientGetterFromConfigData) ToRESTConfig() (*rest.Config, error) {
	return getter.ClientConfig.ClientConfig()
}

func (getter *ClientGetterFromConfigData) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	config, err := getter.ClientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, err
	}

	return memory.NewMemCacheClient(discoveryClient), nil
}

func (getter *ClientGetterFromConfigData) ToRESTMapper() (meta.RESTMapper, error) {
	discoveryClient, err := getter.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient)
	expander := restmapper.NewShortcutExpander(mapper, discoveryClient)
	return expander, nil
}

func (getter *ClientGetterFromConfigData) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return getter.ClientConfig
}

func (getter *ClientGetterFromConfigData) getRawKubeConfigLoader() (clientcmd.ClientConfig, error) {
	if data, err := base64.StdEncoding.DecodeString(getter.ConfigDataBase64); err != nil {
		return nil, fmt.Errorf("unable to decode base64 config data: %w", err)
	} else {
		return GetClientConfig(getter.Context, "", data, nil)
	}
}
