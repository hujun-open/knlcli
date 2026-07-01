package main

import (
	"context"
	"fmt"
	"log"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"kubenetlab.net/knl/api/v1beta1"
)

func (cli *CLI) ExecNode(cmd *cobra.Command, args []string) {
	if cli.Exec.Cmd == "" {
		log.Fatal("command not specified")
	}
	clnt, err := cli.getClnt()
	if err != nil {
		log.Fatal(err)
	}
	lab := &v1beta1.Lab{}
	labKey := types.NamespacedName{Namespace: cli.Namespace, Name: cli.Exec.Lab}
	err = clnt.Get(context.Background(), labKey, lab)
	if err != nil {
		log.Fatal(err)
	}
	node, ok := lab.Spec.NodeList[cli.Exec.Node]
	if !ok {
		log.Fatalf("node %v is not specified in the lab %v", cli.Exec.Node, cli.Exec.Lab)
	}
	sys, _ := node.GetSystem()
	output, err := sys.Exec(context.Background(), clnt, cli.Namespace, cli.Exec.Lab, cli.Exec.Node, cli.Exec.Username, cli.Exec.Passwd, cli.Exec.Cmd)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(output)
}
