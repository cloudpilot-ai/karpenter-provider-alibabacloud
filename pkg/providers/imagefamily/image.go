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
	"sync"

	ecs "github.com/alibabacloud-go/ecs-20140526/v4/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/mitchellh/hashstructure/v2"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/cluster"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/version"
)

type Provider interface {
	List(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass) (Images, error)
}

type DefaultProvider struct {
	region    string
	ecsClient *ecs.Client
	ack       cluster.Provider

	sync.Mutex
	cache *cache.Cache

	versionProvider version.Provider
}

func NewDefaultProvider(region string, ecsClient *ecs.Client, ackProvider cluster.Provider,
	versionProvider version.Provider, cache *cache.Cache) *DefaultProvider {
	return &DefaultProvider{
		region:    region,
		ecsClient: ecsClient,
		ack:       ackProvider,

		cache:           cache,
		versionProvider: versionProvider,
	}
}

// List Get Returning a list of Images with its associated requirements
func (p *DefaultProvider) List(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass) (Images, error) {
	p.Lock()
	defer p.Unlock()

	images, err := p.getImages(ctx, nodeClass)
	if err != nil {
		return nil, err
	}

	return images, nil
}

func (p *DefaultProvider) getImages(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass) (Images, error) {
	hash, err := hashstructure.Hash(nodeClass.Spec.ImageSelectorTerms, hashstructure.FormatV2, &hashstructure.HashOptions{SlicesAsSets: true})
	if err != nil {
		return nil, err
	}
	if images, ok := p.cache.Get(fmt.Sprintf("%d", hash)); ok {
		// Ensure what's returned from this function is a deep-copy of Images so alterations
		// to the data don't affect the original
		return append(Images{}, images.(Images)...), nil
	}

	kubernetesVersion, err := p.versionProvider.Get(ctx)
	if err != nil {
		return nil, err
	}
	images := map[uint64]Image{}
	for _, selectorTerm := range nodeClass.Spec.ImageSelectorTerms {
		var ims Images
		var err error
		if selectorTerm.Alias != "" {
			alias := v1alpha1.NewAlias(selectorTerm.Alias)
			imageFamily := GetImageFamily(selectorTerm.Alias, &Options{ACKProvider: p.ack})
			if imageFamily == nil {
				return nil, fmt.Errorf("unsupported image family %s", selectorTerm.Alias)
			}
			ims, err = imageFamily.GetImages(kubernetesVersion, alias.Version)
			if err != nil {
				return nil, err
			}
		} else {
			ims, err = p.getImagesByID(selectorTerm.ID)
			if err != nil {
				return nil, err
			}
		}

		for _, im := range ims {
			reqsHash := lo.Must(hashstructure.Hash(im.Requirements.NodeSelectorRequirements(),
				hashstructure.FormatV2, &hashstructure.HashOptions{SlicesAsSets: true}))
			// So, this means, the further ahead, the higher the priority.
			if _, ok := images[reqsHash]; ok {
				continue
			}
			images[reqsHash] = im
		}
	}

	p.cache.SetDefault(fmt.Sprintf("%d", hash), Images(lo.Values(images)))
	return lo.Values(images), nil
}

func (p *DefaultProvider) getImagesByID(id string) (Images, error) {
	req := &ecs.DescribeImagesRequest{
		RegionId:    tea.String(p.region),
		ImageId:     tea.String(id),
		ShowExpired: tea.Bool(true),
	}

	resp, err := p.ecsClient.DescribeImages(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get images through id %s", id)
	}

	if resp == nil || resp.Body == nil || resp.Body.Images == nil || len(resp.Body.Images.Image) == 0 {
		return nil, nil
	}

	images := Images{}
	for _, image := range resp.Body.Images.Image {
		arch, ok := v1alpha1.AlibabaCloudToKubeArchitectures[lo.FromPtr(image.Architecture)]
		if !ok {
			continue
		}
		requirement := scheduling.NewRequirement(
			corev1.LabelArchStable, corev1.NodeSelectorOpIn, arch)

		images = append(images, Image{
			Name:         tea.StringValue(image.ImageName),
			ImageID:      id,
			Requirements: scheduling.NewRequirements(requirement),
		})
	}

	return images, nil
}
