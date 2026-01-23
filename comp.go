package main

import (
	"context"
	"log"

	"github.com/hujun-open/completers"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"kubenetlab.net/knl/api/v1beta1"
)

func (cli *CLI) knlNodeComp(labName string) ([]cobra.Completion, cobra.ShellCompDirective) {
	clnt, err := cli.getClnt()
	if err != nil {
		log.Fatal(err)
	}
	lab := &v1beta1.Lab{}
	labKey := types.NamespacedName{Namespace: cli.Namespace, Name: labName}
	err = clnt.Get(context.Background(), labKey, lab)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	opts := []string{}
	for nodeName := range lab.Spec.NodeList {
		opts = append(opts, nodeName)
	}
	return opts, cobra.ShellCompDirectiveNoFileComp
}

func (cli *CLI) K8sLabComp(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	opts, err := completers.GetResourceNames(cli.KubeCfgPath, v1beta1.GroupVersion.Group, v1beta1.GroupVersion.Version, "labs", cli.Namespace, "")
	if err != nil {
		log.Fatal(err)

	}
	return opts, cobra.ShellCompDirectiveNoFileComp
}
