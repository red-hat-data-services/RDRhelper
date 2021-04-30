package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/rivo/tview"
	"github.com/tidwall/sjson"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func executeInPod(cluster kubeAccess, pod *corev1.Pod, command string) (stdout string, stderr string, err error) {
	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}
	// actualCommand := []string{"/bin/sh", "-c", "'", command, "'"}
	actualCommand := strings.Split(command, " ")
	request := cluster.typedClient.CoreV1().RESTClient().
		Post().
		Namespace(pod.Namespace).
		Resource("pods").
		Name(pod.Name).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: actualCommand,
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
			// TTY:     true,
		}, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(&cluster.restConfig, "POST", request.URL())
	if err != nil {
		return "", "", errors.Wrapf(err, "Could not upgrade connection to %s/%s", strings.Join(actualCommand, " "), pod.Namespace, pod.Name)
	}
	err = exec.Stream(remotecommand.StreamOptions{
		Stdout: stdoutBuf,
		Stderr: stderrBuf,
	})
	stdout = stdoutBuf.String()
	stderr = stderrBuf.String()
	if err != nil {
		log.WithError(err).WithField("stdout", stdout).WithField("stderr", stderr).Error("PROBLEM")
		return stdout, stderr, errors.Wrapf(err, "Failed executing command '%s' on %s/%s", strings.Join(actualCommand, " "), pod.Namespace, pod.Name)
	}

	return stdout, stderr, nil
}

func executeInToolbox(cluster kubeAccess, command string) (string, string, error) {
	toolBoxPod, err := getToolsPod(cluster)
	if err != nil {
		return "", "", err
	}
	log.WithField("podname", toolBoxPod.Name).Debug("Pod found")

	stdout, stderr, err := executeInPod(cluster, &toolBoxPod, command)
	if err != nil {
		return stdout, stderr, err
	}
	if stderr != "" {
		log.WithField("command", command).WithField("stderr", stderr).Warn("Command executed with error")
		return stdout, stderr, errors.New("Command executed with error")
	}
	log.WithField("stdout", stdout).WithField("stderr", stderr).Trace("EXECUTE!")
	return stdout, stderr, nil
}

func getToolsPod(cluster kubeAccess) (corev1.Pod, error) {
	list, err := cluster.typedClient.CoreV1().Pods(ocsNamespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=rook-ceph-tools"})
	if err != nil || len(list.Items) == 0 {
		errors.Wrapf(err, "error when looking for tools pod in %s namespace", ocsNamespace)
		return corev1.Pod{}, err
	}
	if len(list.Items) > 1 {
		return corev1.Pod{}, errors.New("more than one tools pod found")
	}
	return list.Items[0], nil

}

func getNetworkCheckPods(cluster kubeAccess) (*corev1.PodList, error) {
	list, err := cluster.typedClient.CoreV1().Pods("openshift-network-diagnostics").List(context.TODO(), metav1.ListOptions{LabelSelector: "app=network-check-target"})
	if err != nil || len(list.Items) == 0 {
		errors.Wrapf(err, "could not find network-check pod in %s namespace", "openshift-network-diagnostics")
		return &corev1.PodList{}, err
	}
	return list, nil
}

func checkNetworkBetweenClusters(from, to kubeAccess) error {
	networkCheckPodsSource, err := getNetworkCheckPods(from)
	if err != nil {
		return err
	}
	networkCheckPodsTarget, err := getNetworkCheckPods(to)
	if err != nil {
		return err
	}

	var ips []string
	for _, targetPod := range networkCheckPodsTarget.Items {
		if targetPod.Status.PodIP == "" {
			continue
		}
		ips = append(ips, targetPod.Status.PodIP)
	}
	if len(ips) == 0 {
		return errors.Errorf("Could not find any IPs to connect to in the %s cluster", to.name)
	}

	for _, ip := range ips {
		stdout, stderr, err := executeInPod(from, &networkCheckPodsSource.Items[0], fmt.Sprintf("curl --silent --fail %s:8080", ip))
		if err != nil {
			return err
		}
		if stderr != "" {
			log.WithField("command", fmt.Sprintf("curl %s", ip)).WithField("stderr", stderr).Warn("Command executed with error")
			return errors.Errorf("Command %s executed with error.\nStderr %s\nStdout %s", fmt.Sprintf("curl %s", ip), stderr, stdout)
		}
		log.WithField("stdout", stdout).WithField("stderr", stderr).Trace("EXECUTE!")
	}

	return nil
}

// getRBDInfoFromPV returns (rbdName, PoolName, nil) or ("", "", error)
func getRBDInfoFromPV(pv *corev1.PersistentVolume) (string, string, error) {
	if pv == nil || pv.Spec.CSI == nil || pv.Spec.CSI.VolumeAttributes == nil {
		return "", "", errors.New("PV does not contain the required information")
	}
	rbdName := pv.Spec.CSI.VolumeAttributes["imageName"]
	poolName := pv.Spec.CSI.VolumeAttributes["pool"]
	if rbdName == "" || poolName == "" {
		return "", "", errors.New("could not get RBD or pool name from PV")
	}
	return rbdName, poolName, nil
}

func showMirrorInfo(cluster kubeAccess, pv *corev1.PersistentVolume) error {
	rbdName, poolName, err := getRBDInfoFromPV(pv)
	if err != nil {
		return err
	}
	command := fmt.Sprintf("rbd -p %s mirror image status %s", poolName, rbdName)
	stdout, stderr, err := executeInToolbox(cluster, command)
	// Catch error later, since exit code 22 is thrown when image is not enabled
	if strings.Contains(stderr, "mirroring not enabled on the image") {
		showAlert("mirroring is not enabled on this PVC")
		return errors.New("mirroring is not enabled on this PVC")
	}
	if err != nil {
		showAlert("could not get RBD mirror info from PV")
		return errors.Wrapf(err, "could not get RBD mirror info from PV")
	}
	buttons := make(map[string]func())
	buttons["Close"] = func() { pages.RemovePage("mirrorInfo") }
	showInfo("mirrorInfo", stdout, buttons)
	return nil
}

func demotePV(cluster kubeAccess, pv *corev1.PersistentVolume) error {
	rbdName, poolName, err := getRBDInfoFromPV(pv)
	if err != nil {
		return err
	}
	// rbd -p replicapool mirror image demote csi-vol-94953897-88fc-11eb-b175-0a580a061092
	command := fmt.Sprintf("rbd -p %s mirror image demote %s", poolName, rbdName)
	_, stderr, err := executeInToolbox(cluster, command)
	// Catch error later, since exit code 22 is thrown when image is not enabled
	if strings.Contains(stderr, "mirroring not enabled on the image") {
		showAlert("mirroring is not enabled on this PVC")
		return errors.WithMessagef(err, "mirroring is not enabled on this PV", pv.Name)
	}
	return err
}

func promotePV(cluster kubeAccess, pv *corev1.PersistentVolume) error {
	rbdName, poolName, err := getRBDInfoFromPV(pv)
	if err != nil {
		return err
	}
	command := fmt.Sprintf("rbd -p %s mirror image promote %s", poolName, rbdName)
	_, stderr, err := executeInToolbox(cluster, command)
	// Catch error later, since exit code 22 is thrown when image is not enabled
	if strings.Contains(stderr, "mirroring not enabled on the image") {
		showAlert("mirroring is not enabled on this PVC")
		return errors.WithMessagef(err, "mirroring is not enabled on this PV", pv.Name)
	}
	return err
}

func setNamespacesToBackup(cluster kubeAccess, namespaces []string) {
	if !checkForOADP(cluster) {
		return
	}
	snapshotVolumeSetting := false
	backupCR := velerov1.Backup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "velero.io/v1",
			Kind:       "Backup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "regional-dr-backup",
			Namespace: "oadp-operator",
		},
		Spec: velerov1.BackupSpec{
			IncludedNamespaces: namespaces,
			ExcludedResources:  []string{"imagetags.image.openshift.io"},
			SnapshotVolumes:    &snapshotVolumeSetting,
		},
	}

	if err := velerov1.AddToScheme(cluster.controllerClient.Scheme()); err != nil {
		log.WithError(err).Warn("[%s] Issues when adding velero schemas", cluster.name)
	}

	backupJSON, err := json.Marshal(backupCR)
	if err != nil {
		log.WithError(err).Warn("[%s] Issues when converting Backup CR to JSON")
		showAlert("The OADP Backup plan might not have been updated properly")
	}

	backupPatchedJSON, _ := sjson.Delete(string(backupJSON), "spec.ttl")

	err = cluster.controllerClient.Patch(context.TODO(),
		&backupCR,
		client.RawPatch(types.ApplyPatchType, []byte(backupPatchedJSON)),
		&client.PatchOptions{FieldManager: "RDRhelper"})

	if err != nil {
		log.WithError(err).Warnf("[%s] Issues when applying Backup CR", cluster.name)
		showAlert("The OADP Backup plan might not have been updated properly")
	}
}

