package main

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	runtimeyaml "k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"kubenetlab.net/knl/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
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
	lab.Namespace = cli.Namespace
	lab.Name = cli.Config.Load.Lab
	labKey := types.NamespacedName{Namespace: cli.Namespace, Name: cli.Config.Load.Lab}
	folderName := filepath.Join(cli.Config.Load.Input, lab.Name)
	if cli.Config.Load.ReCreateLab {
		labFile := filepath.Join(folderName, "lab.yaml")
		buf, err := os.ReadFile(labFile)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("deleting lab %v", lab.Name)
		_ = clnt.Delete(cmd.Context(), lab)
		wctx, wcancel := context.WithTimeout(cmd.Context(), cli.Config.Load.Timeout)
		defer wcancel()
		err = wait.PollUntilContextCancel(wctx, 2*time.Second, true, func(ctx context.Context) (bool, error) {
			err := clnt.Get(ctx, labKey, lab)
			if k8serr.IsNotFound(err) {
				return true, nil // lab is gone, we can proceed
			}
			if err != nil {
				return false, err
			}

			return false, nil // lab still exists, keep polling
		})

		if err != nil {
			log.Printf("Error or timeout: %v", err)
		}
		obj := &unstructured.Unstructured{}
		dec := runtimeyaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
		_, _, err = dec.Decode(buf, nil, obj)
		if err != nil {
			log.Fatalf("failed to decode YAML: %v", err)
		}

		log.Printf("create lab %v", lab.Name)
		err = clnt.Create(cmd.Context(), obj)
		if err != nil {
			log.Fatalf("failed to create lab %v, %v", lab.Name, err)
		}
		log.Printf("waiting for lab %v to become ready...", lab.Name)
		ctx, cancel := context.WithTimeout(cmd.Context(), cli.Config.Load.Timeout)
		defer cancel()
		err = wait.PollUntilContextCancel(ctx, 2*time.Second, true, func(ctx context.Context) (bool, error) {
			err := clnt.Get(ctx, labKey, lab)
			if err != nil {
				return false, nil
			}

			for _, cond := range lab.Status.Conditions {
				if cond.Type == v1beta1.ReadyCondition && cond.Status == metav1.ConditionTrue {
					return true, nil
				}
			}

			return false, nil
		})

		if err != nil {
			log.Printf("Error or timeout: %v", err)
		} else {
			log.Print("lab is Ready!")
		}

	}

	err = clnt.Get(cmd.Context(), labKey, lab)
	if err != nil {
		log.Fatal(err)
	}
	const (
		retryInterval = 5 * time.Second
	)
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

		var support bool
		t0 := time.Now()
		for {
			support, err = sys.LoadCfg(cmd.Context(), clnt, cli.Namespace, cli.Config.Load.Lab, nodeName, cli.Config.User, passwd, string(buf))
			if !support {
				log.Printf("%v doesn't support load config, skip", nodeName)
				continue
			}
			if err != nil {
				log.Printf("failed to load config for %v: %v", nodeName, err)
				if strings.Contains(err.Error(), "rpc error:") {
					log.Fatal("rpc error, abort")
				}
				if time.Since(t0) > cli.Config.Load.Timeout {
					log.Fatal("timeout")
				}
				log.Print("retry...")
				time.Sleep(retryInterval)
			} else {
				log.Printf("loaded config for %v", nodeName)
				break
			}
		}
	}
}
