package main

import (
	"bytes"
	"context"
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

func getCephStatus(cluster kubeAccess) (string, error) {
	toolBoxPod, err := getToolsPod(cluster)
	if err != nil {
		return "", err
	}
	log.WithField("podname", toolBoxPod.Name).Info("Tools Pod found")
	stdout, stderr, err := executeInPod(cluster, &toolBoxPod, "ceph -s")
	if err != nil {
		return "", err
	}
	log.WithField("stdout", stdout).WithField("stderr", stderr).Infof("EXECUTE!")
	return stdout, nil
}

func executeInToolbox(cluster kubeAccess, command string) (string, string, error) {
	toolBoxPod, err := getToolsPod(cluster)
	if err != nil {
		return "", "", err
	}
	log.WithField("podname", toolBoxPod.Name).Info("Tools Pod found")

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
