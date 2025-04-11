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
	"fmt"
	"regexp"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/cluster"
)

var (
	// The format should look like aliyun_3_arm64_20G_alibase_20240819.vhd
	alibabaCloudLinux3ImageIDRegex = regexp.MustCompile("aliyun_3_.*G_alibase_.*vhd")
)

type AlibabaCloudLinux3 struct {
	*Options
}

func (a *AlibabaCloudLinux3) GetImages(kubernetesVersion, imageVersion string) (Images, error) {
	images, err := a.ACKProvider.GetSupportedImages(kubernetesVersion)
	if err != nil {
		return nil, err
	}
	var ret Images
	for _, im := range images {
		if !alibabaCloudLinux3ImageIDRegex.Match([]byte(im.ImageID)) {
			continue
		}
		if image, err := alibabaCloudLinuxResolveImages(im); err == nil {
			ret = append(ret, image)
		}
	}
	return ret, nil
}

func (a *AlibabaCloudLinux3) UserData(ctx context.Context, kubeletConfig *v1alpha1.KubeletConfiguration, taints []corev1.Taint, labels map[string]string, customUserData *string) (string, error) {
	return a.ACKProvider.GetNodeRegisterScript(ctx, labels, taints, kubeletConfig, customUserData)
}

func alibabaCloudLinuxResolveImages(im cluster.Image) (Image, error) {
	// not support i386
	arch, ok := v1alpha1.AlibabaCloudToKubeArchitectures[im.Architecture]
	if !ok {
		return Image{}, fmt.Errorf("image %s has unknown architecture %q", im.ImageID, im.Architecture)
	}
	requirement := scheduling.NewRequirement(corev1.LabelArchStable, corev1.NodeSelectorOpIn, arch)
	return Image{
		Name:         im.ImageName,
		ImageID:      im.ImageID,
		Requirements: scheduling.NewRequirements(requirement),
	}, nil
}
