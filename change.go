package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/scheme"
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
