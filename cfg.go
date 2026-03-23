package main

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"kubenetlab.net/knl/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/yaml"
)

func addGVK(lab *v1beta1.Lab) {
	scheme := runtime.NewScheme()
	err := v1beta1.AddToScheme(scheme)
	if err != nil {
		panic(err)
	}
	gvk, err := apiutil.GVKForObject(lab, scheme)
	if err != nil {
		panic(err)
	}
	lab.SetGroupVersionKind(gvk)
}

func (cli *CLI) SaveCfg(cmd *cobra.Command, args []string) {
	clnt, err := cli.getClnt()
	if err != nil {
		log.Fatal(err)
	}
	lab := &v1beta1.Lab{}
	labKey := types.NamespacedName{Namespace: cli.Namespace, Name: cli.Config.Save.Lab}
	err = clnt.Get(context.Background(), labKey, lab)
	if err != nil {
		log.Fatal(err)
	}
	outFolder := filepath.Join(cli.Config.Save.Output, lab.Name)
	err = os.MkdirAll(outFolder, 0744)
	if err != nil {
		log.Fatal(err)
	}
	//save lab manifest
	cleanLab := lab.GetClean()
	addGVK(cleanLab)
	labYaml, err := yaml.Marshal(cleanLab)
	if err != nil {
		log.Fatalf("failed to marshal lab %v to YAML, %v", lab.Name, err)
	}
	outFile := filepath.Join(outFolder, "lab.yaml")
	err = os.WriteFile(outFile, labYaml, 0644)
	if err != nil {
		log.Fatalf("failed to save lab %v's YAML, %v", lab.Name, err)
	}
	log.Printf("saved lab %v YAML to %v", lab.Name, outFile)
	passwd := cli.Config.Passwd
	for nodeName, node := range lab.Spec.NodeList {
		sys, sysType := node.GetSystem()
		log.Printf("saving config for node %v ...", nodeName)
		if cli.Config.Passwd == "" {
			switch sysType {
			case "VSIM", "VSRI", "SRSIM", "MAGC":
				passwd = "admin"
			case "SRL":
				passwd = "NokiaSrl1!"

			}
		}
		cfg, err := sys.GetCfg(context.Background(), clnt, cli.Namespace, cli.Config.Save.Lab, nodeName, cli.Config.User, passwd)
		if err != nil {
			log.Fatal(err)
		}
		if cfg == "" {
			log.Printf("%v returns empty string, skip saving", nodeName)
			continue
		}

		outFileName := filepath.Join(outFolder, nodeName+".cfg")
		err = os.WriteFile(outFileName, []byte(cfg), 0644)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("%v config is saved as %v", nodeName, outFileName)

	}
}

func (cli *CLI) LoadCfg(cmd *cobra.Command, args []string) {
	//TODO: also load lab's YAML
	clnt, err := cli.getClnt()
	if err != nil {
		log.Fatal(err)
	}
	lab := &v1beta1.Lab{}
	labKey := types.NamespacedName{Namespace: cli.Namespace, Name: cli.Config.Load.Lab}
	err = clnt.Get(context.Background(), labKey, lab)
	if err != nil {
		log.Fatal(err)
	}
	folderName := filepath.Join(cli.Config.Load.Input, lab.Name)
	log.Printf("loading config from folder %v...", folderName)
	for nodeName, node := range lab.Spec.NodeList {
		fileName := filepath.Join(folderName, nodeName+".cfg")
		log.Printf("reading from %v", fileName)
		buf, err := os.ReadFile(fileName)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				log.Printf("%v's config file not found, skip", nodeName)
				continue
			} else {
				log.Fatalf("failed to read config file for %v from file %v, %v", nodeName, fileName, err)
			}
		}
		sys, sysType := node.GetSystem()
		passwd := cli.Config.Passwd
		if cli.Config.Passwd == "" {
			switch sysType {
			case "VSIM", "VSRI", "SRSIM", "MAGC":
				passwd = "admin"
			case "SRL":
				passwd = "NokiaSrl1!"

			}
		}
		support, err := sys.LoadCfg(context.Background(), clnt, cli.Namespace, cli.Config.Load.Lab, nodeName, cli.Config.User, passwd, string(buf))
		if !support {
			log.Printf("%v doesn't support load config, skip", nodeName)
			continue
		}
		if err != nil {
			log.Fatalf("failed to load config for %v, %v", nodeName, err)
		}
		log.Printf("loaded config for %v", nodeName)

	}
}
