package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	ocsv1 "github.com/openshift/ocs-operator/api/v1"
	"github.com/pkg/errors"
	"github.com/rivo/tview"
	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	"github.com/tidwall/sjson"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/storage/v1"
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

//  patchStringValue specifies a patch operation for a bool.
type patchBoolValue struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value bool   `json:"value"`
}

var installText = tview.NewTextView().
	SetChangedFunc(func() {
		app.Draw()
	})
var ocsNamespace = "openshift-storage"
var useNewBlockPoolForMirroring = false

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

func showBlockPoolChoice() {

	form := tview.NewForm().
		AddButton("Use Default Block Pool", func() {
			useNewBlockPoolForMirroring = false
			installReplication()
			pages.RemovePage("blockPoolChoice")
		}).
		AddButton("Use Dedicated Block Pool", func() {
			useNewBlockPoolForMirroring = true
			installReplication()
			pages.RemovePage("blockPoolChoice")
		}).
		SetCancelFunc(func() {
			pages.RemovePage("blockPoolChoice")
			pages.SwitchToPage("main")
		}).
		SetButtonsAlign(tview.AlignCenter)

	helperTextFrame := tview.NewFrame(
		tview.NewTextView().
			SetText("Chose if the default or a new Block Pool is used for mirroring PVCs\nUse TAB to switch between buttons, then select with ENTER").
			SetTextAlign(tview.AlignCenter))

	container := tview.NewFlex().SetDirection(tview.FlexRow)
	container.AddItem(helperTextFrame, 0, 1, false)
	container.AddItem(form, 0, 1, true)

	pages.AddAndSwitchToPage("blockPoolChoice", container, true)

}

