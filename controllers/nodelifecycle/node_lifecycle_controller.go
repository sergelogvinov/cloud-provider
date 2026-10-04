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

package nodelifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformers "k8s.io/client-go/informers/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	v1lister "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	cloudprovider "k8s.io/cloud-provider"
	cloudproviderapi "k8s.io/cloud-provider/api"
	cloudnodeutil "k8s.io/cloud-provider/node/helpers"
	"k8s.io/cloud-provider/node/ownership"
	controllersmetrics "k8s.io/component-base/metrics/prometheus/controllers"
	nodeutil "k8s.io/component-helpers/node/util"
	"k8s.io/klog/v2"
)

const (
	deleteNodeEvent       = "DeletingNode"
	deleteNodeFailedEvent = "DeletingNodeFailed"

	instanceExistsOp   = "instance_exists"
	instanceShutdownOp = "instance_shutdown"
	instanceMetadataOp = "instance_metadata"
)

var ShutdownTaint = &v1.Taint{
	Key:    cloudproviderapi.TaintNodeShutdown,
	Effect: v1.TaintEffectNoSchedule,
}

// CloudNodeLifecycleController is responsible for deleting/updating kubernetes
// nodes that have been deleted/shutdown on the cloud provider
type CloudNodeLifecycleController struct {
	kubeClient clientset.Interface
	nodeLister v1lister.NodeLister

	broadcaster record.EventBroadcaster
	recorder    record.EventRecorder

	cloud cloudprovider.Interface
	// ownership determines which nodes are managed by this cloud provider.
	ownership *ownership.Checker
	// nodeLifecycleWaitTimeout is the time to wait for a node without ProviderID
	// to be initialized by its own cloud controller manager. Nil means the default.
	nodeLifecycleWaitTimeout *time.Duration

	// Value controlling NodeController monitoring period, i.e. how often does NodeController
	// check node status posted from kubelet. This value should be lower than nodeMonitorGracePeriod
	// set in controller-manager
	nodeMonitorPeriod time.Duration

	// Value controlling NodeController monitoring loop worker number.
	concurrentNodeLifecycleSyncs int
}

// Option configures the CloudNodeLifecycleController.
type Option func(*CloudNodeLifecycleController)

// WithNodeLifecycleWaitTimeout sets the time the controller waits for a node without
// ProviderID to be initialized by its own cloud controller manager, counted from the
// node creation. It is used in both single-cloud and hybrid mode.
// If not set, it defaults to ownership.DefaultNodeLifecycleWaitTimeout in hybrid mode and 0 otherwise.
func WithNodeLifecycleWaitTimeout(timeout time.Duration) Option {
	return func(c *CloudNodeLifecycleController) {
		c.nodeLifecycleWaitTimeout = &timeout
	}
}

func NewCloudNodeLifecycleController(
	nodeInformer coreinformers.NodeInformer,
	kubeClient clientset.Interface,
	cloud cloudprovider.Interface,
	nodeMonitorPeriod time.Duration,
	concurrentNodeLifecycleSyncs int,
	opts ...Option) (*CloudNodeLifecycleController, error) {

	registerMetrics()

	if kubeClient == nil {
		return nil, errors.New("kubernetes client is nil")
	}

	if cloud == nil {
		return nil, errors.New("no cloud provider provided")
	}

	_, instancesSupported := cloud.Instances()
	_, instancesV2Supported := cloud.InstancesV2()
	if !instancesSupported && !instancesV2Supported {
		return nil, errors.New("cloud provider does not support instances")
	}

	if concurrentNodeLifecycleSyncs < 1 {
		return nil, fmt.Errorf("concurrentNodeLifecycleSyncs must be >= 1, got %d", concurrentNodeLifecycleSyncs)
	}

	c := &CloudNodeLifecycleController{
		kubeClient:                   kubeClient,
		nodeLister:                   nodeInformer.Lister(),
		cloud:                        cloud,
		ownership:                    ownership.NewChecker(ownership.Platforms(cloud)),
		nodeMonitorPeriod:            nodeMonitorPeriod,
		concurrentNodeLifecycleSyncs: concurrentNodeLifecycleSyncs,
	}

	for _, opt := range opts {
		opt(c)
	}

	if c.ownership.HybridMode() {
		klog.Infof("Node ownership is enabled, managing nodes with platforms: %v", c.ownership.Platforms())
	}
	if timeout := c.waitTimeout(); timeout > 0 {
		klog.Infof("Node lifecycle wait timeout for nodes without ProviderID: %v", timeout)
	}

	return c, nil
}

