/*
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

package cache

import "time"

const (
	// DefaultTTL restricts to Ali APIs to this interval for verifying setup
	// resources. This value represents the maximum eventual consistency between
	// Ali actual state and the controller's ability to provision those
	// resources. Cache hits enable faster provisioning and reduced API load on
	// Ali APIs, which can have a serious impact on performance and scalability.
	// DO NOT CHANGE THIS VALUE WITHOUT DUE CONSIDERATION
	DefaultTTL = time.Minute
	// KubernetesVersionTTL is the time before the detected Kubernetes version is removed from cache,
	// to be re-detected the next time it is needed.
	KubernetesVersionTTL = 15 * time.Minute
	// UnavailableOfferingsTTL is the time before offerings that were marked as unavailable
	// are removed from the cache and are available for launch again
	UnavailableOfferingsTTL = 3 * time.Minute
	// AvailableIPAddressTTL is time to drop AvailableIPAddress data if it is not updated within the TTL
	AvailableIPAddressTTL = 5 * time.Minute
	// InstanceTypeAvailableDiskTTL is the time refresh InstanceType compatible disk
	InstanceTypeAvailableDiskTTL = 30 * time.Minute
	// ClusterAttachScriptTTL is the time refresh for the cluster attach script
	ClusterAttachScriptTTL = 6 * time.Hour

	// DefaultCleanupInterval triggers cache cleanup (lazy eviction) at this interval.
	DefaultCleanupInterval = 1 * time.Minute
	// UnavailableOfferingsCleanupInterval triggers cache cleanup (lazy eviction) at this interval.
	// We drop the cleanup interval down for the ICE cache to get quicker reactivity to offerings
	// that become available after they get evicted from the cache
	UnavailableOfferingsCleanupInterval = time.Second * 10

	// InstanceTypesAndZonesTTL is the time before we refresh instance types and zones at ECS
	InstanceTypesAndZonesTTL = 5 * time.Minute
)