func installReplication() {

	pages.AddAndSwitchToPage("install", installText, true)

	log.Out = installText

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
		showAlert("Issues when enabling OMAP generator in primary cluster")
		return err
	}
	err = enableOMAPGenerator(kubeConfigSecondary)
	if err != nil {
		log.WithError(err).Warn("Issues when enabling OMAP generator in secondary cluster")
		showAlert("Issues when enabling OMAP generator in secondary cluster")
		return err
	}

	if err = ocsv1.AddToScheme(kubeConfigPrimary.controllerClient.Scheme()); err != nil {
		log.WithError(err).Warn("Issues when adding the ocsv1 scheme to the primary client")
	}
	if err = ocsv1.AddToScheme(kubeConfigSecondary.controllerClient.Scheme()); err != nil {
		log.WithError(err).Warn("Issues when adding the ocsv1 scheme to the secondary client")
	}
	if err = cephv1.AddToScheme(kubeConfigPrimary.controllerClient.Scheme()); err != nil {
		log.WithError(err).Warn("Issues when adding the cephv1 scheme to the primary client")
	}
	if err = cephv1.AddToScheme(kubeConfigSecondary.controllerClient.Scheme()); err != nil {
		log.WithError(err).Warn("Issues when adding the cephv1 scheme to the secondary client")
	}

	blockpool := "ocs-storagecluster-cephblockpool"
	if useNewBlockPoolForMirroring {
		blockpool = "replicapool"
		newBlockPool := cephv1.CephBlockPool{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "ceph.rook.io/v1",
				Kind:       "CephBlockPool",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "replicapool",
				Namespace: ocsNamespace,
			},
			Spec: cephv1.PoolSpec{
				Replicated: cephv1.ReplicatedSpec{
					Size: 3,
				},
				Mirroring: cephv1.MirroringSpec{
					Enabled: true,
					Mode:    "image",
					SnapshotSchedules: []cephv1.SnapshotScheduleSpec{
						{Interval: "1h"},
					},
				},
			},
		}

		if err = createBlockPool(kubeConfigPrimary, &newBlockPool); err != nil {
			log.WithError(err).Warn("Issues when adding new block pool in primary cluster")
			showAlert("Issues when adding new block pool in primary cluster")
			return err
		}
		if err = createBlockPool(kubeConfigSecondary, &newBlockPool); err != nil {
			log.WithError(err).Warn("Issues when adding new block pool in secondary cluster")
			showAlert("Issues when adding new block pool in secondary cluster")
			return err
		}

		storageclassPolicy := corev1.PersistentVolumeReclaimRetain
		storageclassBindingMode := v1.VolumeBindingImmediate

		newStorageClass := v1.StorageClass{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "storage.k8s.io/v1",
				Kind:       "StorageClass",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "ocs-storagecluster-ceph-mirror",
			},
			Parameters: map[string]string{
				"csi.storage.k8s.io/controller-expand-secret-name":      "rook-csi-rbd-provisioner",
				"csi.storage.k8s.io/controller-expand-secret-namespace": "openshift-storage",
				"csi.storage.k8s.io/fstype":                             "ext4",
				"csi.storage.k8s.io/node-stage-secret-name":             "rook-csi-rbd-node",
				"csi.storage.k8s.io/node-stage-secret-namespace":        "openshift-storage",
				"csi.storage.k8s.io/provisioner-secret-name":            "rook-csi-rbd-provisioner",
				"csi.storage.k8s.io/provisioner-secret-namespace":       "openshift-storage",
				"clusterID":     "openshift-storage",
				"imageFeatures": "layering",
				"imageFormat":   "2",
				"pool":          "replicapool",
			},
			Provisioner:       "openshift-storage.rbd.csi.ceph.com",
			ReclaimPolicy:     &storageclassPolicy,
			VolumeBindingMode: &storageclassBindingMode,
		}

		if err = createStorageClass(kubeConfigPrimary, &newStorageClass); err != nil {
			log.WithError(err).Warn("Issues when adding StorageClass in primary cluster")
			showAlert("Issues when adding StorageClass in primary cluster")
			return err
		}
		if err = createStorageClass(kubeConfigSecondary, &newStorageClass); err != nil {
			log.WithError(err).Warn("Issues when adding StorageClass in secondary cluster")
			showAlert("Issues when adding StorageClass in secondary cluster")
			return err
		}
	} else {
		if enablePoolMirroring(kubeConfigPrimary, blockpool); err != nil {
			log.WithError(err).Warn("Issues when enabling mirroring in primary cluster")
			showAlert("Issues when enabling mirroring in primary cluster")
			return err
		}
		if enablePoolMirroring(kubeConfigSecondary, blockpool); err != nil {
			log.WithError(err).Warn("Issues when enabling mirroring in secondary cluster")
			showAlert("Issues when enabling mirroring in secondary cluster")
			return err
		}
	}

	// Wait for status to be populated...
	time.Sleep(5 * time.Second)
	err = exchangeMirroringBootstrapSecrets(&kubeConfigSecondary, &kubeConfigPrimary, blockpool)
	if err != nil {
		log.WithError(err).Warnf("Issues when exchanging bootstrap infos from %s to %s", "secondary", "primary")
		showAlert(fmt.Sprintf("Issues when exchanging bootstrap infos from %s to %s", "secondary", "primary"))
		return err
	}
	err = exchangeMirroringBootstrapSecrets(&kubeConfigPrimary, &kubeConfigSecondary, blockpool)
	if err != nil {
		log.WithError(err).Warnf("Issues when exchanging bootstrap infos from %s to %s", "primary", "secondary")
		showAlert(fmt.Sprintf("Issues when exchanging bootstrap infos from %s to %s", "primary", "secondary"))
		return err
	}

	err = enableToolbox(kubeConfigPrimary)
	if err != nil {
		log.WithError(err).Warnf("Issues when enabling the Toolbox in the %s cluster", "primary")
		showAlert(fmt.Sprintf("Issues when enabling the Toolbox in the %s cluster", "primary"))
		return err
	}
	err = enableToolbox(kubeConfigSecondary)
	if err != nil {
		log.WithError(err).Warnf("Issues when enabling the Toolbox in the %s cluster", "secondary")
		showAlert(fmt.Sprintf("Issues when enabling the Toolbox in the %s cluster", "secondary"))
		return err
	}

	addRowOfTextOutput("Install steps done!!")
	addRowOfTextOutput("Press ENTER to get back to main")

	// Once we're finished, set logger back to stdout and file
	log.Out = logFile
	return nil
}

