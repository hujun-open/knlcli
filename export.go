package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"kubenetlab.net/knl/api/v1beta1"
	kvv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	exportPodLabelKey   = "app.kubernetes.io/name"
	exportPodLabelValue = "knlcli-export"
	exportPodWait       = 5 * time.Minute
	exportOutMountPath  = "/out"
)

func (cli *CLI) ExportDiskNode(cmd *cobra.Command, args []string) {
	if cli.ExportDisk.Labdef == "" {
		log.Fatal("lab yaml file not specified")
	}
	if cli.ExportDisk.Node == "" {
		log.Fatal("node name not specified")
	}
	if cli.ExportDisk.Worker == "" {
		log.Fatal("--worker (Kubernetes node name) is required")
	}
	if cli.ExportDisk.HostDir == "" {
		log.Fatal("--host-dir (absolute directory on the worker) is required")
	}
	if !filepath.IsAbs(cli.ExportDisk.HostDir) {
		log.Fatalf("--host-dir must be an absolute path, got %q", cli.ExportDisk.HostDir)
	}

	lab, err := parseLabYAML(cli.ExportDisk.Labdef)
	if err != nil {
		log.Fatal(err)
	}

	node, ok := lab.Spec.NodeList[cli.ExportDisk.Node]
	if !ok {
		log.Fatalf("node %q is not specified in lab %q", cli.ExportDisk.Node, lab.Name)
	}
	_, sysType := node.GetSystem()
	if sysType != "VM" {
		log.Fatalf("node %q is type %q, export-disk only supports General VM nodes", cli.ExportDisk.Node, sysType)
	}

	ns := lab.Namespace
	if ns == "" {
		ns = cli.Namespace
	}

	labName := lab.Name
	vmiName := v1beta1.GetPodName(labName, cli.ExportDisk.Node)
	pvcName := v1beta1.GetVMPCDVName(labName, cli.ExportDisk.Node)

	outputName := cli.ExportDisk.Output
	if outputName == "" {
		outputName = cli.ExportDisk.Node + ".qcow2"
	}
	outputName = filepath.Base(outputName)
	if outputName == "." || outputName == string(filepath.Separator) {
		log.Fatal("invalid -o / --output filename")
	}

	image := cli.ExportDisk.Image
	if image == "" {
		image = defaultExportHelperImage
	}

	clnt, err := cli.getClnt()
	if err != nil {
		log.Fatal(err)
	}
	ctx := cmd.Context()

	if err := ensureVMIAbsent(ctx, clnt, ns, vmiName); err != nil {
		log.Fatal(err)
	}
	if err := ensurePVCExists(ctx, clnt, ns, pvcName); err != nil {
		log.Fatal(err)
	}

	podName := fmt.Sprintf("knlcli-export-%s", rand.String(8))
	hostPath := filepath.Join(cli.ExportDisk.HostDir, outputName)
	log.Printf("creating export helper pod %s on worker %s", podName, cli.ExportDisk.Worker)
	pod := newExportPod(podName, ns, pvcName, image, cli.ExportDisk.Worker, cli.ExportDisk.HostDir)
	if err := clnt.Create(ctx, pod); err != nil {
		log.Fatalf("create export helper pod: %v", err)
	}
	defer func() {
		if err := clnt.Delete(context.Background(), pod); err != nil && !apierrors.IsNotFound(err) {
			log.Printf("warning: delete export helper pod %s: %v", podName, err)
		}
	}()

	log.Printf("waiting for export helper pod %s to run", podName)
	if err := waitForPodRunning(ctx, clnt, ns, podName, exportPodWait); err != nil {
		log.Fatal(err)
	}

	log.Printf("converting disk to %s:%s (progress from qemu-img -p)", cli.ExportDisk.Worker, hostPath)
	size, err := convertDiskToHostPath(ctx, ns, podName, outputName)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("exported %s disk to %s:%s (%d bytes)", cli.ExportDisk.Node, cli.ExportDisk.Worker, hostPath, size)
}

func parseLabYAML(path string) (*v1beta1.Lab, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lab, err := decodeLab(buf)
	if err != nil {
		return nil, err
	}
	if lab.Name == "" {
		return nil, fmt.Errorf("lab name is empty in %s", path)
	}
	return lab, nil
}

