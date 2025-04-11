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
	"testing"

	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/assert"

	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
)

func Test_convertNodeClassKubeletConfigToACKNodeConfig(t *testing.T) {
	kubeletCfg := &v1alpha1.KubeletConfiguration{
		MaxPods: tea.Int32(110),
	}
	d := convertNodeClassKubeletConfigToACKNodeConfig(kubeletCfg)
	assert.Equal(t, "eyJrdWJlbGV0X2NvbmZpZyI6eyJtYXhQb2RzIjoxMTB9fQ==", d)
}
