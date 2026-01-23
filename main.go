package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/hujun-open/completers"
	"github.com/hujun-open/myflags/v2"
	v1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"kubenetlab.net/knl/api/v1beta1"
	kvv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var VERSION string = "no_version"

type CLI struct {
	KubeCfgPath string `alias:"kubeconf" usage:"path to k8s config"`
	Create      struct {
		Labdef string `alias:"lab" noun:"1" usage:"knl lab yaml file path"`
	} `action:"CreateLab" usage:"create a lab via the specified YAML file"`
	Remove struct {
		Lab string `noun:"1" usage:"knl lab name" complete:"K8sLabComp"`
		All bool   `usage:"remove all labs in the specified namespace"`
	} `action:"DelLab" alias:"rm" usage:"remove a lab"`
	Show struct {
		Lab string `noun:"1" usage:"knl lab name" complete:"K8sLabComp"`
	} `action:"ShowLabs" usage:"show existing Lab info"`
	Shell struct {
		Lab  string `noun:"1" usage:"knl lab name" complete:"K8sLabComp"`
		Node string `noun:"2" usage:"node name" complete:"ShellKNLNodeComp"`
	} `action:"ShellNode" usage:"connect to the specified node in the specified lab"`
	Console struct {
		Lab  string `noun:"1" usage:"knl lab name" complete:"K8sLabComp"`
		Node string `noun:"2" usage:"node name" complete:"ConsoleKNLNodeComp"`
	} `action:"ConsoleNode" usage:"connect to the console of specified node in the specified lab"`
	Topo struct {
		Lab    string `noun:"1" usage:"knl lab name" complete:"K8sLabComp"`
		Render bool   `usage:"render to asii output if true, require d2 installed"`
	} `action:"ShowTopo" usage:"generate lab topology in D2 format"`
	Namespace string `alias:"ns" short:"n" usage:"k8s namespace" complete:"K8sNSComp"`
}

func (cli *CLI) getClnt() (client.Client, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", cli.KubeCfgPath)
	if err != nil {
		panic(fmt.Sprintf("Error building kubeconfig from %s: %v", cli.KubeCfgPath, err))
	}
	scheme := runtime.NewScheme()
	v1.AddToScheme(scheme)
	corev1.AddToScheme(scheme)
	kvv1.AddToScheme(scheme)
	v1beta1.AddToScheme(scheme)
	return client.New(cfg, client.Options{Scheme: scheme})
}

func (cli *CLI) ShellKNLNodeComp(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return cli.knlNodeComp(cli.Shell.Lab)
}

func (cli *CLI) ConsoleKNLNodeComp(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return cli.knlNodeComp(cli.Console.Lab)
}

func (cli *CLI) K8sVMIComp(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	opts, err := completers.GetResourceNames("kubevirt.io", "v1", "virtualmachineinstances", cli.Namespace, "", cli.KubeCfgPath)
	if err != nil {
		log.Fatal(err)
		return nil, cobra.ShellCompDirectiveError
	}

	return opts, cobra.ShellCompDirectiveNoFileComp
}

func (cli *CLI) K8sNSComp(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	nsc := completers.CoreV1Completer{Kind: "namespaces", Namespace: "", KubeConfPath: cli.KubeCfgPath}
	return nsc.Complete(cmd, args, toComplete)
}

func (cli *CLI) DelLab(cmd *cobra.Command, args []string) {

	clnt, err := cli.getClnt()
	if err != nil {
		log.Fatal(err)
	}
	lab := &v1beta1.Lab{}
	if !cli.Remove.All {
		lab.Name = cli.Remove.Lab
		lab.Namespace = cli.Namespace
		err = clnt.Delete(context.Background(), lab)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%v has been removed\n", lab.Name)
	} else {
		if !askForConfirmation(fmt.Sprintf("all labs in namespace %v will be removed, continue?", cli.Namespace)) {
			fmt.Println("aborted")
			return
		}
		err = clnt.DeleteAllOf(context.Background(), lab, client.InNamespace(cli.Namespace))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("all labs in namespace %v are removed\n", cli.Namespace)
	}

}

func (cli *CLI) CreateLab(cmd *cobra.Command, args []string) {
	if cli.Create.Labdef == "" {
		log.Fatal("lab yaml file not specified")
	}
	clnt, err := cli.getClnt()
	if err != nil {
		log.Fatal(err)
	}
	buf, err := os.ReadFile(cli.Create.Labdef)
	if err != nil {
		log.Fatal(err)
	}
	scheme := runtime.NewScheme()
	v1beta1.AddToScheme(scheme)
	decode := serializer.NewCodecFactory(scheme).UniversalDeserializer().Decode
	lab := new(v1beta1.Lab)
	_, _, err = decode(buf, nil, lab)
	if err != nil {
		log.Fatal(err)
	}
	err = clnt.Create(context.Background(), lab)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(lab.Name, "created")
}

func DefCLI() *CLI {
	kpath := ""
	if home := homedir.HomeDir(); home != "" {
		kpath = filepath.Join(home, ".kube", "config")
	}
	r := &CLI{
		Namespace:   "knl-system",
		KubeCfgPath: kpath,
	}
	r.Topo.Render = true
	return r
}
func main() {
	log.SetFlags(log.Ltime | log.Lshortfile)
	cli := DefCLI()
	filler := myflags.NewFiller("knlcli", "KNL CLI tool", myflags.WithShellCompletionCMD())
	err := filler.Fill(cli)
	if err != nil {
		panic(err)
	}
	filler.Version = VERSION
	err = filler.Execute()
	if err != nil {
		panic(err)
	}
}

func askForConfirmation(prompt string) bool {
	reader := bufio.NewReader(os.Stdin)

	fmt.Printf("%s [y/n]: ", prompt)

	response, err := reader.ReadString('\n')
	if err != nil {
		log.Fatal(err)
	}

	response = strings.ToLower(strings.TrimSpace(response))

	if response == "y" || response == "yes" {
		return true
	} else {
		return false
	}
}