func createBlockPool(cluster kubeAccess, newBlockPool *cephv1.CephBlockPool) error {
	patchPoolJson, err := json.Marshal(*newBlockPool)
	if err != nil {
		return errors.WithMessage(err, "Issues when converting BlockPool CR to JSON")
	}
	// Fix JSON - OCS 4.7 delivers faulty CRDs with rook-operator
	// Will be resolved with OCS 4.8
	patchedPoolJson, err := sjson.Delete(string(patchPoolJson), "spec.erasureCoded")
	if err != nil {
		return errors.WithMessage(err, "Issues when patching BlockPool CR in JSON")
	}
	log.Infof("DEBUG %s", patchPoolJson)
	err = cluster.controllerClient.Patch(context.TODO(),
		newBlockPool.DeepCopy(),
		client.RawPatch(types.ApplyPatchType, []byte(patchedPoolJson)),
		&client.PatchOptions{FieldManager: "asyncDRhelper"})

	if err != nil {
		return errors.WithMessagef(err, "Issues when applying CephBlockPool in %s cluster", cluster.name)
	}
	return nil
}

func createStorageClass(cluster kubeAccess, newStorageClass *v1.StorageClass) error {
	patchClassJson, err := json.Marshal(*newStorageClass)
	if err != nil {
		return errors.WithMessage(err, "Issues when converting StorageClass CR to JSON")
	}
	_, err = cluster.typedClient.StorageV1().StorageClasses().Patch(context.TODO(),
		newStorageClass.Name,
		types.ApplyPatchType,
		patchClassJson,
		metav1.PatchOptions{FieldManager: "asyncDRhelper"},
	)

	if err != nil {
		return errors.WithMessagef(err, "Issues when applying StorageClass in %s cluster", cluster.name)
	}
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
		return errors.WithMessage(err, "Issues when converting StorageCluster Patch to JSON")
	}

	currentBlockPool := cephv1.CephBlockPool{}
	err = cluster.controllerClient.Get(context.TODO(),
		types.NamespacedName{Name: poolname, Namespace: ocsNamespace},
		&currentBlockPool)
	if err != nil {
		return errors.WithMessagef(err, "Issues when fetching current CephBlockPool in %s cluster", cluster.name)
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
		return errors.WithMessage(err, "Issues when converting CephBlockPool Patch to JSON")
	}

	err = cluster.controllerClient.Patch(context.TODO(),
		&ocsv1.StorageCluster{ObjectMeta: metav1.ObjectMeta{Name: "ocs-storagecluster", Namespace: ocsNamespace}},
		client.RawPatch(types.JSONPatchType, patchClusterJson))

	if err != nil {
		return errors.WithMessagef(err, "Issues when patching StorageCluster in %s cluster", cluster.name)
	}
	addRowOfTextOutput(fmt.Sprintf("[%s] OCS Block Pool reconcile strategy set to ignore", cluster.name))

	err = cluster.controllerClient.Patch(context.TODO(),
		&cephv1.CephBlockPool{ObjectMeta: metav1.ObjectMeta{Name: poolname, Namespace: ocsNamespace}},
		client.RawPatch(types.MergePatchType, patchClassJson))
	if err != nil {
		return errors.WithMessagef(err, "Issues when patching CephBlockPool in %s cluster", cluster.name)
	}
	addRowOfTextOutput(fmt.Sprintf("[%s] OCS Block Pool Mirroring enabled", cluster.name))

	return nil
}

func enableToolbox(cluster kubeAccess) error {
	patchClusterStruc := []patchBoolValue{
		{
			Op:    "replace",
			Path:  "/spec/enableCephTools",
			Value: true,
		},
	}
	patchClusterJson, err := json.Marshal(patchClusterStruc)
	if err != nil {
		return errors.WithMessage(err, "Issues when converting OCSInitialization Patch to JSON")
	}
	err = cluster.controllerClient.Patch(context.TODO(),
		&ocsv1.OCSInitialization{ObjectMeta: metav1.ObjectMeta{Name: "ocsinit", Namespace: ocsNamespace}},
		client.RawPatch(types.JSONPatchType, patchClusterJson))

	if err != nil {
		return errors.WithMessagef(err, "Issues when enabling Ceph Toolbox in %s cluster", cluster.name)
	}
	addRowOfTextOutput(fmt.Sprintf("[%s] OCS Toolbox enabled", cluster.name))

	return nil
}

