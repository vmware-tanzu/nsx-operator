// Copyright (c) 2021 VMware, Inc. All rights reserved. VMware Confidential.
// SPDX-License-Identifier: Apache-2.0

// Package main under directory cmd/nsx-operator provide entry point for nsx operator, it parses
// and validate user configuration from config file as well as command line
// options, initializes objects and start the main process.
package main

import (
	"flag"
	"os"

	"github.com/spf13/cobra"
	"pkg/log"
	"pkg/util"
	"k8s.io/klog"
)

func main() {
	// Parse cmd and flags from cmd line. Construct NSX Operator config object from the given config file
	// Start NCP controller with the config object
	cmd := &cobra.Command{
		Use:   "nsxop",
		Short: "The NSX Operator",
		Run: func(cmd *cobra.Command, args []string) {
			log.InitWithFlags(cmd.Flags())
			// TODO Add Config Validation
			nsxOperatorConfig, err := util.NewNSXOperatorConfigFromFile()
			if err != nil {
				klog.Fatalf("Error parsing NSX Operator config: %v", err)
			}
			err = run(nsxOperatorConfig)
			if err != nil {
				klog.Fatalf("Error running controller: %v", err)
			}
		},
	}

	flags := cmd.Flags()
	util.AddFlags(flags)
	log.AddFlags(flags)
	flags.AddGoFlagSet(flag.CommandLine)

	if err := cmd.Execute(); err != nil {
		klog.Flush()
		os.Exit(1)
	}
}
