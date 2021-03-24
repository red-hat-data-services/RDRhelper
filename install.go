package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	ocsv1 "github.com/openshift/ocs-operator/api/v1"
	"github.com/rivo/tview"
	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//  patchStringValue specifies a patch operation for a string.
type patchStringValue struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string `json:"value"`
}

var installText = tview.NewTextView().
	SetChangedFunc(func() {
		app.Draw()
	})
var ocsNamespace = "openshift-storage"

func addRowOfTextOutput(newText string) {
	fmt.Fprintln(installText, newText)
}

func init() {
	installText.
		SetDoneFunc(func(key tcell.Key) {
			installText.Clear()
			pages.RemovePage("install")
			pages.SwitchToPage("main")
		})
}

func installReplication() {
	pages.AddAndSwitchToPage("install", installText, true)
	log.SetOutput(installText)

	addRowOfTextOutput("Starting Install!")
	addRowOfTextOutput("")

	// TODO
	//  * Check that Kubernetes links are ok
	//  * Check that OCS is installed and ready
	//  * Check that the cluster networks are linked
	go doInstall()
}

func doInstall() error {

	err := enableOMAPGenerator(kubeConfigPrimary)
	if err != nil {
		log.WithError(err).Warn("Issues when enabling OMAP generator in primary cluster")
		return err
	}
	err = enableOMAPGenerator(kubeConfigSecondary)
	if err != nil {
		log.WithError(err).Warn("Issues when enabling OMAP generator in secondary cluster")
		return err
	}

	ocsv1.AddToScheme(kubeConfigPrimary.controllerClient.Scheme())
	ocsv1.AddToScheme(kubeConfigSecondary.controllerClient.Scheme())
	cephv1.AddToScheme(kubeConfigPrimary.controllerClient.Scheme())
	cephv1.AddToScheme(kubeConfigSecondary.controllerClient.Scheme())

	enablePoolMirroring(kubeConfigPrimary, "ocs-storagecluster-cephblockpool")
	enablePoolMirroring(kubeConfigSecondary, "ocs-storagecluster-cephblockpool")

	// Wait for status to be populated...
	time.Sleep(5 * time.Second)
	err = exchangeMirroringBootstrapSecrets(&kubeConfigSecondary, &kubeConfigPrimary)
	if err != nil {
		log.WithError(err).Warnf("Issues when exchanging bootstrap infos from %s to %s", "secondary", "primary")
		return err
	}
	err = exchangeMirroringBootstrapSecrets(&kubeConfigPrimary, &kubeConfigSecondary)
	if err != nil {
		log.WithError(err).Warnf("Issues when exchanging bootstrap infos from %s to %s", "primary", "secondary")
		return err
	}

	// err = changeRBDStorageClasstoRetain(kubeConfigPrimary)
	// if err != nil {
	// 	log.WithError(err).Warn("Issues when changing StorageClass to Retain in primary")
	// 	return err
	// }
	// err = changeRBDStorageClasstoRetain(kubeConfigSecondary)
	// if err != nil {
	// 	log.WithError(err).Warn("Issues when changing StorageClass to Retain in secondary")
	// 	return err
	// }

	addRowOfTextOutput("Install steps done!!")

	// Once we're finished, set logger back to stdout and file
	log.SetOutput(logMultiWriter)
	return nil
}

