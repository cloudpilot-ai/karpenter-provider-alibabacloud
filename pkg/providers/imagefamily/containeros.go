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

package imagefamily

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
)

type ContainerOS struct {
	*Options
}

func (c *ContainerOS) GetImages(kubernetesVersion, imageVersion string) (Images, error) {
	images, err := c.ACKProvider.GetSupportedImages(kubernetesVersion)
	if err != nil {
		return nil, err
	}
	var ret Images
	for _, im := range images {
		if !strings.HasPrefix(im.ImageName, "ContainerOS") {
			continue
		}
		if image, err := alibabaCloudLinuxResolveImages(im); err == nil {
			ret = append(ret, image)
		}
	}
	return ret, nil
}

func (c *ContainerOS) UserData(ctx context.Context, kubeletConfig *v1alpha1.KubeletConfiguration, taints []corev1.Taint, labels map[string]string, customUserData *string) (string, error) {
	return c.ACKProvider.GetNodeRegisterScript(ctx, labels, taints, kubeletConfig, customUserData)
}
