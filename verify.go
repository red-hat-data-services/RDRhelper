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

func init() {
	verifyText.
		SetDoneFunc(func(key tcell.Key) {
			verifyText.Clear()
			pages.RemovePage("verify")
			pages.SwitchToPage("main")
		})
}

func showVerifyPage(kubeConfigPrimary, kubeConfigSecondary kubeAccess) error {
	pages.AddAndSwitchToPage("verify", verifyText, true)
	clusters := []kubeAccess{ kubeConfigPrimary, kubeConfigSecondary }
	for _, cluster := range clusters {
		err := verifyRBDMirrorPods(cluster)
		if err != nil {
			errormsg := fmt.Sprintf("[%s] ERRORS when verifying RBD Mirror Pods. Please fix before proceeding.", cluster.name)
			log.WithError(err).Warn(errormsg)
			showAlert(errormsg)
			addRowOfTextOutput(verifyText, "Press ENTER to get back to main")
			return err
		}

		err = verifyCBPmirror(cluster)
		if err != nil {
			errormsg := fmt.Sprintf("[%s] ERRORS when verifying RBD Mirror Pods. Please fix before proceeding.", cluster.name)
			log.WithError(err).Error(errormsg)
			showAlert(errormsg)
			addRowOfTextOutput(verifyText, "Press ENTER to get back to main")
			return err
		}

		err = verifyOADPOperator(cluster)
		if err != nil {
			warningmsg := fmt.Sprintf("[%s] WARNING when verifying OADP Mirror Pods.", cluster.name)
			log.WithError(err).Warn("Warning when verifying OADP Mirror Pods.")
			addRowOfTextOutput(verifyText, warningmsg)
		}

	}
	addRowOfTextOutput(verifyText, "Press ENTER to get back to main")
	return nil
}

// Check OMAP configmap was enabled/patched "configmap/rook-ceph-operator-config patched"
func verifyOMAPEnabled (cluster kubeAccess) error {
	rbdcmrookceph := "rook-ceph-operator-config"
	rbdcm, err := cluster.typedClient.CoreV1().ConfigMaps(ocsNamespace).Get(context.TODO(),
		rbdcmrookceph, metav1.GetOptions{})

	if err != nil {
		return fmt.Errorf("[%s] ERROR: Cannot get ConfigMap: %s",cluster.name,rbdcmrookceph)
	}

	OMAPEnabled := fmt.Sprintf("[%s] CSI_ENABLE_OMAP_GENERATOR enabled? %+v",cluster.name,rbdcm.Data["CSI_ENABLE_OMAP_GENERATOR"])
	if rbdcm.Data["CSI_ENABLE_OMAP_GENERATOR"] != "true" {
		return fmt.Errorf("[%s] Please enable the OMAP Generator before proceeding",cluster.name)
	}
	addRowOfTextOutput(verifyText,OMAPEnabled)
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
		addRowOfTextOutput(verifyText, missingRBDPodsmsg)
		return errors.WithMessagef(err,missingRBDPodsmsg)
	}
	// TODO get replicas directly from deployment
	if len(rbdmirrorpods.Items) != 2 {
		missingRBDDeploymsg := fmt.Sprintf("[%s] ERROR: There should be 2 pods from deployment/%s",
			cluster.name, rbdlabelselector)
		log.Errorf(missingRBDDeploymsg)
		showAlert(missingRBDDeploymsg)
		return errors.New(missingRBDDeploymsg)
	}
	for _, pod := range rbdmirrorpods.Items {
		for _, container := range pod.Status.ContainerStatuses {
			if container.Ready != true {
				notreadyRBDPodmsg := fmt.Sprintf("[%s] ERROR: Checking RBD Mirror Pod: [%s] container: %s status: %t",
					cluster.name, rbdlabelselector, container.Name, container.Ready)
				log.Errorf(notreadyRBDPodmsg)
				showAlert(notreadyRBDPodmsg)
				return errors.WithMessagef(err,notreadyRBDPodmsg)
			}
		}
	}
	addRowOfTextOutput(verifyText, "[%s] Setup for mirrored relationship OK", cluster.name)
    return nil
}

// 1.2.2 Verify Ceph block pool has mirroring enabled
// oc get cephblockpools.ceph.rook.io -n openshift-storage -o json | jq '.items[].status.mirroringStatus.summary.summary'
func verifyCBPmirror(cluster kubeAccess) error {
	if err := cephv1.AddToScheme(kubeConfigPrimary.controllerClient.Scheme()); err != nil {
		schemeErrormsg := fmt.Sprintf("[%s] Issues when adding the cephv1 scheme to the primary client",
			cluster.name)
		log.WithError(err).Warn(schemeErrormsg)
		showAlert(schemeErrormsg)
		return errors.WithMessagef(err,schemeErrormsg)
	}
	// TODO discover and cycle through the different blockpools
	blockpool := "ocs-storagecluster-cephblockpool"
	currentBlockPool := cephv1.CephBlockPool{}
	err := cluster.controllerClient.Get(context.TODO(),
		types.NamespacedName{Name: blockpool, Namespace: ocsNamespace},
		&currentBlockPool)
	if err != nil {
		fetchErrorCBPmsg := fmt.Sprintf("[%s] ERRORS when fetching current CephBlockPool. Please fix before proceeding.", cluster.name)
		log.WithError(err).Error(fetchErrorCBPmsg)
		showAlert(fetchErrorCBPmsg)
		return errors.WithMessagef(err, fetchErrorCBPmsg)
	}
	for cbpkey, cbpstatus := range currentBlockPool.Status.MirroringStatus.Summary["summary"].(map[string]interface{}) {
		if cbpkey != "states" {
			if cbpstatus != "OK" {
				statusCBPnotready := fmt.Sprintf("[%s] CephBlockPool Mirror Status %s is %s. Please investigate.",
					cluster.name, cbpkey, cbpstatus)
				log.Error(statusCBPnotready)
				showAlert(statusCBPnotready)
				return errors.WithMessagef(err, "[%s] ERROR CephBlockPool mirror summary status %s %s",
					cluster.name, cbpkey, cbpstatus)
			}
		}
	}
	addRowOfTextOutput(verifyText,"[%s] CephBlockPool mirror summary status OK", cluster.name)
	return nil
}
// Check and warn if OADP is not installed (but it's optional)
func verifyOADPOperator(cluster kubeAccess) error {
	oadppod, err := cluster.typedClient.CoreV1().Pods("oadp-operator").
		List(context.TODO(), metav1.ListOptions{})
	if err != nil || len(oadppod.Items) == 0 {
		warningOADPmsg := fmt.Sprintf("[%s] WARNING: No OADP. Please consider installing OADP",cluster.name)
		addRowOfTextOutput(verifyText,warningOADPmsg)
	} else {
		for _, mypod := range oadppod.Items {
			podstatusCondition := mypod.Status.Conditions[1]
			if podstatusCondition.Status == "False" {
				errormsg := fmt.Sprintf("[%s] ERROR: OADP Operator Check %s container ready? %s", cluster.name, mypod.Name, podstatusCondition.Status)
				log.Error(errormsg)
				showAlert(errormsg)
				return errors.New(errormsg)
			}
		}
		addRowOfTextOutput(verifyText, "[%s] OADP Operator status OK", cluster.name)
	}
	return nil
}