func ensureVMIAbsent(ctx context.Context, clnt client.Client, ns, vmiName string) error {
	vmi := &kvv1.VirtualMachineInstance{}
	err := clnt.Get(ctx, types.NamespacedName{Namespace: ns, Name: vmiName}, vmi)
	if err == nil {
		return fmt.Errorf("VMI %q exists in namespace %q; remove the lab or VMI before exporting disk", vmiName, ns)
	}
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func ensurePVCExists(ctx context.Context, clnt client.Client, ns, pvcName string) error {
	pvc := &corev1.PersistentVolumeClaim{}
	err := clnt.Get(ctx, types.NamespacedName{Namespace: ns, Name: pvcName}, pvc)
	if err == nil {
		return nil
	}
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("PVC %q not found in namespace %q", pvcName, ns)
	}
	return err
}

func newExportPod(name, ns, pvcName, image, worker, hostDir string) *corev1.Pod {
	hostPathType := corev1.HostPathDirectoryOrCreate
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				exportPodLabelKey: exportPodLabelValue,
			},
		},
		Spec: corev1.PodSpec{
			NodeName:      worker,
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "export",
					Image:   image,
					Command: []string{"sleep", "infinity"},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "disk",
							MountPath: "/pvc",
							ReadOnly:  true,
						},
						{
							Name:      "out",
							MountPath: exportOutMountPath,
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "disk",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
							ReadOnly:  true,
						},
					},
				},
				{
					Name: "out",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: hostDir,
							Type: &hostPathType,
						},
					},
				},
			},
		},
	}
}

func waitForPodRunning(ctx context.Context, clnt client.Client, ns, podName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pod := &corev1.Pod{}
		err := clnt.Get(ctx, types.NamespacedName{Namespace: ns, Name: podName}, pod)
		if err != nil {
			return err
		}
		switch pod.Status.Phase {
		case corev1.PodRunning:
			return nil
		case corev1.PodFailed, corev1.PodSucceeded:
			return fmt.Errorf("export helper pod %q entered phase %s", podName, pod.Status.Phase)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("timed out waiting for export helper pod %q to run", podName)
}

func convertDiskToHostPath(ctx context.Context, ns, podName, outputName string) (int64, error) {
	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		return 0, fmt.Errorf("kubectl not found in PATH: %w", err)
	}

	srcFormat, err := detectDiskFormat(ctx, kubectlPath, ns, podName)
	if err != nil {
		return 0, err
	}

	outPodPath := path.Join(exportOutMountPath, outputName)

	convertArgs := []string{"exec", "-n", ns, podName, "--",
		"qemu-img", "convert", "-p", "-O", "qcow2"}
	if srcFormat != "" {
		convertArgs = append(convertArgs, "-f", srcFormat)
	}
	convertArgs = append(convertArgs, "/pvc/disk.img", outPodPath)

	convertCmd := exec.CommandContext(ctx, kubectlPath, convertArgs...)
	convertCmd.Stdout = os.Stdout
	convertCmd.Stderr = os.Stderr
	if err := convertCmd.Run(); err != nil {
		return 0, fmt.Errorf("qemu-img convert in pod failed: %w", err)
	}

	size, err := statFileInPod(ctx, kubectlPath, ns, podName, outPodPath)
	if err != nil {
		return 0, err
	}
	if size == 0 {
		return 0, fmt.Errorf("output file %q is empty", outPodPath)
	}
	return size, nil
}

func statFileInPod(ctx context.Context, kubectlPath, ns, podName, path string) (int64, error) {
	cmd := exec.CommandContext(ctx, kubectlPath, "exec", "-n", ns, podName, "--",
		"stat", "-c", "%s", path)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("stat output file in pod: %w", err)
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse output file size %q: %w", strings.TrimSpace(string(out)), err)
	}
	return size, nil
}

func detectDiskFormat(ctx context.Context, kubectlPath, ns, podName string) (string, error) {
	cmd := exec.CommandContext(ctx, kubectlPath, "exec", "-n", ns, podName, "--",
		"qemu-img", "info", "--output=json", "/pvc/disk.img")
	out, err := cmd.Output()
	if err != nil {
		// CDI imports are typically raw; fall back when info fails.
		return "raw", nil
	}
	var info struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(out, &info); err != nil || info.Format == "" {
		return "raw", nil
	}
	return info.Format, nil
}
