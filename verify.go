package main

import (
	"context"
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/pkg/errors"
	"github.com/rivo/tview"

	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/types"
)

var verifyText = tview.NewTextView().
	SetChangedFunc(func() {
		app.Draw()
	})

func addRowOfVerifyTextOutput(target *tview.TextView, format string, a ...interface{}) {
	newText := fmt.Sprintf(format, a...)
	log.Info(newText)
	if _, err := fmt.Fprintln(target, newText); err != nil {
		log.WithError(err).Error("Error when writing to TextField")
	}
}

func init() {
	verifyText.
		SetDoneFunc(func(key tcell.Key) {
			verifyText.Clear()
			pages.RemovePage("verify")
			pages.SwitchToPage("main")
		})
}

func showVerifyPage(kubeConfigPrimary, kubeConfigSecondary kubeAccess) {
	pages.AddAndSwitchToPage("verify", verifyText, true)

	clusters := []kubeAccess{ kubeConfigPrimary, kubeConfigSecondary }
	for _, cluster := range clusters {

		err := verifyRBDMirrorPods(cluster)
		if err != nil {
			log.WithError(err).Warn("Issues when verifying RBD Mirror Pods")
		}

		err = verifyCBPmirror(cluster)
		if err != nil {
			log.WithError(err).Error("Issues when verifying Ceph Block Pool Mirror Pods")
		}

		err = verifyOADPOperator(cluster)
		if err != nil {
			log.WithError(err).Warn("Issues when verifying OADP Operator")
		}

	}
	addRowOfVerifyTextOutput(verifyText, "Press ENTER to get back to main")
}

// Check OMAP configmap was enabled/patched "configmap/rook-ceph-operator-config patched"
func verifyOMAPEnabled (cluster kubeAccess) error {
	rbdcmrookceph := "rook-ceph-operator-config"
	rbdcm, err := cluster.typedClient.CoreV1().ConfigMaps(ocsNamespace).Get(context.TODO(),
		rbdcmrookceph, metav1.GetOptions{})

	if err != nil {
		missingCMmsg := "[" + cluster.name + "] ERROR: Cannot get ConfigMap: " + rbdcmrookceph
		addRowOfVerifyTextOutput(verifyText, missingCMmsg)
		return errors.New(missingCMmsg)
	}

	OMAPEnabled := fmt.Sprintf("[%s] CSI_ENABLE_OMAP_GENERATOR enabled? %s",cluster.name,rbdcm.Data["CSI_ENABLE_OMAP_GENERATOR"])
	if rbdcm.Data["CSI_ENABLE_OMAP_GENERATOR"] != "true" {
		enableOMAPcmd := `oc patch cm ` + rbdcmrookceph +
			` -n openshift-storage --type json --patch  '[{ "op": "add", "path": "/data/CSI_ENABLE_OMAP_GENERATOR", "value": "true" }]'`
		enableOMAPmsg := fmt.Sprintf("[%s] Please enable the OMAP Generator before proceeding. For example: %s",cluster.name, enableOMAPcmd)

		addRowOfVerifyTextOutput(verifyText, OMAPEnabled)
		addRowOfVerifyTextOutput(verifyText, enableOMAPmsg)
		log.WithError(err).Error(OMAPEnabled)
		log.WithError(err).Error(enableOMAPmsg)
		return err
	}
	addRowOfVerifyTextOutput(verifyText,OMAPEnabled)
	return nil
}

