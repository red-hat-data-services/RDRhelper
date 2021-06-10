package main

import (
	"context"
	"fmt"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
		err := verifyOMAPpods(cluster)
		if err != nil {
			errormsg := fmt.Sprintf("[%s] ERRORS when verifying OMAP Pods. Please fix before proceeding.", cluster.name)
			log.WithError(err).Warn(errormsg)
			showAlert(errormsg)
			addRowOfTextOutput(verifyText, "Press ENTER to get back to main")
			return err
		}

		err = verifyRBDMirrorPods(cluster)
		if err != nil {
			errormsg := fmt.Sprintf("[%s] ERRORS when verifying RBD Mirror Pods. Please fix before proceeding.", cluster.name)
			log.WithError(err).Warn(errormsg)
			showAlert(errormsg)
			addRowOfTextOutput(verifyText, "Press ENTER to get back to main")
			return err
		}

		err = verifyCBPmirror(cluster)
		if err != nil {
			errormsg := fmt.Sprintf("[%s] ERRORS when verifying Ceph Block Pool Pods. Please fix before proceeding.", cluster.name)
			log.WithError(err).Error(errormsg)
			showAlert(errormsg)
			addRowOfTextOutput(verifyText, "Press ENTER to get back to main")
			return err
		}

		err = verifyOADPOperator(cluster)
		if err != nil {
			warningmsg := fmt.Sprintf("[%s] WARNING when verifying OADP Mirror Pods.", cluster.name)
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
func verifyOMAPpods(cluster kubeAccess) error {
	omapLabelSelector := "app=csi-rbdplugin-provisioner"
	if err := verifyOMAPEnabled(cluster); err != nil {
		return err
	}
	omappods, err := cluster.typedClient.CoreV1().Pods(ocsNamespace).
		List(context.TODO(), metav1.ListOptions{LabelSelector: omapLabelSelector})
	if err != nil || len(omappods.Items) == 0 {
		missingOMAPpodsmsg := fmt.Sprintf("[%s] ERROR: OMAP No pods in %s namespace with label %s",
			cluster.name, ocsNamespace, omapLabelSelector)
		log.WithError(err).Error(missingOMAPpodsmsg)
		addRowOfTextOutput(verifyText, missingOMAPpodsmsg)
		return errors.WithMessagef(err,missingOMAPpodsmsg)
	}
	// TODO get replicas directly from deployment
	if len(omappods.Items) != 2 {
		missingOMAPDeploymsg := fmt.Sprintf("[%s] ERROR: There should be 2 pods from deployment/%s",
			cluster.name, omapLabelSelector)
		log.Errorf(missingOMAPDeploymsg)
		showAlert(missingOMAPDeploymsg)
		return errors.New(missingOMAPDeploymsg)
	}
	for _, pod := range omappods.Items {
		for _, container := range pod.Status.ContainerStatuses {
			if container.Ready != true {
				notreadyOMAPpodmsg := fmt.Sprintf("[%s] ERROR: Checking OMAP Pod: [%s] container: %s status: %t",
					cluster.name, omapLabelSelector, container.Name, container.Ready)
				log.Errorf(notreadyOMAPpodmsg)
				showAlert(notreadyOMAPpodmsg)
				return errors.WithMessagef(err,notreadyOMAPpodmsg)
			}
		}
	}
	addRowOfTextOutput(verifyText, "[%s] Setup for mirrored relationship OK", cluster.name)
    return nil
}

// oc get pods -l 'app=rook-ceph-rbd-mirror' -n openshift-storage
func verifyRBDMirrorPods(cluster kubeAccess) error {
	rbdLabelSelector := "app=rook-ceph-rbd-mirror"
	rbdmirrorpods, err := cluster.typedClient.CoreV1().Pods(ocsNamespace).
		List(context.TODO(), metav1.ListOptions{LabelSelector: rbdLabelSelector})

	if err != nil || len(rbdmirrorpods.Items) == 0 {
		missingRBDpodmsg := fmt.Sprintf("[%s] ERROR: RBD Mirror No pods in %s namespace with label %s",
			cluster.name, ocsNamespace, rbdLabelSelector)
		log.WithError(err).Error(missingRBDpodmsg)
		addRowOfTextOutput(verifyText, missingRBDpodmsg)
		return errors.WithMessagef(err,missingRBDpodmsg)
	}
	for _, pod := range rbdmirrorpods.Items {
		for _, container := range pod.Status.ContainerStatuses {
			if container.Ready != true {
				notreadyRBDpodmsg := fmt.Sprintf("[%s] ERROR: Checking RBD Mirror Pod: [%s] container: %s status: %t",
					cluster.name, rbdLabelSelector, container.Name, container.Ready)
				log.Errorf(notreadyRBDpodmsg)
				showAlert(notreadyRBDpodmsg)
				return errors.WithMessagef(err,notreadyRBDpodmsg)
			}
		}
	}
	addRowOfTextOutput(verifyText, "[%s] RBD Mirror Pods OK", cluster.name)
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
	//var blockPool cephv1.CephBlockPool
	var cbpList cephv1.CephBlockPoolList
	err := cluster.controllerClient.List(context.TODO(),
		&cbpList, &client.ListOptions{Namespace: ocsNamespace})
	if err != nil {
		return errors.WithMessagef(err, "[%s] Issues when listing CephBlockPools", cluster.name)
	}
//	for _, cbp := range cbpList.Items {
//		addRowOfTextOutput(verifyText,"cbp name %s and blockpool %s",cbp.Name,blockPool.Name)
//	}
	blockpool := "ocs-storagecluster-cephblockpool"
	currentBlockPool := cephv1.CephBlockPool{}

	err = cluster.controllerClient.Get(context.TODO(),
		types.NamespacedName{Name: blockpool, Namespace: ocsNamespace},
		&currentBlockPool)
	if err != nil {
		fetchErrorCBPmsg := fmt.Sprintf("[%s] FETCHING ERRORS when fetching current CephBlockPool. Please fix before proceeding.", cluster.name)
		log.WithError(err).Error(fetchErrorCBPmsg)
		showAlert(fetchErrorCBPmsg)
		return errors.WithMessagef(err, fetchErrorCBPmsg)
	}
	//blockpool := currentBlockPool.GetName()
	//addRowOfTextOutput(verifyText,"BPOOL %s", blockpool)

	err = cluster.controllerClient.Get(context.TODO(),
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
