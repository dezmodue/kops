/*
Copyright 2019 The Kubernetes Authors.

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

package watchers

import (
	"context"
	"fmt"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"k8s.io/kops/dns-controller/pkg/dns"
	"k8s.io/kops/dns-controller/pkg/util"
)

// PodController watches for Pods with dns annotations
type PodController struct {
	util.Stoppable
	client    kubernetes.Interface
	namespace string
	scope     dns.Scope
}

// NewPodController creates a podController
func NewPodController(client kubernetes.Interface, dns dns.Context, namespace string) (*PodController, error) {
	scope, err := dns.CreateScope("pod")
	if err != nil {
		return nil, fmt.Errorf("error building dns scope: %v", err)
	}
	c := &PodController{
		client:    client,
		scope:     scope,
		namespace: namespace,
	}

	return c, nil
}

// Run starts the PodController.
func (c *PodController) Run() {
	klog.Infof("starting pod controller")

	stopCh := c.StopChannel()
	go c.runWatcher(stopCh)

	<-stopCh
	klog.Infof("shutting down pod controller")
}

func (c *PodController) runWatcher(stopCh <-chan struct{}) {
	runOnce := func() (bool, error) {
		ctx := context.TODO()

		var listOpts metav1.ListOptions
		klog.V(4).Infof("querying without label filter")

		allKeys := c.scope.AllKeys()

		podList, err := c.client.CoreV1().Pods(c.namespace).List(ctx, listOpts)
		if err != nil {
			return false, fmt.Errorf("error listing pods: %v", err)
		}
		foundKeys := make(map[string]bool)
		for i := range podList.Items {
			pod := &podList.Items[i]
			klog.V(4).Infof("found pod: %v", pod.Name)
			key := c.updatePodRecords(pod)
			foundKeys[key] = true
		}
		for _, key := range allKeys {
			if !foundKeys[key] {
				// The pod previous existed, but no longer exists; delete it from the scope
				klog.V(2).Infof("removing pod not found in list: %s", key)
				c.scope.Replace(key, nil)
			}
		}
		c.scope.MarkReady()

		listOpts.Watch = true
		listOpts.ResourceVersion = podList.ResourceVersion
		watcher, err := c.client.CoreV1().Pods(c.namespace).Watch(ctx, listOpts)
		if err != nil {
			return false, fmt.Errorf("error watching pods: %v", err)
		}
		ch := watcher.ResultChan()
		for {
			select {
			case <-stopCh:
				klog.Infof("Got stop signal")
				return true, nil
			case event, ok := <-ch:
				if !ok {
					klog.Infof("pod watch channel closed")
					return false, nil
				}

				pod := event.Object.(*v1.Pod)
				klog.V(4).Infof("pod changed: %s %v", event.Type, pod.Name)

				switch event.Type {
				case watch.Added, watch.Modified:
					c.updatePodRecords(pod)

				case watch.Deleted:
					c.scope.Replace(pod.Namespace+"/"+pod.Name, nil)

				default:
					klog.Warningf("Unknown event type: %v", event.Type)
				}
			}
		}
	}

	for {
		stop, err := runOnce()
		if stop {
			return
		}

		if err != nil {
			klog.Warningf("Unexpected error in event watch, will retry: %v", err)
			time.Sleep(10 * time.Second)
		}
	}
}

// podIPsMatchingNodeIP checks which pod IPs match a NodeInternalIP or NodeExternalIP address on the specified node
func (c *PodController) podIPsMatchingNodeIP(pod *v1.Pod, addressType v1.NodeAddressType) []string {
	ctx := context.TODO()
	var matchingIPs []string
	node, err := c.client.CoreV1().Nodes().Get(ctx, pod.Spec.NodeName, metav1.GetOptions{})
	if err != nil {
		klog.Warningf("Failed to get node %q: %v", pod.Spec.NodeName, err)
		return []string{}
	}
	for _, podIP := range pod.Status.PodIPs {
		for _, addr := range node.Status.Addresses {
			if addr.Type == addressType && addr.Address == podIP.IP {
				matchingIPs = append(matchingIPs, addr.Address)
			}
		}
	}
	return matchingIPs
}

// updatePodRecords will apply the records for the specified pod.  It returns the key that was set.
func (c *PodController) updatePodRecords(pod *v1.Pod) string {
	var records []dns.Record

	specExternal := pod.Annotations[AnnotationNameDNSExternal]
	if specExternal != "" {

		tokens := strings.Split(specExternal, ",")

		if pod.Spec.NodeName != "" && pod.Spec.HostNetwork {
			podExternalIPs := c.podIPsMatchingNodeIP(pod, v1.NodeExternalIP)
			if len(podExternalIPs) == 0 {
				klog.V(4).Infof("Pod %q IPs do not match any NodeExternalIP on node %q", pod.Name, pod.Spec.NodeName)
			} else {
				for _, token := range tokens {
					token = strings.TrimSpace(token)
					fqdn := dns.EnsureDotSuffix(token)
					for _, podIP := range podExternalIPs {
						records = append(records, dns.Record{
							RecordType: dns.RecordTypeA,
							FQDN:       fqdn,
							Value:      podIP,
						})
					}
				}
			}
		} else {
			klog.V(4).Infof("Pod %q had %s=%s, but was not HostNetwork or NodeName not set", pod.Name, AnnotationNameDNSExternal, specExternal)
		}
	} else {
		klog.V(4).Infof("Pod %q did not have %s label", pod.Name, AnnotationNameDNSExternal)
	}

	specInternal := pod.Annotations[AnnotationNameDNSInternal]
	if specInternal != "" {

		tokens := strings.Split(specInternal, ",")

		if pod.Spec.NodeName != "" && pod.Spec.HostNetwork {
			podInternalIPs := c.podIPsMatchingNodeIP(pod, v1.NodeInternalIP)
			if len(podInternalIPs) == 0 {
				klog.V(4).Infof("Pod %q IPs do not match any NodeInternalIP on node %q", pod.Name, pod.Spec.NodeName)
			} else {
				for _, token := range tokens {
					token = strings.TrimSpace(token)
					fqdn := dns.EnsureDotSuffix(token)
					for _, podIP := range podInternalIPs {
						records = append(records, dns.Record{
							RecordType: dns.RecordTypeA,
							FQDN:       fqdn,
							Value:      podIP,
						})
					}
				}
			}
		} else {
			klog.V(4).Infof("Pod %q had %s=%s, but was not HostNetwork or NodeName not set", pod.Name, AnnotationNameDNSInternal, specInternal)
		}
	} else {
		klog.V(4).Infof("Pod %q did not have %s label", pod.Name, AnnotationNameDNSInternal)
	}

	key := pod.Namespace + "/" + pod.Name
	c.scope.Replace(key, records)
	return key
}