// 1.2.2. Configuring RBD Mirroring between ODF clusters
// oc -n openshift-storage get pods -l app=csi-rbd-plugin-provisioner
func verifyRBDMirrorPods(cluster kubeAccess) error {
	rbdlabelselector := "app=csi-rbdplugin-provisioner"

	if err := verifyOMAPEnabled(cluster); err != nil {
		return err
	}

	rbdmirrorpods, err := cluster.typedClient.CoreV1().Pods(ocsNamespace).
		List(context.TODO(), metav1.ListOptions{LabelSelector: rbdlabelselector})

	if err != nil || len(rbdmirrorpods.Items) == 0 {
		missingRBDPodsmsg := fmt.Sprintf("[%s] ERROR: RBD Mirror No pods in %s namespace with label %s",
			cluster.name, ocsNamespace, rbdlabelselector)
		log.WithError(err).Error(missingRBDPodsmsg)
		addRowOfVerifyTextOutput(verifyText, missingRBDPodsmsg)
		return err
	}

	// TODO get replicas directly from deployment
	if len(rbdmirrorpods.Items) != 2 {
		missingRBDDeploymsg := "[" + cluster.name + "] ERROR: There should be 2 pods from deployment/" + rbdlabelselector
		return errors.New(missingRBDDeploymsg)
	}

	for _, pod := range rbdmirrorpods.Items {
		for _, container := range pod.Status.ContainerStatuses {
			if container.Ready != true {
				addRowOfVerifyTextOutput(verifyText, "[%s] ERROR: Checking RBD mirror setup %s container %s Ready state? %t",
					cluster.name, rbdlabelselector, container.Name, container.Ready)
				addRowOfVerifyTextOutput(verifyText,"[%s] Please investigate. i.e. `oc -n %s get pods -l %s`",
					 cluster.name, ocsNamespace, rbdlabelselector)
				return errors.WithMessagef(err, "[%s] ERROR: Checking RBD mirrorr setup status %s container %s Ready state? %t",
					cluster.name, rbdlabelselector, container.Name, container.Ready)
			}
		}
	}
	addRowOfVerifyTextOutput(verifyText, "[%s] Setup for mirrored relationship OK", cluster.name)
    return nil
}

// 1.2.2 Verify Ceph block pool has mirroring enabled
// oc get cephblockpools.ceph.rook.io -n openshift-storage -o json | jq '.items[].status.mirroringStatus.summary.summary'
func verifyCBPmirror(cluster kubeAccess) error {
	if err := cephv1.AddToScheme(kubeConfigPrimary.controllerClient.Scheme()); err != nil {
		log.WithError(err).Warn("Issues when adding the cephv1 scheme to the primary client")
	}

	blockpool := "ocs-storagecluster-cephblockpool"
	currentBlockPool := cephv1.CephBlockPool{}
	err := cluster.controllerClient.Get(context.TODO(),
		types.NamespacedName{Name: blockpool, Namespace: ocsNamespace},
		&currentBlockPool)

	if err != nil {
		return errors.WithMessagef(err, "[%s] Issues when fetching current CephBlockPool", cluster.name)
	}

	for cbpkey, cbpstatus := range currentBlockPool.Status.MirroringStatus.Summary["summary"].(map[string]interface{}) {
		if cbpkey != "states" {
			if cbpstatus != "OK" {
				addRowOfVerifyTextOutput(verifyText,"[%s] CephBlockPool Mirror Status %s is %s. Please investigate.", cluster.name, cbpkey, cbpstatus)
				return errors.WithMessagef(err, "[%s] ERROR CephBlockPool mirror summary status %s %s",
					cluster.name, cbpkey, cbpstatus)
			}
		}
	}
	addRowOfVerifyTextOutput(verifyText,"[%s] CephBlockPool mirror summary status OK", cluster.name)
	return nil
}
// Check and warn if OADP is not installed (but it's optional)
func verifyOADPOperator(cluster kubeAccess) error {
	oadppod, err := cluster.typedClient.CoreV1().Pods("oadp-operator").
		List(context.TODO(), metav1.ListOptions{})

	if err != nil || len(oadppod.Items) == 0 {
		addRowOfVerifyTextOutput(verifyText,"[%s] WARNING: OADP does NOT appear to be installed and running. " +
			"Please consider installing OADP.",
			cluster.name)
		return err
	}

	noerrors := true
	for _, mypod := range oadppod.Items {
		podstatusCondition := mypod.Status.Conditions[1]
		if podstatusCondition.Status == "False" {
			addRowOfVerifyTextOutput(verifyText, "[%s] ERROR: OADP Operator Check %s container ready? %s", cluster.name, mypod.Name, podstatusCondition.Status)
			addRowOfVerifyTextOutput(verifyText,"[%s] ERROR: Please investigate. i.e. `oc -n oadp-operator get pods`", cluster.name)
			noerrors = false
		}
	}
	if noerrors {
		addRowOfVerifyTextOutput(verifyText, "[%s] OADP Operator status OK", cluster.name)
	}
	return nil
}
