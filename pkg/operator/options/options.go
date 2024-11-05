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

package options

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	cs20151215 "github.com/alibabacloud-go/cs-20151215/v5/client"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	"sigs.k8s.io/karpenter/pkg/utils/env"

	"github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/utils"
	utilsclient "github.com/cloudpilot-ai/karpenter-provider-alicloud/pkg/utils/client"
)

func init() {
	coreoptions.Injectables = append(coreoptions.Injectables, &Options{})
}

type optionsKey struct{}

type Options struct {
	ClusterName             string
	ClusterID               string
	ClusterCNI              string
	VMMemoryOverheadPercent float64
	Interruption            bool
}

func (o *Options) AddFlags(fs *coreoptions.FlagSet) {
	fs.StringVar(&o.ClusterName, "cluster-name", env.WithDefaultString("CLUSTER_NAME", ""), "[REQUIRED] The kubernetes cluster name for resource discovery.")
	fs.StringVar(&o.ClusterID, "cluster-id", env.WithDefaultString("CLUSTER_ID", ""), "The external kubernetes cluster id for new nodes to connect with.")
	fs.StringVar(&o.ClusterCNI, "cluster-cni", env.WithDefaultString("CLUSTER_CNI", "terway-eniip"), "The network cni used by the cluster.")
	fs.Float64Var(&o.VMMemoryOverheadPercent, "vm-memory-overhead-percent", utils.WithDefaultFloat64("VM_MEMORY_OVERHEAD_PERCENT", 0.075), "The VM memory overhead as a percent that will be subtracted from the total memory for all instance types.")
	fs.BoolVar(&o.Interruption, "interruption", env.WithDefaultBool("INTERRUPTION", true), "Enable interruption handling.")
}

func (o *Options) Parse(fs *coreoptions.FlagSet, args ...string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		return fmt.Errorf("parsing flags, %w", err)
	}

	clientConfig, err := utilsclient.NewClientConfig()
	if err != nil {
		return fmt.Errorf("getting client config, %w", err)
	}

	if o.ClusterID != "" && o.ClusterName == "" {
		client, err := cs20151215.NewClient(clientConfig)
		if err != nil {
			return fmt.Errorf("creating cs client, %w", err)
		}
		o.ClusterName, err = utilsclient.GetClusterName(client, &o.ClusterID)
		if err != nil {
			return fmt.Errorf("getting cluster name, %w", err)
		}
	}

	if err := o.Validate(); err != nil {
		return fmt.Errorf("validating options, %w", err)
	}
	return nil
}

func (o *Options) ToContext(ctx context.Context) context.Context {
	return ToContext(ctx, o)
}

func ToContext(ctx context.Context, opts *Options) context.Context {
	return context.WithValue(ctx, optionsKey{}, opts)
}

func FromContext(ctx context.Context) *Options {
	retval := ctx.Value(optionsKey{})
	if retval == nil {
		return nil
	}
	return retval.(*Options)
}
