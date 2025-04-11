/*
Copyright 2024 The CloudPilot AI Authors.

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

package cluster

import (
	"context"
	"net/http"

	ackclient "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/patrickmn/go-cache"
	corev1 "k8s.io/api/core/v1"

	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	alicache "github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/cache"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/operator/options"
)

// FeatureFlags describes whether the features below are enabled
type FeatureFlags struct {
	PodsPerCoreEnabled           bool
	SupportsENILimitedPodDensity bool
}

type Image struct {
	ImageID      string
	ImageName    string
	Platform     string
	OsVersion    string
	ImageType    string
	OsType       string
	Architecture string
}

// Provider can be implemented to generate userdata
type Provider interface {
	UserData(context.Context, map[string]string, []corev1.Taint, *v1alpha1.KubeletConfiguration, *string) (string, error)
	GetClusterCNI(context.Context) (string, error)
	LivenessProbe(*http.Request) error
	GetSupportedImages(string) ([]Image, error)
	FeatureFlags() FeatureFlags
}

func NewClusterProvider(ctx context.Context, ackClient *ackclient.Client, region string) Provider {
	clusterID := options.FromContext(ctx).ClusterID
	return NewACKManaged(clusterID, region, ackClient, cache.New(alicache.ClusterAttachScriptTTL, alicache.DefaultCleanupInterval))
}