// Run starts the main loop for this controller. Run is blocking so should
// be called via a goroutine
func (c *CloudNodeLifecycleController) Run(ctx context.Context, controllerManagerMetrics *controllersmetrics.ControllerManagerMetrics) {
	c.broadcaster = record.NewBroadcaster(record.WithContext(ctx))
	c.recorder = c.broadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "cloud-node-lifecycle-controller"})

	defer utilruntime.HandleCrash()
	controllerManagerMetrics.ControllerStarted("cloud-node-lifecycle")
	defer controllerManagerMetrics.ControllerStopped("cloud-node-lifecycle")

	// Start event processing pipeline.
	klog.Info("Sending events to api server")
	c.broadcaster.StartStructuredLogging(0)
	c.broadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: c.kubeClient.CoreV1().Events("")})
	defer c.broadcaster.Shutdown()

	// The following loops run communicate with the APIServer with a worst case complexity
	// of O(num_nodes) per cycle. These functions are justified here because these events fire
	// very infrequently. DO NOT MODIFY this to perform frequent operations.

	// Start a loop to periodically check if any nodes have been
	// deleted or shutdown from the cloudprovider
	wait.UntilWithContext(ctx, c.MonitorNodes, c.nodeMonitorPeriod)
}

// MonitorNodes checks to see if nodes in the cluster have been deleted
// or shutdown. If deleted, it deletes the node resource. If shutdown it
// applies a shutdown taint to the node
func (c *CloudNodeLifecycleController) MonitorNodes(ctx context.Context) {
	startTime := time.Now()

	nodes, err := c.nodeLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("error listing nodes from cache: %s", err)
		return
	}

	processNode := func(piece int) {
		node := nodes[piece].DeepCopy()
		if c.skipNode(node) {
			return
		}

		// Default NodeReady status to v1.ConditionUnknown
		status := v1.ConditionUnknown
		if _, c := nodeutil.GetNodeCondition(&node.Status, v1.NodeReady); c != nil {
			status = c.Status
		}

		if status == v1.ConditionTrue {
			// if taint exist remove taint
			if err := cloudnodeutil.RemoveTaintOffNode(c.kubeClient, node.Name, node, ShutdownTaint); err != nil {
				klog.Errorf("error patching node taints: %v", err)
			}
			return
		}

		// At this point the node has NotReady status, we need to check if the node has been removed
		// from the cloud provider. If node cannot be found in cloudprovider, then delete the node
		exists, err := c.ensureNodeExistsByProviderID(ctx, node)
		if err != nil {
			if ownership.IsNotOwned(err) {
				ownership.RecordNotOwned(ownership.ControllerNodeLifecycle, node)
				return
			}
			klog.Errorf("error checking if node %s exists: %v", node.Name, err)
			return
		}

		if !exists {
			// Current node does not exist, we should delete it, its taints do not matter anymore

			klog.V(2).Infof("deleting node since it is no longer present in cloud provider: %s", node.Name)

			ref := &v1.ObjectReference{
				APIVersion: "v1",
				Kind:       "Node",
				Name:       node.Name,
				UID:        node.UID,
				Namespace:  "",
			}

			c.recorder.Eventf(ref, v1.EventTypeNormal, deleteNodeEvent,
				"Deleting node %s because it does not exist in the cloud provider", node.Name)

			if err := c.kubeClient.CoreV1().Nodes().Delete(ctx, node.Name, metav1.DeleteOptions{}); err != nil {
				klog.Errorf("unable to delete node %q: %v", node.Name, err)
				c.recorder.Eventf(ref, v1.EventTypeWarning, deleteNodeFailedEvent,
					"Failed deleting node %s: %v", node.Name, err)
			}
		} else {
			// Node exists. We need to check this to get taint working in similar in all cloudproviders
			// current problem is that shutdown nodes are not working in similar way ie. all cloudproviders
			// does not delete node from kubernetes cluster when instance it is shutdown see issue #46442
			shutdown, err := c.shutdownInCloudProvider(ctx, node)
			if err != nil {
				if ownership.IsNotOwned(err) {
					ownership.RecordNotOwned(ownership.ControllerNodeLifecycle, node)
					return
				}
				klog.Errorf("error checking if node %s is shutdown: %v", node.Name, err)
				return
			}

			if shutdown {
				// if node is shutdown add shutdown taint
				if err := cloudnodeutil.AddOrUpdateTaintOnNode(c.kubeClient, node.Name, ShutdownTaint); err != nil {
					klog.Errorf("failed to apply shutdown taint to node %s, it may have been deleted.", node.Name)
				}
			}
		}
	}

	workqueue.ParallelizeUntil(ctx, c.concurrentNodeLifecycleSyncs, len(nodes), processNode)

	duration := time.Since(startTime).Seconds()
	monitorNodesDuration.Observe(duration)
}

