package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"kubenetlab.net/knl/api/v1beta1"
)

func (cli *CLI) ShowTopo(cmd *cobra.Command, args []string) {
	clnt, err := cli.getClnt()
	if err != nil {
		log.Fatal(err)
	}
	if cli.Topo.Lab == "" {
		log.Fatal("lab name not specified")
	}
	labKey := types.NamespacedName{Namespace: cli.Namespace, Name: cli.Topo.Lab}
	targetLab := v1beta1.Lab{}
	err = clnt.Get(context.Background(), labKey, &targetLab)
	if err != nil {
		log.Fatal(err)
	}
	topoInputStr := ""
	for _, link := range targetLab.Spec.LinkList {
		topoInputStr += "\n"
		for _, c := range link.Connectors {
			topoInputStr += fmt.Sprintf("%v -- ", *c.NodeName)
		}
		topoInputStr = strings.TrimSuffix(topoInputStr, " -- ")
	}
	if cli.Topo.Render {
		d2cmd := exec.Command("/bin/sh", "-c", "d2 --stdout-format=txt - -")
		d2cmd.Stdin = strings.NewReader(topoInputStr)
		// var out bytes.Buffer
		// d2cmd.Stdout = &out
		out, err := d2cmd.CombinedOutput()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(string(out))
	} else {
		fmt.Println(topoInputStr)
	}
}
