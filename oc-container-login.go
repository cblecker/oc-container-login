// Command oc-container-login is an oc plugin that logs Apple's container CLI
// in to a registry with the token of the current OpenShift session.
package main

import (
	"os"

	"github.com/spf13/pflag"

	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/cblecker/oc-container-login/pkg/login"
)

func main() {
	flags := pflag.NewFlagSet("oc-container-login", pflag.ExitOnError)
	pflag.CommandLine = flags

	root := login.NewCmdLogin(genericclioptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr})
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
