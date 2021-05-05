package main

//
import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/types"

	//	"k8s.io/apimachinery/pkg/types"

	//	"github.com/operator-framework/api/pkg/lib/version"
	"github.com/pkg/errors"
	"github.com/rivo/tview"
	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"github.com/gdamore/tcell/v2"
)

var verifyText = tview.NewTextView().
	SetChangedFunc(func() {
		app.Draw()
	})

func addRowOfVerifyTextOutput(target *tview.TextView, format string, a ...interface{}) {
	newText := fmt.Sprintf(format, a...)
//	log.Info(newText)
	_, err := fmt.Fprintln(target, newText)
	if err != nil {
		log.WithError(err).Error("Error when writing to TextField")
	}
}

func init() {
		verifyText.
			SetDoneFunc(func(key tcell.Key) {
				verifyText.Clear()
				pages.RemovePage("verify")
				pages.SwitchToPage("main")
			    //log.Out = logFile
			})
	}

func showVerifyPage(kubeConfigPrimary, kubeConfigSecondary kubeAccess) {
	pages.AddAndSwitchToPage("verify", verifyText, true)
	log.Out = verifyText

	clusters := []kubeAccess{ kubeConfigPrimary, kubeConfigSecondary }
	for _, cluster := range clusters {
		addRowOfVerifyTextOutput(verifyText,"")
		addRowOfVerifyTextOutput(verifyText,"%s cluster", cluster.name)

		err := verifyRBDMirrorPods(cluster)
    	if err != nil {
    		log.WithError(err).Warn("Issues when verifying RBD Mirror Pods")
    	}

    	err = verifyCBPmirror(cluster)
    	if err != nil {
    		log.WithError(err).Warn("Issues when verifying Ceph Block Pool Mirror Pods")
    	}

    	err = verifyOADPOperator(cluster)
    	if err != nil {
    		log.WithError(err).Warn("Issues when verifying OADP Operator")
    	}
	}

}

// to be removed? no longer in issue list
func verifyOcsOperator(cluster kubeAccess) (string) {
	ocspod, err := cluster.typedClient.CoreV1().Pods(ocsNamespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=rook-ceph-operator"})
	if err != nil || len(ocspod.Items) == 0 {
		errors.Wrapf(err, "error when looking for ocs operator pod in %s namespace", ocsNamespace)
		log.WithError(err).Warn("OCS operator pods not found")
		addRowOfVerifyTextOutput(verifyText,"OCS NOT installed?")
		return "FAILED no ocs operator found"
	}

	if len(ocspod.Items) > 1 {
		log.WithError(err).Warn("multiple ocs operator pods found")
		return "FAILED multiple ocs operators installed"
	}
	//addRowOfVerifyTextOutput(fmt.Sprintf("OCS operator OK: %s %s",ocspod.Items[0].Name,ocspod.Items[0].Status.Phase))
	return "OCS operator OK"
}

// Check and warn if OADP is not installed (but it's optional)
func verifyOADPOperator(cluster kubeAccess) (error) {
	oadppod, err := cluster.typedClient.CoreV1().Pods("oadp-operator").List(context.TODO(), metav1.ListOptions{})
	// Does len(oadppod.Items need to be 3? (velero, aws default and operator?)
	if err != nil || len(oadppod.Items) == 0 {
		addRowOfVerifyTextOutput(verifyText,"OADP does NOT appear to be installed and running. Please consider installing OADP.")
		return err
	}
	addRowOfVerifyTextOutput(verifyText,"OADP Operator ... OK")
	return nil
}

//Check that the RBDMirror Pods are Running
func verifyRBDMirrorPods(cluster kubeAccess) (error) {
	rbdlabelselector := "app=csi-rbdplugin-provisioner"
	rbdmirrorpods, err := cluster.typedClient.CoreV1().Pods(ocsNamespace).List(context.TODO(), metav1.ListOptions{LabelSelector: rbdlabelselector})
	if err != nil || len(rbdmirrorpods.Items) == 0 {
		errors.Wrapf(err, "error when looking for rbd mirror pods in %s namespace", ocsNamespace)
		addRowOfVerifyTextOutput(verifyText,"RBDMirrorPods NOT OK. No pods in %s namespace with label %s found!", ocsNamespace, rbdlabelselector)
		return err
	}

	if len(rbdmirrorpods.Items) != 2 {
		return errors.New("There should be 2 pods from deployment/csi-rbdplugin-provisioner")
	}

	for rbd := range rbdmirrorpods.Items {
		addRowOfVerifyTextOutput(verifyText,"RBD Mirror Deployment %d of 2",rbd+1)
		for rbdpod := range rbdmirrorpods.Items[rbd].Spec.Containers {
			addRowOfVerifyTextOutput(verifyText,"RBD Mirror Pod %s ... OK",rbdmirrorpods.Items[rbd].Spec.Containers[rbdpod].Name)
		}
    }
    return nil
}

// CephBlockPool mirror status is OK
func verifyCBPmirror(cluster kubeAccess) (error) {
	if err := cephv1.AddToScheme(kubeConfigPrimary.controllerClient.Scheme()); err != nil {
		log.WithError(err).Warn("Issues when adding the cephv1 scheme to the primary client")
	}
	poolname := "ocs-storagecluster-cephblockpool"
	currentBlockPool := cephv1.CephBlockPool{}
	err := cluster.controllerClient.Get(context.TODO(),
		types.NamespacedName{Name: poolname, Namespace: ocsNamespace},
		&currentBlockPool)
	if err != nil {
		return errors.WithMessagef(err, "Issues when fetching current CephBlockPool in %s cluster", cluster.name)
	}
	for k, v := range currentBlockPool.Status.MirroringStatus.Summary["summary"].(map[string]interface{}) {
		if k != "states" {
			if v != "OK" {
				addRowOfVerifyTextOutput(verifyText," CBP pod %s NOT OK. status: %s", k, v)
				return errors.WithMessagef(err, "CBP mirror status error %s in %s cluster", v, cluster.name)
			}
			addRowOfVerifyTextOutput(verifyText,"CephBlockPool pod %s ... %s", k, v)
		}
	}
	return nil
}