func changeRBDStorageClasstoRetain(cluster kubeAccess) error {
	class, err := cluster.typedClient.StorageV1().StorageClasses().Get(context.TODO(), "ocs-storagecluster-ceph-rbd", metav1.GetOptions{})
	if err != nil {
		return errors.WithMessagef(err, "Issues when fetching StorageClass in %s cluster", cluster.name)
	}
	policy := corev1.PersistentVolumeReclaimRetain
	class.ReclaimPolicy = &policy
	class.TypeMeta = metav1.TypeMeta{APIVersion: "storage.k8s.io/v1", Kind: "StorageClass"}
	class.ManagedFields = nil
	class.ResourceVersion = ""

	err = cluster.typedClient.StorageV1().StorageClasses().Delete(context.TODO(), "ocs-storagecluster-ceph-rbd", metav1.DeleteOptions{})
	if err != nil {
		return errors.WithMessagef(err, "Issues when temporarily deleting StorageClass in %s cluster", cluster.name)
	}
	_, err = cluster.typedClient.StorageV1().StorageClasses().Create(context.TODO(), class, metav1.CreateOptions{})
	if err != nil {
		return errors.WithMessagef(err, "Issues when creating new StorageClass in %s cluster", cluster.name)
	}
	addRowOfTextOutput(fmt.Sprintf("[%s] OCS RBD Storage Class retain policy changed to retain", cluster.name))
	return nil
}

func exchangeMirroringBootstrapSecrets(from, to *kubeAccess, blockpool string) error {
	result := cephv1.CephBlockPool{}
	var secretName string
	for i := 0; i < 60; i++ {

		err := from.controllerClient.Get(context.TODO(),
			types.NamespacedName{Name: blockpool, Namespace: ocsNamespace},
			&result)
		if err != nil {
			log.WithError(err).Warnf("[%s] Issues when getting CephBlockPool", from.name)
			return err
		}
		if result.Status != nil && result.Status.Info != nil {
			secretName = result.Status.Info["rbdMirrorBootstrapPeerSecretName"]
			if secretName != "" {
				break
			}
		}
		addRowOfTextOutput(fmt.Sprintf("[%s] secret name not yet present in pool status", from.name))
		time.Sleep(time.Second)
	}
	if secretName == "" {
		log.Warnf("[%s] Could not find 'rbdMirrorBootstrapPeerSecretName' in %s status block", from.name, blockpool)
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
			"pool":  []byte(blockpool),
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
	// err = cluster.controllerClient.Patch(context.TODO(),
	// 	&ocsv1.StorageCluster{ObjectMeta: metav1.ObjectMeta{Name: "ocs-storagecluster", Namespace: ocsNamespace}},
	// 	client.RawPatch(types.JSONPatchType, patchClusterJson))
	rbdMirrorSpec := cephv1.CephRBDMirror{
		ObjectMeta: metav1.ObjectMeta{Name: "rbd-mirror", Namespace: ocsNamespace},
		TypeMeta:   metav1.TypeMeta{Kind: "CephRBDMirror", APIVersion: "ceph.rook.io/v1"},
		Spec: cephv1.RBDMirroringSpec{
			Count: 1,
			Peers: cephv1.RBDMirroringPeerSpec{
				SecretNames: []string{siteName["site_name"].(string)},
			},
		}}
	rbdMirrorJSON, err := json.Marshal(rbdMirrorSpec)
	if err != nil {
		log.Warnf("[%s] issues when converting rbd-mirror Spec to JSON %+v", from.name, rbdMirrorSpec)
		return err
	}
	err = to.controllerClient.Patch(context.TODO(),
		&rbdMirrorSpec,
		client.RawPatch(types.ApplyPatchType, rbdMirrorJSON), &client.PatchOptions{FieldManager: "asyncDRhelper"})
	if err != nil {
		log.WithError(err).Warnf("Issues when creating rbd-mirror CR in %s location", to.name)
		return err
	}
	addRowOfTextOutput(fmt.Sprintf("[%s] Created rbd-mirror CR", to.name))
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
		if checkForOMAPGenerator(cluster) {
			addRowOfTextOutput("OMAP generator container appeared")
			return nil
		}
	}
}

func checkForOMAPGenerator(cluster kubeAccess) bool {
	pods, err := cluster.typedClient.CoreV1().Pods(ocsNamespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=csi-rbdplugin-provisioner"})
	if err != nil {
		return false
	}
	for _, pod := range pods.Items {
		containers := pod.Spec.Containers
		for _, container := range containers {
			if container.Name == "csi-omap-generator" {
				return true
			}
		}
	}
	return false
}
