package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	ocsv1 "github.com/openshift/ocs-operator/api/v1"
	"github.com/rivo/tview"
	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
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

func installReplication() {
	pages.AddAndSwitchToPage("install", installText, true)
	log.SetOutput(installText)
	log.Info("Log redirected")

	addRowOfTextOutput("Starting Install!")
	addRowOfTextOutput("")

	var wg sync.WaitGroup
	wg.Add(1)
	// TODO
	//  * Check that Kubernetes links are ok
	//  * Check that OCS is installed and ready
	//  * Check that the cluster networks are linked
	go doInstall(&wg)
	wg.Wait()
	addRowOfTextOutput("Install steps done!!")

	// Once we're finished, set logger back to stdout
	// log.SetOutput(os.Stdout)
}

func doInstall(wg *sync.WaitGroup) error {
	defer wg.Done()

	// enableOMAPGenerator(kubeConfigPrimary.typedClient, "primary")
	// enableOMAPGenerator(kubeConfigSecondary.typedClient, "secondary")

	ocsv1.AddToScheme(kubeConfigPrimary.controllerClient.Scheme())
	ocsv1.AddToScheme(kubeConfigSecondary.controllerClient.Scheme())
	patchClusterStruc := ocsv1.StorageCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "ocs-storagecluster", Namespace: ocsNamespace},
		Spec: ocsv1.StorageClusterSpec{
			ManagedResources: ocsv1.ManagedResourcesSpec{
				CephBlockPools: ocsv1.ManageCephBlockPools{
					ReconcileStrategy: ocsv1.ReconcileInit,
				},
			},
		},
	}
	patchClusterJson, err := json.Marshal(patchClusterStruc)
	if err != nil {
		log.WithError(err).Warn("Issues when converting StorageCluster Patch to JSON")
		return err
	}
	patchClassStruc := cephv1.CephBlockPool{
		Spec: cephv1.PoolSpec{
			Mirroring: cephv1.MirroringSpec{
				Enabled: true,
				Mode:    "image",
				SnapshotSchedules: []cephv1.SnapshotScheduleSpec{
					{
						Interval:  "1h",
						StartTime: "00:00:00-00:00",
					},
				},
			},
			StatusCheck: cephv1.MirrorHealthCheckSpec{
				Mirror: cephv1.HealthCheckSpec{
					Disabled: false,
					Interval: "60",
				},
			},
		},
	}
	patchClassJson, err := json.Marshal(patchClassStruc)

	if err != nil {
		log.WithError(err).Warn("Issues when converting CephBlockPool Patch to JSON")
		return err
	}

	err = kubeConfigPrimary.controllerClient.Patch(context.TODO(),
		&ocsv1.StorageCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "ocs-storagecluster", Namespace: ocsNamespace},
		},
		client.RawPatch(types.MergePatchType, patchClusterJson))

	if err != nil {
		log.WithError(err).Warn("Issues when patching StorageCluster in primary cluster")
		return err
	}
	addRowOfTextOutput("[primary] OCS Block Pool reconcile strategy set to init")

	err = kubeConfigSecondary.typedClient.RESTClient().
		Patch(types.JSONPatchType).
		Namespace(ocsNamespace).
		Resource("storageclusters").
		AbsPath("ocs.openshift.io/v1").
		Name("ocs-storagecluster").
		// VersionedParams(&metav1.PatchOptions{}, scheme.ParameterCodec).
		Body(patchClusterJson).
		Do(context.TODO()).Error()
	if err != nil {
		log.WithError(err).Warn("Issues when patching StorageCluster in secondary cluster")
		return err
	}
	addRowOfTextOutput("[secondary] OCS Block Pool reconcile strategy set to init")

	err = kubeConfigPrimary.typedClient.RESTClient().
		Patch(types.JSONPatchType).
		Namespace(ocsNamespace).
		Resource("cephblockpools.ceph.rook.io").
		AbsPath().
		Name("ocs-storagecluster-cephblockpool").
		// VersionedParams(&metav1.PatchOptions{}, scheme.ParameterCodec).
		Body(patchClassJson).
		Do(context.TODO()).Error()
	if err != nil {
		log.WithError(err).Warn("Issues when patching CephBlockPool in primary cluster")
		return err
	}
	addRowOfTextOutput("[primary] OCS Block Pool Mirroring enabled")

	err = kubeConfigSecondary.typedClient.RESTClient().
		Patch(types.JSONPatchType).
		Namespace(ocsNamespace).
		Resource("cephblockpools.ceph.rook.io").
		AbsPath().
		Name("ocs-storagecluster-cephblockpool").
		// VersionedParams(&metav1.PatchOptions{}, scheme.ParameterCodec).
		Body(patchClassJson).
		Do(context.TODO()).Error()
	if err != nil {
		log.WithError(err).Warn("Issues when patching CephBlockPool in secondary cluster")
		return err
	}
	addRowOfTextOutput("[secondary] OCS Block Pool Mirroring enabled")

	// Wait for status to be populated...
	time.Sleep(2 * time.Second)
	exchangeMirroringBootstrapSecrets(kubeConfigSecondary.typedClient, kubeConfigPrimary.typedClient, "secondary", "primary")
	exchangeMirroringBootstrapSecrets(kubeConfigPrimary.typedClient, kubeConfigSecondary.typedClient, "primary", "secondary")

	payload := []patchStringValue{{
		Op:    "replace",
		Path:  "reclaimPolicy",
		Value: "Retain",
	}}
	payloadBytes, _ := json.Marshal(payload)
	_, err = kubeConfigPrimary.typedClient.StorageV1().StorageClasses().Patch(context.TODO(), "ocs-storagecluster-ceph-rbd", types.JSONPatchType, payloadBytes, metav1.PatchOptions{})
	if err != nil {
		log.WithError(err).Warn("Issues when patching StorageClass in primary cluster")
		return err
	}
	addRowOfTextOutput("[primary] OCS RBD Storage Class retain policy changed to retain")
	_, err = kubeConfigSecondary.typedClient.StorageV1().StorageClasses().Patch(context.TODO(), "ocs-storagecluster-ceph-rbd", types.JSONPatchType, payloadBytes, metav1.PatchOptions{})
	if err != nil {
		log.WithError(err).Warn("Issues when patching StorageClass in secondary cluster")
		return err
	}
	addRowOfTextOutput("[secondary] OCS RBD Storage Class retain policy changed to retain")

	return nil
}

