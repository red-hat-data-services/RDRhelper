package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	ocsv1 "github.com/openshift/ocs-operator/api/v1"
	"github.com/operator-framework/api/pkg/lib/version"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/pkg/errors"
	"github.com/rivo/tview"
	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	"github.com/tidwall/sjson"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

type s3information struct {
	Bucketname       string `yaml:"bucketname"`
	Region           string `yaml:"region"`
	S3URL            string `yaml:"s3URL"`
	S3ForcePathStyle bool   `yaml:"s3ForcePathStyle"`
	S3AllowInsecure  bool   `yaml:"s3AllowInsecure"`
	S3keyID          string `yaml:"s3keyID"`
	S3keySecret      string `yaml:"s3keySecret"`
	Objectprefix     string `yaml:"objectprefix"`
}

var installText = tview.NewTextView().
	SetChangedFunc(func() {
		app.Draw()
	})

const ocsNamespace = "openshift-storage"

var useNewBlockPoolForMirroring = false
var installOADP = true

func addRowOfTextOutput(newText string) {
	fmt.Fprintln(installText, newText)
}

func init() {
	installText.
		SetDoneFunc(func(key tcell.Key) {
			installText.Clear()
			pages.RemovePage("install")
			pages.SwitchToPage("main")
			log.Out = logFile
		})
	appConfig.S3info.Objectprefix = "velero"
	appConfig.S3info.S3ForcePathStyle = true
	appConfig.S3info.S3AllowInsecure = false
}

func showBlockPoolChoice() {
	err := checkInstallRequirements(kubeConfigPrimary)
	if err != nil {
		showAlert(fmt.Sprintf("Install requirements are not met in the primary cluster!\n%s", err))
		return
	}
	err = checkInstallRequirements(kubeConfigSecondary)
	if err != nil {
		showAlert(fmt.Sprintf("Install requirements are not met in the secondary cluster!\n%s", err))
		return
	}
	err = checkNetworkBetweenClusters(kubeConfigPrimary, kubeConfigSecondary)
	if err != nil {
		showAlert(fmt.Sprintf("Network connection between cluster could not be checked successfully!\n%s", err))
		return
	}

	form := tview.NewForm().
		AddCheckbox("Install OADP for CR backups", true, func(checked bool) { installOADP = checked }).
		AddButton("Use Default Block Pool", func() {
			useNewBlockPoolForMirroring = false
			gatherS3Info()
			pages.RemovePage("blockPoolChoice")
		}).
		AddButton("Use Dedicated Block Pool", func() {
			useNewBlockPoolForMirroring = true
			gatherS3Info()
			pages.RemovePage("blockPoolChoice")
		}).
		SetCancelFunc(func() {
			pages.RemovePage("blockPoolChoice")
			pages.SwitchToPage("main")
		}).
		SetButtonsAlign(tview.AlignCenter)

	helperText :=
		tview.NewTextView().
			SetText("Chose if the default or a new Block Pool is used for mirroring PVCs\nUse TAB to switch between buttons, then select with ENTER").
			SetTextAlign(tview.AlignCenter)

	container := tview.NewFlex().SetDirection(tview.FlexRow)
	container.AddItem(helperText, 4, 1, false)
	container.AddItem(form, 0, 1, true)

	pages.AddAndSwitchToPage("blockPoolChoice", container, true)

}

func gatherS3Info() {
	if !installOADP {
		installReplication()
		return
	}

	form := tview.NewForm().
		AddInputField("s3 access key ID", appConfig.S3info.S3keyID, 0, nil, func(text string) { appConfig.S3info.S3keyID = text }).
		AddInputField("s3 access key secret", appConfig.S3info.S3keySecret, 0, nil, func(text string) { appConfig.S3info.S3keySecret = text }).
		AddInputField("s3 region", appConfig.S3info.Region, 0, nil, func(text string) { appConfig.S3info.Region = text }).
		AddInputField("s3 bucket name", appConfig.S3info.Bucketname, 0, nil, func(text string) { appConfig.S3info.Bucketname = text }).
		AddInputField("object name prefix", appConfig.S3info.Objectprefix, 0, nil, func(text string) { appConfig.S3info.Objectprefix = text }).
		AddButton("Proceed", func() {
			if validateS3info() {
				writeNewConfig()
				installReplication()
				pages.RemovePage("s3Info")
			} else {
				showAlert("These S3 information are not valid. Please try again.")
			}
		}).
		AddButton("Cancel", func() {
			pages.RemovePage("s3Info")
			pages.SwitchToPage("main")
		}).
		SetCancelFunc(func() {
			pages.RemovePage("s3Info")
			pages.SwitchToPage("main")
		}).
		SetButtonsAlign(tview.AlignCenter)

	helperText :=
		tview.NewTextView().
			SetText("Please provide the S3 details for the bucket that will be used to store the CR definition of your synchronized namespaces\nThis is for AWS S3 buckets only at the moment\nUse TAB to jump between lines, then select Proceed with ENTER").
			SetTextAlign(tview.AlignCenter)

	container := tview.NewFlex().SetDirection(tview.FlexRow)
	container.AddItem(helperText, 4, 1, false)
	container.AddItem(form, 0, 1, true)

	pages.AddAndSwitchToPage("s3Info", container, true)
}

