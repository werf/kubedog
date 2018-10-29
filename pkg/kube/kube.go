package kube

import (
	"fmt"
	"os"
	"path/filepath"

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

func Init() error {
	var err error
	var config *rest.Config

	if isRunningOutOfKubeCluster() {
		var kubeconfig string
		if kubeconfig = os.Getenv("KUBECONFIG"); kubeconfig == "" {
			kubeconfig = filepath.Join(os.Getenv("HOME"), ".kube", "config")
		}

		// use the current context in kubeconfig
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return fmt.Errorf("out-of-cluster configuration problem: %s", err)
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

func isRunningOutOfKubeCluster() bool {
	_, err := os.Stat(kubeTokenFilePath)
	return os.IsNotExist(err)
}
