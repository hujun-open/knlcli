package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"kubenetlab.net/knl/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (cli *CLI) ShowLabs(cmd *cobra.Command, args []string) {
	clnt, err := cli.getClnt()
	if err != nil {
		log.Fatal(err)
	}
	labList := []v1beta1.Lab{}
	if cli.Show.Lab == "" {
		//list all labs
		labs := &v1beta1.LabList{}
		err = clnt.List(context.Background(), labs)
		if err != nil {
			log.Fatal(err)
		}
		labList = labs.Items
	} else {
		labKey := types.NamespacedName{Namespace: cli.Namespace, Name: cli.Show.Lab}
		targetLab := v1beta1.Lab{}
		err := clnt.Get(context.Background(), labKey, &targetLab)
		if err != nil {
			log.Fatal(err)
		}
		labList = append(labList, targetLab)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	defer w.Flush()
	sort.Slice(labList, func(i, j int) bool { return labList[i].Name < labList[j].Name })
	for i, lab := range labList {
		fmt.Fprintf(w, "%v:\n", lab.Name)
		fmt.Fprintln(w, "\tNode\tType\tChassis\tPods\tWorker/PodIP")
		sortedNodeList := v1beta1.GetSortedKeySlice(lab.Spec.NodeList)

		for _, node := range sortedNodeList {
			syst := lab.Spec.NodeList[node]
			sys, nts := syst.GetSystem()
			chassis := "n/a"
			// slots := "n/a"
			switch v1beta1.NodeType(strings.ToLower(nts)) {
			case v1beta1.SRSIM:
				chassis = *((sys).(*v1beta1.SRSim).Chassis.Model)
				// slots = strconv.Itoa(len((sys).(*v1beta1.SRSim).Chassis.Cards))
			case v1beta1.SRVMMAGC:
				chassis = *((sys).(*v1beta1.MAGC).Chassis.Model)
				// slots = strconv.Itoa(len((sys).(*v1beta1.MAGC).Chassis.Cards))
			case v1beta1.SRVMVSIM:
				chassis = *((sys).(*v1beta1.VSIM).Chassis.Model)
				// slots = strconv.Itoa(len((sys).(*v1beta1.VSIM).Chassis.Cards))
			case v1beta1.SRVMVSRI:
				chassis = *((sys).(*v1beta1.VSRI).Chassis.Model)
				// slots = "1"
			case v1beta1.SRL:
				chassis = *((sys).(*v1beta1.SRLinux).Chassis)
				// slots = "1"
			}
			pods := getPods(cmd.Context(), clnt, cli.Namespace, lab.Name, node)
			fmt.Fprintf(w, "\t%v\t%v\t%v\t%v\t%v\n", node, nts, chassis, pods[0].Name, pods[0].Spec.NodeName+"/"+pods[0].Status.PodIP)
			if len(pods) > 1 {
				for i := 1; i < len(pods); i++ {
					fmt.Fprintf(w, "\t\t\t\t%v\t%v\n", pods[1].Name, pods[i].Spec.NodeName+"/"+pods[i].Status.PodIP)
				}
			}
		}
		sortedLinkList := v1beta1.GetSortedKeySlice(lab.Spec.LinkList)
		fmt.Fprintln(w, "\tLink\tNodes")
		for _, link := range sortedLinkList {
			fmt.Fprintf(w, "\t%v\t%v\n", link, *lab.Spec.LinkList[link].Connectors[0].NodeName)
			if len(lab.Spec.LinkList[link].Connectors) > 1 {
				for i := 1; i < len(lab.Spec.LinkList[link].Connectors); i++ {
					fmt.Fprintf(w, "\t\t%v\n", *lab.Spec.LinkList[link].Connectors[i].NodeName)
				}
			}
		}

		if i != len(labList)-1 {
			fmt.Fprintf(w, "---\n")
		}
	}

}

func getPods(ctx context.Context, clnt client.Client, ns, lab, node string) []corev1.Pod {
	pods := new(corev1.PodList)
	listOpts := []client.ListOption{
		client.InNamespace(ns),
		client.MatchingLabels{
			v1beta1.K8SLABELSETUPKEY:      lab,
			v1beta1.ChassisNameAnnotation: node,
		},
	}
	err := clnt.List(ctx, pods, listOpts...)
	if err != nil {
		log.Fatal(err)
	}
	return pods.Items
}
