/*
Copyright 2023 The Karmada Authors.
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

package app

import (
	"context"
	"flag"
	"fmt"
	"github.com/karmada-io/karmada/cmd/metrics-adapter/app/options"
	generatedopenapi "github.com/karmada-io/karmada/pkg/generated/openapi"
	"github.com/karmada-io/karmada/pkg/metricsadapter"
	"github.com/karmada-io/karmada/pkg/sharedcli"
	"github.com/karmada-io/karmada/pkg/sharedcli/klogflag"
	"github.com/karmada-io/karmada/pkg/version"
	"github.com/karmada-io/karmada/pkg/version/sharedcommand"
	"github.com/spf13/cobra"
	openapinamer "k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/term"
	"k8s.io/klog/v2"
	basecmd "sigs.k8s.io/custom-metrics-apiserver/pkg/cmd"
	"sigs.k8s.io/metrics-server/pkg/api"
)

type MetricsAdapter struct {
	basecmd.AdapterBase
}

// NewMetricsAdapterCommand creates a *cobra.Command object with default parameters
func NewMetricsAdapterCommand(ctx context.Context) *cobra.Command {
	opts := options.NewOptions()

	cmd := &cobra.Command{
		Use:  "karmada-metrics-adapter",
		Long: `The karmada-metrics-adapter is a adapter to aggregate the metrics from member clusters.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.Complete(); err != nil {
				return err
			}
			// validate options
			if err := opts.Validate(); err != nil {
				return err
			}
			if err := run(ctx, opts); err != nil {
				return err
			}
			return nil
		},
		Args: func(cmd *cobra.Command, args []string) error {
			for _, arg := range args {
				if len(arg) > 0 {
					return fmt.Errorf("%q does not take any arguments, got %q", cmd.CommandPath(), args)
				}
			}
			return nil
		},
	}

	fss := cliflag.NamedFlagSets{}

	genericFlagSet := fss.FlagSet("generic")
	genericFlagSet.AddGoFlagSet(flag.CommandLine)
	opts.AddFlags(genericFlagSet)

	// Set klog flags
	logsFlagSet := fss.FlagSet("logs")
	klogflag.Add(logsFlagSet)

	cmd.AddCommand(sharedcommand.NewCmdVersion("karmada-metrics-adapter"))
	cmd.Flags().AddFlagSet(genericFlagSet)
	cmd.Flags().AddFlagSet(logsFlagSet)

	cols, _, _ := term.TerminalSize(cmd.OutOrStdout())
	sharedcli.SetUsageAndHelpFunc(cmd, fss, cols)
	return cmd
}

func run(ctx context.Context, opts *options.Options) error {
	klog.Infof("karmada-metrics-adapter version: %s", version.Get())
	stopCh := genericapiserver.SetupSignalHandler()

	cmd := &MetricsAdapter{}

	cmd.OpenAPIConfig = genericapiserver.DefaultOpenAPIConfig(generatedopenapi.GetOpenAPIDefinitions, openapinamer.NewDefinitionNamer(api.Scheme))
	cmd.OpenAPIConfig.Info.Title = "karmada-metrics-adapter"
	cmd.OpenAPIConfig.Info.Version = version.Get().GitVersion

	provider, err := metricsadapter.NewResourceMetricsProvider()
	if err != nil {
		return err
	}
	resourceLister := metricsadapter.NewResourceLister()

	server, err := cmd.Server()
	if err != nil {
		return err
	}

	if err := api.Install(provider, resourceLister.PodLister, resourceLister.NodeLister, server.GenericAPIServer, nil); err != nil {
		return err
	}

	if err := cmd.Run(stopCh); err != nil {
		return err
	}

	return nil
}