func enablePoolMirroring(cluster kubeAccess, poolname string) error {
	patchClusterStruc := []patchStringValue{
		{
			Op:    "add",
			Path:  "/spec/managedResources/cephBlockPools/reconcileStrategy",
			Value: "ignore",
		},
	}
	patchClusterJson, err := json.Marshal(patchClusterStruc)
	if err != nil {
		log.WithError(err).Warn("Issues when converting StorageCluster Patch to JSON")
		return err
	}

	currentBlockPool := cephv1.CephBlockPool{}
	err = cluster.controllerClient.Get(context.TODO(),
		types.NamespacedName{Name: poolname, Namespace: ocsNamespace},
		&currentBlockPool)
	if err != nil {
		log.WithError(err).Warnf("Issues when fetching current CephBlockPool in %s cluster", cluster.name)
		return err
	}

	mirrorSpec := cephv1.MirroringSpec{
		Enabled: true,
		Mode:    "image",
		SnapshotSchedules: []cephv1.SnapshotScheduleSpec{
			{
				Interval: "1h",
				// StartTime: "00:00:00-00:00",
			},
		},
	}
	currentBlockPool.Spec.Mirroring = mirrorSpec
	patchClassJson, err := json.Marshal(currentBlockPool)
	if err != nil {
		log.WithError(err).Warn("Issues when converting CephBlockPool Patch to JSON")
		return err
	}

	err = cluster.controllerClient.Patch(context.TODO(),
		&ocsv1.StorageCluster{ObjectMeta: metav1.ObjectMeta{Name: "ocs-storagecluster", Namespace: ocsNamespace}},
		client.RawPatch(types.JSONPatchType, patchClusterJson))

	if err != nil {
		log.WithError(err).Warnf("Issues when patching StorageCluster in %s cluster", cluster.name)
		return err
	}
	addRowOfTextOutput(fmt.Sprintf("[%s] OCS Block Pool reconcile strategy set to ignore", cluster.name))

	err = cluster.controllerClient.Patch(context.TODO(),
		&cephv1.CephBlockPool{ObjectMeta: metav1.ObjectMeta{Name: poolname, Namespace: ocsNamespace}},
		client.RawPatch(types.MergePatchType, patchClassJson))
	if err != nil {
		log.WithError(err).Warnf("Issues when patching CephBlockPool in %s cluster", cluster.name)
		return err
	}
	addRowOfTextOutput(fmt.Sprintf("[%s] OCS Block Pool Mirroring enabled", cluster.name))

	return nil
}

func changeRBDStorageClasstoRetain(cluster kubeAccess) error {
	class, err := cluster.typedClient.StorageV1().StorageClasses().Get(context.TODO(), "ocs-storagecluster-ceph-rbd", metav1.GetOptions{})
	if err != nil {
		log.WithError(err).Warnf("Issues when fetching StorageClass in %s cluster", cluster.name)
		return err
	}
	policy := corev1.PersistentVolumeReclaimRetain
	class.ReclaimPolicy = &policy
	class.TypeMeta = metav1.TypeMeta{APIVersion: "storage.k8s.io/v1", Kind: "StorageClass"}
	class.ManagedFields = nil
	class.ResourceVersion = ""

	err = cluster.typedClient.StorageV1().StorageClasses().Delete(context.TODO(), "ocs-storagecluster-ceph-rbd", metav1.DeleteOptions{})
	if err != nil {
		log.WithError(err).Warnf("Issues when temporarily deleting StorageClass in %s cluster", cluster.name)
		return err
	}
	_, err = cluster.typedClient.StorageV1().StorageClasses().Create(context.TODO(), class, metav1.CreateOptions{})
	if err != nil {
		log.WithError(err).Warnf("Issues when creating new StorageClass in %s cluster", cluster.name)
		return err
	}
	addRowOfTextOutput(fmt.Sprintf("[%s] OCS RBD Storage Class retain policy changed to retain", cluster.name))
	return nil
}