func exchangeMirroringBootstrapSecrets(from, to *kubernetes.Clientset, fromCluster, toCluster string) error {
	result := cephv1.CephBlockPool{}
	err := from.RESTClient().
		Get().
		Namespace(ocsNamespace).
		Resource("cephblockpools.ceph.rook.io").
		AbsPath().
		Name("ocs-storagecluster-cephblockpool").
		VersionedParams(&metav1.GetOptions{}, scheme.ParameterCodec).
		Do(context.TODO()).Into(&result)
	if err != nil {
		log.WithError(err).Warn("Issues when getting CephBlockPool in secondary cluster")
		return err
	}
	secretName := result.Status.Info["rbdMirrorBootstrapPeerSecretName"]
	secret, err := from.CoreV1().Secrets(ocsNamespace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil {
		log.WithError(err).Warn("Issues when fetching secret token in secondary cluster")
		return err
	}
	secondaryPoolToken := secret.Data["token"]
	addRowOfTextOutput(fmt.Sprintf("[%s] Got Pool Mirror secret from secret %s", fromCluster, secretName))
	secondarySiteName := result.Status.MirroringInfo.Summary["site_name"].(string)
	addRowOfTextOutput(fmt.Sprintf("[%s] Got site name %s", fromCluster, secondarySiteName))

	primaryBootstrapSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: secondarySiteName,
		},
		Data: map[string][]byte{
			"token": secondaryPoolToken,
			"pool":  []byte("ocs-storagecluster-cephblockpool"),
		},
	}
	_, err = to.CoreV1().Secrets(ocsNamespace).Create(context.TODO(), &primaryBootstrapSecret, metav1.CreateOptions{})
	if err != nil {
		log.WithError(err).Warn("Issues when creating bootstrap secret in primary location")
		return err
	}
	addRowOfTextOutput(fmt.Sprintf("[%s] Created bootstrap secret", toCluster))
	return nil
}

func enableOMAPGenerator(client *kubernetes.Clientset, cluster string) error {
	configMapClient := client.CoreV1().ConfigMaps(ocsNamespace)

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
		return fmt.Errorf("failed with enabling the OMAP client on %s", cluster)
	}
	addRowOfTextOutput("Patched CM for OMAP Generator")
	addRowOfTextOutput("Waiting for OMAP generator container to appear")

	for {
		pods, err := client.CoreV1().Pods(ocsNamespace).List(context.TODO(), metav1.ListOptions{})
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