// waitTimeout returns the time to wait for a node without ProviderID to be initialized.
// An explicitly set timeout is used in any mode, otherwise it defaults to
// ownership.DefaultNodeLifecycleWaitTimeout in hybrid mode and 0 (no wait) otherwise.
func (c *CloudNodeLifecycleController) waitTimeout() time.Duration {
	if c.nodeLifecycleWaitTimeout != nil {
		return *c.nodeLifecycleWaitTimeout
	}
	if c.ownership.HybridMode() {
		return ownership.DefaultNodeLifecycleWaitTimeout
	}
	return 0
}

// skipNode returns true if the node must not be processed by this cloud provider:
// the node belongs to another platform, or it has no ProviderID yet and
// its own cloud controller manager may still initialize it.
func (c *CloudNodeLifecycleController) skipNode(node *v1.Node) bool {
	if node.Spec.ProviderID == "" {
		timeout := c.waitTimeout()
		if age := time.Since(node.CreationTimestamp.Time); age < timeout {
			klog.V(4).Infof("Skipping node %s without ProviderID, waiting %v for node initialization", node.Name, timeout-age)
			ownership.RecordSkipped(ownership.ControllerNodeLifecycle, ownership.ReasonTimeoutWait)
			return true
		}
		// The node was not initialized in time, use the default behavior.
		return false
	}

	return c.ownership.SkipNode(ownership.ControllerNodeLifecycle, node)
}

// getProviderID returns the provider ID for the node. If Node CR has no provider ID,
// it will be the one from the cloud provider.
func (c *CloudNodeLifecycleController) getProviderID(ctx context.Context, node *v1.Node) (string, error) {
	if node.Spec.ProviderID != "" {
		return node.Spec.ProviderID, nil
	}

	if instanceV2, ok := c.cloud.InstancesV2(); ok {
		metadata, err := instanceV2.InstanceMetadata(ctx, node)
		observeInstanceOp(instanceMetadataOp, err)
		if err != nil {
			return "", err
		}
		return metadata.ProviderID, nil
	}

	providerID, err := cloudprovider.GetInstanceProviderID(ctx, c.cloud, types.NodeName(node.Name))
	observeInstanceOp(instanceMetadataOp, err)
	if err != nil {
		return "", err
	}
	return providerID, nil
}

// shutdownInCloudProvider returns true if the node is shutdown on the cloud provider
func (c *CloudNodeLifecycleController) shutdownInCloudProvider(ctx context.Context, node *v1.Node) (bool, error) {
	if instanceV2, ok := c.cloud.InstancesV2(); ok {
		shutdown, err := instanceV2.InstanceShutdown(ctx, node)
		observeInstanceOp(instanceShutdownOp, err)
		return shutdown, err
	}

	instances, ok := c.cloud.Instances()
	if !ok {
		return false, errors.New("cloud provider does not support instances")
	}

	providerID, err := c.getProviderID(ctx, node)
	if err != nil {
		if errors.Is(err, cloudprovider.InstanceNotFound) {
			return false, nil
		}
		return false, err
	}

	shutdown, err := instances.InstanceShutdownByProviderID(ctx, providerID)
	observeInstanceOp(instanceShutdownOp, err)
	if errors.Is(err, cloudprovider.NotImplemented) {
		return false, nil
	}

	return shutdown, err
}

// ensureNodeExistsByProviderID checks if the instance exists by the provider id,
func (c *CloudNodeLifecycleController) ensureNodeExistsByProviderID(ctx context.Context, node *v1.Node) (bool, error) {
	if instanceV2, ok := c.cloud.InstancesV2(); ok {
		exists, err := instanceV2.InstanceExists(ctx, node)
		if err == nil && !exists {
			observeInstanceOp(instanceExistsOp, cloudprovider.InstanceNotFound)
		} else {
			observeInstanceOp(instanceExistsOp, err)
		}
		return exists, err
	}

	instances, ok := c.cloud.Instances()
	if !ok {
		return false, errors.New("instances interface not supported in the cloud provider")
	}

	providerID, err := c.getProviderID(ctx, node)
	if err != nil {
		if errors.Is(err, cloudprovider.InstanceNotFound) {
			return false, nil
		}
		return false, err
	}

	exists, err := instances.InstanceExistsByProviderID(ctx, providerID)
	if err == nil && !exists {
		observeInstanceOp(instanceExistsOp, cloudprovider.InstanceNotFound)
	} else {
		observeInstanceOp(instanceExistsOp, err)
	}
	return exists, err
}

func observeInstanceOp(operation string, err error) {
	result := "success"
	switch {
	case errors.Is(err, cloudprovider.NotImplemented):
		result = "not_implemented"
	case errors.Is(err, cloudprovider.InstanceNotFound):
		result = "instance_not_found"
	case errors.Is(err, cloudprovider.NotOwned):
		result = "not_owned"
	case errors.Is(err, context.Canceled):
		result = "canceled"
	case err != nil:
		result = "error"
	}
	cloudProviderCalls.WithLabelValues(operation, result).Inc()
}
