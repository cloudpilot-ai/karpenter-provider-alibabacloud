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

package client

import (
	"errors"

	cs20151215 "github.com/alibabacloud-go/cs-20151215/v5/client"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	aliyunconfig "github.com/aliyun/aliyun-cli/config"
)

func NewClientConfig() (*openapi.Config, error) {
	profile, err := aliyunconfig.LoadCurrentProfile()
	if err != nil {
		return nil, err
	}

	if profile.RegionId == "" {
		return nil, errors.New("regionId must be set in the config file")
	}

	credentialClient, err := profile.GetCredential(nil, nil)
	if err != nil {
		return nil, err
	}

	return &openapi.Config{
		RegionId:   tea.String(profile.RegionId),
		Credential: credentialClient,
	}, nil
}

func GetClusterID(client *cs20151215.Client, clusterName string) (string, error) {
	describeClustersV1Request := &cs20151215.DescribeClustersV1Request{
		Name: tea.String(clusterName),
	}
	runtime := &util.RuntimeOptions{}
	headers := make(map[string]*string)

	resp, err := client.DescribeClustersV1WithOptions(describeClustersV1Request, headers, runtime)
	if err != nil {
		return "", err
	}

	if resp == nil || resp.Body == nil || len(resp.Body.Clusters) == 0 {
		return "", errors.New("cluster not found")
	}

	if len(resp.Body.Clusters) > 1 {
		return "", errors.New("more than one cluster found")
	}

	return tea.StringValue(resp.Body.Clusters[0].ClusterId), nil
}