func exchangeMirroringBootstrapSecrets(from, to *kubeAccess) error {
	result := cephv1.CephBlockPool{}
	var secretName string
	for i := 0; i < 60; i++ {

		err := from.controllerClient.Get(context.TODO(),
			types.NamespacedName{Name: "ocs-storagecluster-cephblockpool", Namespace: ocsNamespace},
			&result)
		if err != nil {
			log.WithError(err).Warnf("[%s] Issues when getting CephBlockPool", from.name)
			return err
		}
		secretName = result.Status.Info["rbdMirrorBootstrapPeerSecretName"]
		if secretName != "" {
			break
		}
		addRowOfTextOutput(fmt.Sprintf("[%s] secret name not yet present in pool status", from.name))
		time.Sleep(time.Second)
	}
	if secretName == "" {
		log.Warnf("[%s] Could not find 'rbdMirrorBootstrapPeerSecretName' in %+v", from.name, result.Status.Info)
		return errors.New("secret name not found in pool status")
	}

	secret, err := from.typedClient.CoreV1().Secrets(ocsNamespace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil {
		log.WithError(err).Warnf("[%s] Issues when fetching secret token", from.name)
		return err
	}
	poolToken := secret.Data["token"]
	addRowOfTextOutput(fmt.Sprintf("[%s] Got Pool Mirror secret from secret %s", from.name, secretName))
	mirrorinfo := result.Status.MirroringInfo
	if mirrorinfo == nil {
		log.Warnf("[%s] MirroringInfo not set yet %+v", from.name, mirrorinfo)
		return errors.New("MirroringInfo not set yet")
	}
	siteName := mirrorinfo.Summary["summary"].(map[string]interface{})
	if siteName == nil || siteName["site_name"] == "" {
		log.Warnf("[%s] site_name not set yet %+v", from.name, siteName)
		return errors.New("site_name not set yet")
	}
	addRowOfTextOutput(fmt.Sprintf("[%s] Got site name %s", from.name, siteName["site_name"]))
	bootstrapSecretStruc := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      siteName["site_name"].(string),
			Namespace: ocsNamespace,
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		Data: map[string][]byte{
			"token": poolToken,
			"pool":  []byte("ocs-storagecluster-cephblockpool"),
		},
	}
	bootstrapSecretJSON, err := json.Marshal(bootstrapSecretStruc)
	if err != nil {
		log.Warnf("[%s] issues when converting secret to JSON %+v", from.name, bootstrapSecretStruc)
		return err
	}
	_, err = to.typedClient.CoreV1().Secrets(ocsNamespace).Patch(context.TODO(), siteName["site_name"].(string), types.ApplyPatchType, bootstrapSecretJSON, metav1.PatchOptions{FieldManager: "asyncDRhelper"})
	if err != nil {
		log.WithError(err).Warnf("Issues when creating bootstrap secret in %s location", to.name)
		return err
	}
	addRowOfTextOutput(fmt.Sprintf("[%s] Created bootstrap secret", to.name))
	return nil
}

func enableOMAPGenerator(cluster kubeAccess) error {
	configMapClient := cluster.typedClient.CoreV1().ConfigMaps(ocsNamespace)

	payload := []patchStringValue{{
		Op:    "add",
		Path:  "/data/CSI_ENABLE_OMAP_GENERATOR",
		Value: "true",
	}}
	payloadBytes, _ := json.Marshal(payload)

	addRowOfTextOutput("Patching CM for OMAP Generator")
	addRowOfTextOutput(fmt.Sprintf("  Payload: %+v", payload))
	_, err := configMapClient.Patch(context.TODO(), "rook-ceph-operator-config", types.JSONPatchType, payloadBytes, metav1.PatchOptions{})
	if err != nil {
		addRowOfTextOutput(fmt.Sprintf("Failed with patching: %s", err))
		return fmt.Errorf("failed with enabling the OMAP client on %s", cluster.name)
	}
	addRowOfTextOutput("Patched CM for OMAP Generator")
	addRowOfTextOutput("Waiting for OMAP generator container to appear")

	for {
		pods, err := cluster.typedClient.CoreV1().Pods(ocsNamespace).List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			return errors.New("error when checking on Pods")
		}
		for _, pod := range pods.Items {
			containers := pod.Spec.Containers
			for _, container := range containers {
				if container.Name == "csi-omap-generator" {
					addRowOfTextOutput("OMAP generator container appeared")
					return nil
				}
			}
		}

	}
}