func installReplication() {

	pages.AddAndSwitchToPage("install", installText, true)

	// TODO
	//  * Check that Kubernetes links are ok
	//  * Check that OCS is installed and ready
	//  * Check that the cluster networks are linked
	go doInstall()
}

func doInstall() error {
	log.Out = installText

	addRowOfTextOutput("Starting Install!")
	if useNewBlockPoolForMirroring {
		addRowOfTextOutput("Using dedicated Block Pool")
	} else {
		addRowOfTextOutput("Using default Block Pool")
	}
	addRowOfTextOutput("")

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
		if err = enablePoolMirroring(kubeConfigPrimary, blockpool); err != nil {
			log.WithError(err).Warn("Issues when enabling mirroring in primary cluster")
			showAlert("Issues when enabling mirroring in primary cluster")
			return err
		}
		if err = enablePoolMirroring(kubeConfigSecondary, blockpool); err != nil {
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

	err = doInstallOADP(kubeConfigPrimary)
	if err != nil {
		log.WithError(err).Warnf("Issues when installing OADP in the %s cluster", "primary")
		showAlert(fmt.Sprintf("Issues when installing OADP in the %s cluster", "primary"))
		return err
	}
	err = doInstallOADP(kubeConfigSecondary)
	if err != nil {
		log.WithError(err).Warnf("Issues when installing OADP in the %s cluster", "secondary")
		showAlert(fmt.Sprintf("Issues when installing OADP in the %s cluster", "secondary"))
		return err
	}
	err = verifyOADPinstall(kubeConfigPrimary)
	if err != nil {
		log.WithError(err).Warnf("Issues when verifying OADP in the %s cluster", "primary")
		showAlert(fmt.Sprintf("Issues when verifying OADP in the %s cluster", "primary"))
		return err
	}
	err = verifyOADPinstall(kubeConfigSecondary)
	if err != nil {
		log.WithError(err).Warnf("Issues when verifying OADP in the %s cluster", "secondary")
		showAlert(fmt.Sprintf("Issues when verifying OADP in the %s cluster", "secondary"))
		return err
	}

	addRowOfTextOutput("")
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
	patchClusterJson := `
	{
		"spec": {
		  "managedResources": { "cephBlockPools": { "reconcileStrategy": "ignore" } }
		}
	}`

	currentBlockPool := cephv1.CephBlockPool{}
	err := cluster.controllerClient.Get(context.TODO(),
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
		client.RawPatch(types.MergePatchType, []byte(patchClusterJson)))

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

func exchangeMirroringBootstrapSecrets(from, to *kubeAccess, blockPoolName string) error {
	var blockPool cephv1.CephBlockPool
	var cbpList cephv1.CephBlockPoolList
	var tokenSecretName string
	for {
		err := from.controllerClient.List(context.TODO(),
			&cbpList, &client.ListOptions{Namespace: ocsNamespace})
		if err != nil {
			log.WithError(err).Warnf("[%s] Issues when listing CephBlockPools", from.name)
			return err
		}
		for _, cbp := range cbpList.Items {
			if cbp.Name == blockPoolName {
				blockPool = cbp
				break
			}
		}
		if blockPool.Status != nil && blockPool.Status.Info != nil && blockPool.Status.MirroringInfo != nil && blockPool.Status.MirroringInfo.Summary["summary"] != nil {
			tokenSecretName = blockPool.Status.Info["rbdMirrorBootstrapPeerSecretName"]
			if tokenSecretName != "" {
				break
			}
		}
		addRowOfTextOutput(fmt.Sprintf("[%s] mirroring info not yet available in pool status", from.name))
		time.Sleep(time.Second * 3)
	}
	if tokenSecretName == "" {
		log.Warnf("[%s] Could not find 'rbdMirrorBootstrapPeerSecretName' in %s status block", from.name, blockPoolName)
		return errors.New("secret name not found in pool status")
	}

	secret, err := from.typedClient.CoreV1().Secrets(ocsNamespace).Get(context.TODO(), tokenSecretName, metav1.GetOptions{})
	if err != nil {
		log.WithError(err).Warnf("[%s] Issues when fetching secret token", from.name)
		return err
	}
	poolToken := secret.Data["token"]
	addRowOfTextOutput(fmt.Sprintf("[%s] Got Pool Mirror secret from secret %s", from.name, tokenSecretName))
	mirrorinfo := blockPool.Status.MirroringInfo
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
	bootstrapSecretName := fmt.Sprintf("mirror-bootstrap-%s", blockPoolName)
	bootstrapSecretStruc := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bootstrapSecretName,
			Namespace: ocsNamespace,
			Labels: map[string]string{
				"usage":     "bootstrap",
				"pool":      blockPoolName,
				"site-name": siteName["site_name"].(string),
			},
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		Data: map[string][]byte{
			"token": poolToken,
			"pool":  []byte(blockPoolName),
		},
	}
	bootstrapSecretJSON, err := json.Marshal(bootstrapSecretStruc)
	if err != nil {
		log.Warnf("[%s] issues when converting secret to JSON %+v", from.name, bootstrapSecretStruc)
		return err
	}
	_, err = to.typedClient.CoreV1().Secrets(ocsNamespace).
		Patch(context.TODO(), bootstrapSecretName,
			types.ApplyPatchType, bootstrapSecretJSON, metav1.PatchOptions{FieldManager: "asyncDRhelper"})
	if err != nil {
		log.WithError(err).Warnf("Issues when creating bootstrap secret in %s location", to.name)
		return err
	}
	addRowOfTextOutput(fmt.Sprintf("[%s] Created bootstrap secret", to.name))
	mirrroringSecrets := getAllSecretNames(*to)
	if len(mirrroringSecrets) == 0 {
		return errors.WithMessagef(err, "No bootstrap secrets found")
	}
	rbdMirrorSpec := cephv1.CephRBDMirror{
		ObjectMeta: metav1.ObjectMeta{Name: "rbd-mirror", Namespace: ocsNamespace},
		TypeMeta:   metav1.TypeMeta{Kind: "CephRBDMirror", APIVersion: "ceph.rook.io/v1"},
		Spec: cephv1.RBDMirroringSpec{
			Count: len(mirrroringSecrets),
			Peers: cephv1.RBDMirroringPeerSpec{
				SecretNames: mirrroringSecrets,
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

func getAllSecretNames(cluster kubeAccess) []string {
	mirrroringSecrets := []string{}
	secretList, err := cluster.typedClient.CoreV1().
		Secrets(ocsNamespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "usage=bootstrap"})
	if err != nil {
		log.WithError(err).Warnf("[%s] Issues when listing secrets for bootstrap exchange", cluster.name)
	}
	for _, secret := range secretList.Items {
		mirrroringSecrets = append(mirrroringSecrets, secret.Name)
	}
	return mirrroringSecrets
}

func enableOMAPGenerator(cluster kubeAccess) error {
	configMapClient := cluster.typedClient.CoreV1().ConfigMaps(ocsNamespace)

	payload := []patchStringValue{{
		Op:    "add",
		Path:  "/data/CSI_ENABLE_OMAP_GENERATOR",
		Value: "true",
	}}
	payloadBytes, _ := json.Marshal(payload)

	addRowOfTextOutput(fmt.Sprintf("[%s] Patching CM for OMAP Generator", cluster.name))
	log.Debugf("  Payload: %+v", payload)
	_, err := configMapClient.Patch(context.TODO(), "rook-ceph-operator-config", types.JSONPatchType, payloadBytes, metav1.PatchOptions{})
	if err != nil {
		log.WithError(err).Errorf("[%s] Failed with patching", cluster.name)
		return errors.WithMessagef(err, "failed with patching the OMAP client on %s", cluster.name)
	}
	addRowOfTextOutput(fmt.Sprintf("[%s] Patched CM for OMAP Generator", cluster.name))
	addRowOfTextOutput(fmt.Sprintf("[%s] Waiting for OMAP generator container to appear", cluster.name))

	for {
		if checkForOMAPGenerator(cluster) {
			addRowOfTextOutput(fmt.Sprintf("[%s] OMAP generator container appeared", cluster.name))
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

func doInstallOADP(cluster kubeAccess) error {
	if err := operatorsv1alpha1.AddToScheme(cluster.controllerClient.Scheme()); err != nil {
		return errors.WithMessagef(err, "[%s] Issues when adding operator API schemas", cluster.name)
	}
	if err := operatorsv1.AddToScheme(cluster.controllerClient.Scheme()); err != nil {
		return errors.WithMessagef(err, "[%s] Issues when adding operator API schemas", cluster.name)
	}
	if err := velerov1.AddToScheme(cluster.controllerClient.Scheme()); err != nil {
		return errors.WithMessagef(err, "[%s] Issues when adding velero schemas", cluster.name)
	}

	// Create instead of Patch, because Patch created too many issues... If this fails, it's 99% of the time because the namespace already exists
	cluster.typedClient.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"}, ObjectMeta: metav1.ObjectMeta{Name: "oadp-operator"}}, metav1.CreateOptions{})

	oadpSubscriptionSpec := operatorsv1alpha1.Subscription{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "operators.coreos.com/v1alpha1",
			Kind:       "Subscription",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oadp-operator",
			Namespace: "oadp-operator",
		},
		Spec: &operatorsv1alpha1.SubscriptionSpec{
			Package:                "oadp-operator",
			Channel:                "alpha",
			InstallPlanApproval:    operatorsv1alpha1.ApprovalAutomatic,
			CatalogSourceNamespace: "openshift-marketplace",
			CatalogSource:          "community-operators",
			// StartingCSV:            "oadp-operator.v0.2.1",
		},
	}
	oadpSubscriptionJSON, err := json.Marshal(oadpSubscriptionSpec)
	if err != nil {
		return errors.WithMessagef(err, "[%s] issues when converting OADP Subscription Spec to JSON %+v", cluster.name, oadpSubscriptionSpec)
	}
	tmp, err := sjson.Delete(string(oadpSubscriptionJSON), "status")
	if err != nil {
		return errors.WithMessagef(err, "[%s] issues when patching OADP Subscription JSON", cluster.name)
	}
	oadpSubscriptionPatchedJSON := []byte(tmp)
	err = cluster.controllerClient.Patch(context.TODO(),
		&oadpSubscriptionSpec,
		client.RawPatch(types.ApplyPatchType, oadpSubscriptionPatchedJSON),
		&client.PatchOptions{FieldManager: "asyncDRhelper"})
	if err != nil {
		return errors.WithMessagef(err, "[%s] issues when applying OADP Subscription", cluster.name)
	}

	oadpOGroupSpec := operatorsv1.OperatorGroup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "operators.coreos.com/v1",
			Kind:       "OperatorGroup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oadp-operator",
			Namespace: "oadp-operator",
		},
		Spec: operatorsv1.OperatorGroupSpec{
			TargetNamespaces: []string{"oadp-operator"},
		},
	}
	oadpOGroupJSON, err := json.Marshal(oadpOGroupSpec)
	if err != nil {
		return errors.WithMessagef(err, "[%s] issues when converting OADP OperatorGroup Spec to JSON %+v", cluster.name, oadpOGroupSpec)
	}
	tmp, err = sjson.Delete(string(oadpOGroupJSON), "status")
	if err != nil {
		return errors.WithMessagef(err, "[%s] issues when patching OADP OperatorGroup JSON", cluster.name)
	}
	oadpOGroupPatchedJSON := []byte(tmp)
	err = cluster.controllerClient.Patch(context.TODO(),
		&oadpOGroupSpec,
		client.RawPatch(types.ApplyPatchType, oadpOGroupPatchedJSON),
		&client.PatchOptions{FieldManager: "asyncDRhelper"})
	if err != nil {
		return errors.WithMessagef(err, "[%s] issues when applying OADP OperatorGroup", cluster.name)
	}

	s3CredStruc := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cloud-credentials",
			Namespace: "oadp-operator",
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		Data: map[string][]byte{
			"cloud": []byte(fmt.Sprintf("[default]\naws_access_key_id=%s\naws_secret_access_key=%s", appConfig.S3info.S3keyID, appConfig.S3info.S3keySecret)),
		},
	}
	s3CredJSON, err := json.Marshal(s3CredStruc)
	if err != nil {
		return errors.WithMessagef(err, "[%s] issues when converting secret to JSON %+v", cluster.name, s3CredStruc)
	}
	_, err = cluster.typedClient.CoreV1().Secrets("oadp-operator").Patch(context.TODO(),
		"cloud-credentials",
		types.ApplyPatchType,
		s3CredJSON,
		metav1.PatchOptions{FieldManager: "asyncDRhelper"})
	if err != nil {
		return errors.WithMessagef(err, "[%s] issues when creating S3 secret", cluster.name)
	}
	addRowOfTextOutput(fmt.Sprintf("[%s] OADP cloud secret created", cluster.name))

	// Wait for OADP Operator to be installed

	for {
		var csvs operatorsv1alpha1.ClusterServiceVersionList
		err = cluster.controllerClient.List(context.TODO(),
			&csvs, client.MatchingLabels{"operators.coreos.com/oadp-operator.oadp-operator": ""})
		if err != nil {
			log.WithError(err).Warnf("[%s] issues when listing OADP ClusterServiceVersions - Retrying...", cluster.name)
			time.Sleep(9 * time.Second)
			continue
		}
		if len(csvs.Items) == 0 {
			addRowOfTextOutput(fmt.Sprintf("[%s] No OADP Operator detected yet - Retrying...", cluster.name))
			time.Sleep(9 * time.Second)
			continue
		}

		csv := csvs.Items[0]
		if csv.Status.Phase == "" {
			time.Sleep(3 * time.Second)
			continue
		}

		if csv.Status.Phase == "Succeeded" {
			break
		}

		addRowOfTextOutput(fmt.Sprintf("[%s] OADP operator is still installing", cluster.name))
		time.Sleep(3 * time.Second)
	}
	addRowOfTextOutput(fmt.Sprintf("[%s] OADP operator is installed and ready now", cluster.name))

	veleroJSON := fmt.Sprintf(`
apiVersion: konveyor.openshift.io/v1alpha1
kind: Velero
metadata:
  name: oadp-velero
  namespace: oadp-operator
spec:
  olm_managed: true
  backup_storage_locations:
    - config:
        profile: default
        region: %s
      credentials_secret_ref:
        name: cloud-credentials
        namespace: oadp-operator
      name: default
      object_storage:
        bucket: %s
        prefix: %s
      provider: aws
  default_velero_plugins:
    - aws
    - openshift
  enable_restic: false`, appConfig.S3info.Region, appConfig.S3info.Bucketname, appConfig.S3info.Objectprefix)

	veleroRes := schema.GroupVersionResource{
		Group:    "konveyor.openshift.io",
		Version:  "v1alpha1",
		Resource: "veleros",
	}
	_, err = cluster.dynamicClient.Resource(veleroRes).Namespace("oadp-operator").Patch(context.TODO(),
		"oadp-velero", types.ApplyPatchType, []byte(veleroJSON), metav1.PatchOptions{FieldManager: "asyncDRhelper"})
	if err != nil {
		return errors.WithMessagef(err, "[%s] issues when creating Velero CR", cluster.name)
	}
	addRowOfTextOutput(fmt.Sprintf("[%s] OADP Velero CR created", cluster.name))
	return nil
}
func verifyOADPinstall(cluster kubeAccess) error {
	addRowOfTextOutput(fmt.Sprintf("[%s] verifying OADP install", cluster.name))
	for {
		podlist, err := cluster.typedClient.CoreV1().Pods("oadp-operator").List(context.TODO(), metav1.ListOptions{LabelSelector: "component=velero"})
		if err != nil {
			log.WithError(err).Warnf("[%s] issues when listing Pods in oadp-operator namespace - Retrying...", cluster.name)
			time.Sleep(9 * time.Second)
			continue
		}
		if len(podlist.Items) == 0 {
			addRowOfTextOutput(fmt.Sprintf("[%s] still waiting for Velero Pod to appear...", cluster.name))
			time.Sleep(9 * time.Second)
			continue
		}
		if podlist.Items[0].Status.Phase == corev1.PodRunning {
			addRowOfTextOutput(fmt.Sprintf("[%s] Velero Pod is ready and Running", cluster.name))
			break
		}
		addRowOfTextOutput(fmt.Sprintf("[%s] Velero Pod is not yet running", cluster.name))
		time.Sleep(3 * time.Second)
	}

	for {
		var backupstoragelocation velerov1.BackupStorageLocation
		err := cluster.controllerClient.Get(context.TODO(),
			types.NamespacedName{Name: "default", Namespace: "oadp-operator"},
			&backupstoragelocation)
		if err != nil {
			addRowOfTextOutput(fmt.Sprintf("[%s] issues when fetching default BackupStorageLocation - Retrying...", cluster.name))
			time.Sleep(9 * time.Second)
			continue
		}
		if backupstoragelocation.Status.Phase == "Available" {
			addRowOfTextOutput(fmt.Sprintf("[%s] BackupStorageLocation is Available", cluster.name))
			break
		}

		addRowOfTextOutput(fmt.Sprintf("[%s] BackupStorageLocation is not Available yet", cluster.name))
		time.Sleep(3 * time.Second)
	}

	addRowOfTextOutput(fmt.Sprintf("[%s] OADP install is complete", cluster.name))

	return nil
}

func validateS3info() bool {
	if appConfig.S3info.S3keyID == "" ||
		appConfig.S3info.S3keySecret == "" ||
		appConfig.S3info.Region == "" ||
		appConfig.S3info.Bucketname == "" {
		return false
	}

	// TODO: Do some more smart validation on the S3infos that will work on all cloud providers and the RGW

	return true
}

func checkInstallRequirements(cluster kubeAccess) error {
	ocsVersion, err := checkForOCSCSV(cluster)
	if err != nil {
		return errors.WithMessagef(err, "[%s] OCS not properly installed", cluster.name)
	}
	if ocsVersion.Major != uint64(4) || ocsVersion.Minor != uint64(7) {
		return errors.WithMessagef(err, "[%s] OCS version does not match 4.7 - detected version %d.%d", cluster.name, ocsVersion.Major, ocsVersion.Minor)
	}

	storageClusterRes := schema.GroupVersionResource{
		Group:    "ocs.openshift.io",
		Version:  "v1",
		Resource: "storageclusters",
	}
	storageClusterIdentifier := types.NamespacedName{
		Name:      "ocs-storagecluster",
		Namespace: ocsNamespace,
	}
	status, err := getObjectStatus(storageClusterRes, storageClusterIdentifier, cluster)
	if err != nil {
		return errors.WithMessagef(err, "[%s] Issues when checking StorageCluster status", cluster.name)
	}
	if status != "Ready" {
		return errors.Errorf("StorageCluster is not ready yet - current status is %s", status)
	}

	return nil
}

func checkForOCSCSV(cluster kubeAccess) (version.OperatorVersion, error) {
	if err := operatorsv1alpha1.AddToScheme(cluster.controllerClient.Scheme()); err != nil {
		return version.OperatorVersion{}, errors.WithMessagef(err, "[%s] Issues when adding operator API schemas", cluster.name)
	}
	var csvs operatorsv1alpha1.ClusterServiceVersionList
	err := cluster.controllerClient.List(context.TODO(),
		&csvs, client.MatchingLabels{"operators.coreos.com/ocs-operator.openshift-storage": ""})
	if err != nil {
		return version.OperatorVersion{}, errors.WithMessagef(err, "[%s] issues when listing OADP ClusterServiceVersions", cluster.name)
	}
	csvCount := len(csvs.Items)
	if csvCount == 0 {
		return version.OperatorVersion{}, errors.New("No OCS CSV found")
	}
	if csvCount > 1 {
		return version.OperatorVersion{}, errors.New("More than one OCS CSV found")
	}
	return csvs.Items[0].Spec.Version, nil
}

func getObjectStatus(resource schema.GroupVersionResource, identifier types.NamespacedName, cluster kubeAccess) (string, error) {
	obj, err := cluster.dynamicClient.Resource(resource).Namespace(identifier.Namespace).Get(context.TODO(), identifier.Name, metav1.GetOptions{})
	if err != nil {
		return "", errors.WithMessagef(err, "[%s] Issues when fetching %s/%s in namespace %s", cluster.name, resource.Resource, identifier.Name, identifier.Namespace)
	}
	status := obj.Object["status"].(map[string]interface{})
	if status == nil || status["phase"] == nil || status["phase"].(string) == "" {
		return "", errors.New("[%s] Status of %s/%s in namespace %s is not set yet")
	}
	return status["phase"].(string), nil
}
