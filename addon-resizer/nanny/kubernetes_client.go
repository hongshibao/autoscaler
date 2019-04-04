/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package nanny

import (
	"fmt"
	"time"

	apps "k8s.io/api/apps/v1"
	api "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	kube_client "k8s.io/client-go/kubernetes"
	kube_client_apps "k8s.io/client-go/kubernetes/typed/apps/v1"
	v1appslister "k8s.io/client-go/listers/apps/v1"
	v1lister "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

type kubernetesClient struct {
	nodeLister       v1lister.NodeLister
	podLister        v1lister.PodNamespaceLister
	deploymentLister v1appslister.DeploymentNamespaceLister
	deploymentClient kube_client_apps.DeploymentInterface
	namespace        string
	deployment       string
	pod              string
	container        string
	stopChannel      chan struct{}
}

// NewKubernetesClient gives a KubernetesClient with the given dependencies.
func NewKubernetesClient(kubeClient kube_client.Interface, namespace, deployment, pod, container string) KubernetesClient {
	stopChannel := make(chan struct{})
	result := &kubernetesClient{
		stopChannel:      stopChannel,
		namespace:        namespace,
		deployment:       deployment,
		pod:              pod,
		container:        container,
		nodeLister:       newReadyNodeLister(kubeClient, stopChannel),
		podLister:        newPodListerByNamespace(kubeClient, stopChannel, namespace),
		deploymentLister: newDeploymentListerByNamespace(kubeClient, stopChannel, namespace),
		deploymentClient: kubeClient.AppsV1().Deployments(namespace),
	}
	return result
}

func (k *kubernetesClient) Stop() {
	for i := 0; i < 3; i++ {
		k.stopChannel <- struct{}{}
	}
}

func (k *kubernetesClient) CountNodes() (uint64, error) {
	nodes, err := k.nodeLister.List(labels.Everything())
	return uint64(len(nodes)), err
}

func (k *kubernetesClient) ContainerResources() (*api.ResourceRequirements, error) {
	pod, err := k.podLister.Get(k.pod)

	if err != nil {
		return nil, err
	}
	for _, container := range pod.Spec.Containers {
		if container.Name == k.container {
			return &container.Resources, nil
		}
	}
	return nil, fmt.Errorf("container %s was not found in deployment %s in namespace %s", k.container, k.deployment, k.namespace)
}

func (k *kubernetesClient) UpdateDeployment(resources *api.ResourceRequirements) error {
	// First, get the Deployment.
	dep, err := k.deploymentLister.Get(k.deployment)
	if err != nil {
		return err
	}

	dep = dep.DeepCopy()
	// Modify the Deployment object with our ResourceRequirements.
	for i, container := range dep.Spec.Template.Spec.Containers {
		if container.Name == k.container {
			// Update the deployment.
			dep.Spec.Template.Spec.Containers[i].Resources = *resources
			_, err := k.deploymentClient.Update(dep)
			return err
		}
	}

	return fmt.Errorf("container %s was not found in the deployment %s in namespace %s", k.container, k.deployment, k.namespace)
}

func newReadyNodeLister(kubeClient kube_client.Interface, stopChannel <-chan struct{}) v1lister.NodeLister {
	listWatcher := cache.NewListWatchFromClient(kubeClient.CoreV1().RESTClient(), "nodes", api.NamespaceAll, fields.Everything())
	store := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	nodeLister := v1lister.NewNodeLister(store)
	reflector := cache.NewReflector(listWatcher, &api.Node{}, store, time.Hour)
	go reflector.Run(stopChannel)
	return nodeLister
}

func newPodListerByNamespace(kubeClient kube_client.Interface, stopChannel <-chan struct{},
	namespace string) v1lister.PodNamespaceLister {
	listWatcher := cache.NewListWatchFromClient(kubeClient.CoreV1().RESTClient(), "pods", namespace, fields.Everything())
	store := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	lister := v1lister.NewPodLister(store)
	reflector := cache.NewReflector(listWatcher, &api.Pod{}, store, time.Hour)
	go reflector.Run(stopChannel)
	nsLister := lister.Pods(namespace)
	return nsLister
}

func newDeploymentListerByNamespace(kubeClient kube_client.Interface, stopChannel <-chan struct{},
	namespace string) v1appslister.DeploymentNamespaceLister {
	listWatcher := cache.NewListWatchFromClient(kubeClient.AppsV1().RESTClient(), "deployments", namespace, fields.Everything())
	store := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	lister := v1appslister.NewDeploymentLister(store)
	reflector := cache.NewReflector(listWatcher, &apps.Deployment{}, store, time.Hour)
	go reflector.Run(stopChannel)
	nsLister := lister.Deployments(namespace)
	return nsLister
}
