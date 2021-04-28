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


func addRowOfVerifyTextOutput(newText string) {
	fmt.Fprintln(verifyText, newText)
}

func init() {
		verifyText.
			SetDoneFunc(func(key tcell.Key) {
				verifyText.Clear()
				pages.RemovePage("install")
				pages.SwitchToPage("main")
			    //log.Out = logFile
			})
	}

func showVerifyPage(kubeConfigPrimary kubeAccess, kubeConfigSecondary kubeAccess) {
	pages.AddAndSwitchToPage("verify", verifyText, true)
	log.Out = verifyText

	clusters := []kubeAccess{ kubeConfigPrimary, kubeConfigSecondary }
	for cluster := range clusters {
		addRowOfVerifyTextOutput("")
		addRowOfVerifyTextOutput(fmt.Sprintf("%s cluster", clusters[cluster].name))

		err := verifyRBDMirrorPods(clusters[cluster])
    	if err != nil {
    		log.WithError(err).Warn("Issues when adding the cephv1 scheme to the primary client")
    	}

    	err = verifyCBPmirror(clusters[cluster])
    	if err != nil {
    		log.WithError(err).Warn("Issues when adding the cephv1 scheme to the primary client")
    	}

    	err = verifyOADPOperator(clusters[cluster])
    	if err != nil {
    		log.WithError(err).Warn("Issues when adding the cephv1 scheme to the primary client")
    	}
	}

}

// to be removed? no longer in issue list
func verifyOcsOperator(cluster kubeAccess) (string) {
	ocspod, err := cluster.typedClient.CoreV1().Pods(ocsNamespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=rook-ceph-operator"})
	if err != nil || len(ocspod.Items) == 0 {
		errors.Wrapf(err, "error when looking for ocs operator pod in %s namespace", ocsNamespace)
		log.WithError(err).Warn("OCS operator pods not found")
		addRowOfVerifyTextOutput("OCS NOT installed?")
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
		addRowOfVerifyTextOutput("OADP does NOT appear to be installed and running. Please consider installing OADP.")
		return err
	}
	addRowOfVerifyTextOutput("OADP Operator ... OK")
	return nil
}

//Check that the RBDMirror Pods are Running
func verifyRBDMirrorPods(cluster kubeAccess) (error) {
	rbdlabelselector := "app=csi-rbdplugin-provisioner"
	rbdmirrorpods, err := cluster.typedClient.CoreV1().Pods(ocsNamespace).List(context.TODO(), metav1.ListOptions{LabelSelector: rbdlabelselector})
	if err != nil || len(rbdmirrorpods.Items) == 0 {
		errors.Wrapf(err, "error when looking for rbd mirror pods in %s namespace", ocsNamespace)
		addRowOfVerifyTextOutput(fmt.Sprintf("RBDMirrorPods NOT OK. No pods in %s namespace with label %s found!", ocsNamespace, rbdlabelselector ))
		return err
	}

	if len(rbdmirrorpods.Items) != 2 {
		return errors.New("There should be 2 pods from deployment/csi-rbdplugin-provisioner")
	}

	for rbd := range rbdmirrorpods.Items {
		addRowOfVerifyTextOutput(fmt.Sprintf("RBD Mirror Deployment %d of 2",rbd+1))
		for rbdpod := range rbdmirrorpods.Items[rbd].Spec.Containers {
			addRowOfVerifyTextOutput(fmt.Sprintf("RBD Mirror Pod %s ... OK",rbdmirrorpods.Items[rbd].Spec.Containers[rbdpod].Name))
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
				addRowOfVerifyTextOutput(fmt.Sprintf(" CBP pod %s NOT OK. status: %s", k, v))
				return errors.WithMessagef(err, "CBP mirror status error %s in %s cluster", v, cluster.name)
			}
			addRowOfVerifyTextOutput(fmt.Sprintf("CephBlockPool pod %s ... %s", k, v))
		}
	}
	return nil
}