func setNamespacesToRestore(cluster kubeAccess, namespaces []string) error {
	if !checkForOADP(cluster) {
		return errors.New("Cluster has no OADP installed")
	}
	restoreCR := velerov1.Restore{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "velero.io/v1",
			Kind:       "Backup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "regional-dr-restore",
			Namespace: "oadp-operator",
		},
		Spec: velerov1.RestoreSpec{
			IncludedNamespaces: namespaces,
			BackupName:         "regional-dr-backup",
		},
	}

	if err := velerov1.AddToScheme(cluster.controllerClient.Scheme()); err != nil {
		return errors.WithMessagef(err, "[%s] Issues when adding velero schemas", cluster.name)
	}

	backupJSON, err := json.Marshal(restoreCR)
	if err != nil {
		return errors.WithMessagef(err, "[%s] Issues when converting Restore CR to JSON")
	}

	backupPatchedJSON, _ := sjson.Delete(string(backupJSON), "spec.ttl")

	err = cluster.controllerClient.Patch(context.TODO(),
		&restoreCR,
		client.RawPatch(types.ApplyPatchType, []byte(backupPatchedJSON)),
		&client.PatchOptions{FieldManager: "RDRhelper"})

	if err != nil {
		return errors.WithMessagef(err, "[%s] Issues when applying Restore CR", cluster.name)
	}
	return nil
}

func waitForRecoveryDone(cluster kubeAccess, failoverLog *tview.TextView) error {
	if !checkForOADP(cluster) {
		return errors.New("Cluster has no OADP installed")
	}
	var restoreCR velerov1.Restore
	for {
		err := cluster.controllerClient.Get(context.TODO(), types.NamespacedName{Name: "regional-dr-restore", Namespace: "oadp-operator"}, &restoreCR)
		if err != nil {
			addRowOfTextOutput(failoverLog, "  Error while fetching Retore CR: %s", err)
		}
		if restoreCR.Status.Phase == velerov1.RestorePhaseCompleted {
			return nil
		}
		addRowOfTextOutput(failoverLog, "  The restore status is %s", restoreCR.Status.Phase)
		time.Sleep(5 * time.Second)
	}
}
