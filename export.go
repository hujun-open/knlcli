package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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
)

func (cli *CLI) ExportDiskNode(cmd *cobra.Command, args []string) {
	if cli.ExportDisk.Labdef == "" {
		log.Fatal("lab yaml file not specified")
	}
	if cli.ExportDisk.Node == "" {
		log.Fatal("node name not specified")
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

	output := cli.ExportDisk.Output
	if output == "" {
		output = cli.ExportDisk.Node + ".qcow2"
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
	pod := newExportPod(podName, ns, pvcName, image)
	if err := clnt.Create(ctx, pod); err != nil {
		log.Fatalf("create export helper pod: %v", err)
	}
	defer func() {
		if err := clnt.Delete(context.Background(), pod); err != nil && !apierrors.IsNotFound(err) {
			log.Printf("warning: delete export helper pod %s: %v", podName, err)
		}
	}()

	if err := waitForPodRunning(ctx, clnt, ns, podName, exportPodWait); err != nil {
		log.Fatal(err)
	}

	if err := streamDiskAsQCOW2(ctx, ns, podName, output); err != nil {
		log.Fatal(err)
	}

	info, err := os.Stat(output)
	if err != nil {
		log.Fatalf("verify output file: %v", err)
	}
	if info.Size() == 0 {
		log.Fatalf("output file %q is empty", output)
	}

	log.Printf("exported %s disk to %s (%d bytes)", cli.ExportDisk.Node, output, info.Size())
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

func newExportPod(name, ns, pvcName, image string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				exportPodLabelKey: exportPodLabelValue,
			},
		},
		Spec: corev1.PodSpec{
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

func streamDiskAsQCOW2(ctx context.Context, ns, podName, outputPath string) error {
	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		return fmt.Errorf("kubectl not found in PATH: %w", err)
	}

	srcFormat, err := detectDiskFormat(ctx, kubectlPath, ns, podName)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil && filepath.Dir(outputPath) != "." {
		return fmt.Errorf("create output directory: %w", err)
	}

	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()

	args := []string{"exec", "-n", ns, podName, "--", "qemu-img", "convert", "-O", "qcow2"}
	if srcFormat != "" {
		args = append(args, "-f", srcFormat)
	}
	args = append(args, "/pvc/disk.img", "/dev/stdout")

	cmd := exec.CommandContext(ctx, kubectlPath, args...)
	cmd.Stdout = outFile
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("setup stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start qemu-img convert: %w", err)
	}

	errOut, readErr := io.ReadAll(stderr)
	waitErr := cmd.Wait()
	if readErr != nil {
		return readErr
	}
	if waitErr != nil {
		msg := strings.TrimSpace(string(errOut))
		if msg != "" {
			return fmt.Errorf("qemu-img convert failed: %w: %s", waitErr, msg)
		}
		return fmt.Errorf("qemu-img convert failed: %w", waitErr)
	}
	return nil
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
