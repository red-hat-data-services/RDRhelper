package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/pkg/errors"
	"github.com/rivo/tview"
	"github.com/tidwall/sjson"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var primaryPVCs, secondaryPVCs *tview.Table
var pvcStatusFrame *tview.Frame
var oadpAvailable = false

func setPVCViewPage(table *tview.Table, cluster kubeAccess) {
	// Check if the tools Pod is available
	_, err := getToolsPod(cluster)
	if err != nil {
		showAlert("The Tools Pod is not ready. Please check that the install has completed successfully.")
	}
	oadpAvailable = checkForOADP(currentCluster)

	table = tview.NewTable().
		SetSelectable(true, false).
		SetSeparator(tview.Borders.Vertical).
		SetFixed(1, 1).
		SetDoneFunc(func(key tcell.Key) {
			if key == tcell.KeyEscape {
				pages.SwitchToPage("main")
				pages.RemovePage("pvcView")
			}
		}).
		SetSelectedFunc(func(row int, column int) {
			namespaceCell := table.GetCell(row, 0)
			pvcCell := table.GetCell(row, 1)
			ref := pvcCell.GetReference()
			if ref == nil || ref == false {
				pvcCell.SetTextColor(tcell.ColorRed)
				namespaceCell.SetTextColor(tcell.ColorRed)
				pvcCell.SetReference(true)
			} else {
				pvcCell.SetTextColor(tcell.ColorWhite)
				namespaceCell.SetTextColor(tcell.ColorWhite)
				pvcCell.SetReference(false)
			}
		})

	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Rune() {
		case 'a':
			selectAllFromTable(table, true)
		case 'n':
			row, _ := table.GetSelection()
			namespace := table.GetCell(row, 0).Text
			selectAllFromNamespaceFromTable(table, true, namespace)
		case 'x':
			selectAllFromTable(table, false)
		case 'r':
			setPVStati(cluster, true, table)
		case 'u':
			setPVStati(cluster, false, table)
		case 's':
			go populatePVCTable(table, cluster)
		case 'i':
			row, _ := table.GetSelection()
			pvcStatus := table.GetCell(row, 2).Text
			if pvcStatus == "inactive" {
				showAlert("mirroring is not enabled on this PVC")
				break
			}
			pvReference := table.GetCell(row, 0).GetReference()
			pv := pvReference.(corev1.PersistentVolume)
			showMirrorInfo(cluster, &pv)
		}
		return event
	})

	helpText := tview.NewTextView().SetText(`
Keyboard keys:
General actions
	(s) Refresh PVC table
	(i) Show PVCs mirror info
Selection
	(a) Select all
	(n) Select all in namespace
	(x) Deselect all
	(ENTER) (De-)Select single PVC
Actions on Selection
	(r) Activate for replication
	(u) Deactivate for replication
	`)
	helperTextFrame := tview.NewFrame(helpText).
		SetBorders(0, 1, 0, 0, 3, 0)

	container := tview.NewFlex().SetDirection(tview.FlexColumn)
	container.AddItem(table, 0, 2, true)
	container.AddItem(helperTextFrame, 0, 1, false)

	pvcStatusFrame = tview.NewFrame(container)
	pvcStatusFrame.SetBorderPadding(0, 0, 0, 0)
	pvcInfoFrame := tview.NewFrame(pvcStatusFrame).
		AddText(fmt.Sprintf("PVCs in %s cluster", cluster.name), true, tview.AlignCenter, tcell.ColorWhite)
	pvcInfoFrame.SetBorder(true)

	pages.AddAndSwitchToPage("pvcView",
		pvcInfoFrame,
		true)
	go populatePVCTable(table, cluster)
}

func selectAllFromTable(table *tview.Table, selected bool) {
	for row := 1; row < table.GetRowCount(); row++ {
		for column := 0; column < 2; column++ {
			cell := table.GetCell(row, column)
			if selected {
				cell.SetTextColor(tcell.ColorRed)
			} else {
				cell.SetTextColor(tcell.ColorWhite)
			}
			table.GetCell(row, 1).SetReference(selected)
		}
	}
}
func selectAllFromNamespaceFromTable(table *tview.Table, selected bool, namespace string) {
	for row := 1; row < table.GetRowCount(); row++ {
		if table.GetCell(row, 0).Text != namespace {
			continue
		}
		for column := 0; column < 2; column++ {
			currentCell := table.GetCell(row, column)
			if selected {
				currentCell.SetTextColor(tcell.ColorRed)
			} else {
				currentCell.SetTextColor(tcell.ColorWhite)
			}
			table.GetCell(row, 1).SetReference(selected)
		}
	}
}

// getSelectedRows Returns the row indexes that are selected
func getSelectedRows(table *tview.Table) []int {
	result := []int{}
	for row := 1; row < table.GetRowCount(); row++ {
		pv := table.GetCell(row, 1)
		if pv.GetReference() != nil && pv.GetReference().(bool) {
			result = append(result, row)
		}
	}
	return result
}

// getActiveRows Returns the row indexes with active mirroring
func getActiveRows(table *tview.Table) []int {
	result := []int{}
	for row := 1; row < table.GetRowCount(); row++ {
		statusCell := table.GetCell(row, 2)
		if statusCell.Text == "active" {
			result = append(result, row)
		}
	}
	return result
}

// setPVStati sets the PV status of selected rows to either active or inactive
func setPVStati(cluster kubeAccess, enable bool, table *tview.Table) {
	statusText := "active"
	statusColor := tcell.ColorGreen
	if !enable {
		statusText = "inactive"
		statusColor = tcell.ColorRed
	}
	for _, row := range getSelectedRows(table) {
		statusCell := table.GetCell(row, 2)
		if statusCell.Text == statusText {
			// PV already in desired state
			continue
		}
		pvReference := table.GetCell(row, 0).GetReference()
		if pvReference == nil {
			log.WithField("row", row).Warn("Could not get PV reference for row")
			continue
		}
		pv := pvReference.(corev1.PersistentVolume)
		err := setMirrorStatus(cluster, &pv, enable)
		if err != nil {
			log.WithError(err).WithField("pvName", pv.Name).Warn("Could not change PV mirror status")
			continue
		}
		table.SetCell(row, 2, tview.NewTableCell(statusText).SetTextColor(statusColor))
	}
	ensureActivePVCsBackuped(cluster, table)
}

func ensureActivePVCsBackuped(cluster kubeAccess, table *tview.Table) {
	// Collect a list of unique namespace names
	// that contain PVCs with active mirroring
	namespaceMap := make(map[string]struct{})
	var namespaces []string
	for _, row := range getActiveRows(table) {
		namespace := table.GetCell(row, 0).Text
		namespaceMap[namespace] = struct{}{}
	}
	for namespace := range namespaceMap {
		namespaces = append(namespaces, namespace)
	}
	setNamespacesToBackup(cluster, namespaces)
}

func setNamespacesToBackup(cluster kubeAccess, namespaces []string) {
	if !oadpAvailable {
		return
	}
	snapshotVolumeSetting := false
	backupCR := velerov1.Backup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "velero.io/v1",
			Kind:       "Backup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "regional-dr-backup",
			Namespace: "oadp-operator",
		},
		Spec: velerov1.BackupSpec{
			IncludedNamespaces: namespaces,
			ExcludedResources:  []string{"imagetags.image.openshift.io"},
			SnapshotVolumes:    &snapshotVolumeSetting,
		},
	}

	if err := velerov1.AddToScheme(cluster.controllerClient.Scheme()); err != nil {
		log.WithError(err).Warn("[%s] Issues when adding velero schemas", cluster.name)
	}

	backupJSON, err := json.Marshal(backupCR)
	if err != nil {
		log.WithError(err).Warn("[%s] Issues when converting Backup CR to JSON")
		showAlert("The OADP Backup plan might not have been updated properly")
	}

	backupPatchedJSON, _ := sjson.Delete(string(backupJSON), "spec.ttl")

	err = cluster.controllerClient.Patch(context.TODO(),
		&backupCR,
		client.RawPatch(types.ApplyPatchType, []byte(backupPatchedJSON)),
		&client.PatchOptions{FieldManager: "RDRhelper"})

	if err != nil {
		log.WithError(err).Warnf("[%s] Issues when applying Backup CR", cluster.name)
		showAlert("The OADP Backup plan might not have been updated properly")
	}
}

func populatePVCTable(table *tview.Table, cluster kubeAccess) error {

	pvcStatusFrame.AddText(
		"Fetching list of PVCs and their mirroring status",
		true,
		tview.AlignCenter,
		tcell.ColorWhite,
	)

	table.Clear()

	table.
		SetCell(0, 0, &tview.TableCell{Text: "Namespace", NotSelectable: true, Color: tcell.ColorYellow}).
		SetCell(0, 1, &tview.TableCell{Text: "PVC", NotSelectable: true, Color: tcell.ColorYellow}).
		SetCell(0, 2, &tview.TableCell{Text: "Replication status", NotSelectable: true, Color: tcell.ColorYellow})

	pvs, err := cluster.typedClient.CoreV1().PersistentVolumes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.WithError(err).Warn("Issues when listing pods for PVC list")
		return err
	}

	currentRow := 1
	for _, pv := range pvs.Items {
		pvc := pv.Spec.ClaimRef
		if pvc == nil {
			// This happens for unbound PVs, we skip those
			continue
		}
		mirroringEnabled, err := checkMirrorStatus(cluster, &pv)
		if err != nil {
			log.WithField("PV", pv.Name).WithError(err).Warn("Issues when fetching mirror status")
			continue
		}
		table.SetCell(currentRow, 0, &tview.TableCell{
			Text:      pvc.Namespace,
			Expansion: 1,
			Color:     tcell.ColorWhite,
			Reference: pv,
		})
		table.SetCell(currentRow, 1, &tview.TableCell{
			Text:      pvc.Name,
			Expansion: 2,
			Color:     tcell.ColorWhite,
		})
		if mirroringEnabled {
			table.SetCell(currentRow, 2, &tview.TableCell{
				Text:      "active",
				Expansion: 1,
				Color:     tcell.ColorGreen,
			})
		} else {
			table.SetCell(currentRow, 2, &tview.TableCell{
				Text:      "inactive",
				Expansion: 1,
				Color:     tcell.ColorRed,
			})
		}
		currentRow += 1
		app.Draw()
	}

	pvcStatusFrame.Clear().AddText(
		"Fetching list of PVCs completed",
		true,
		tview.AlignCenter,
		tcell.ColorGreen,
	)
	app.Draw()

	time.Sleep(5 * time.Second)

	pvcStatusFrame.Clear()
	app.Draw()

	return nil
}

func checkMirrorStatus(cluster kubeAccess, pv *corev1.PersistentVolume) (bool, error) {
	rbdName, poolName, err := getRBDInfoFromPV(pv)
	if err != nil {
		return false, err
	}
	command := fmt.Sprintf("rbd -p %s mirror image status %s", poolName, rbdName)
	_, stderr, err := executeInToolbox(cluster, command)
	// Catch error later, since exit code 22 is thrown when image is not enabled
	if strings.Contains(stderr, "mirroring not enabled on the image") {
		return false, nil
	}
	if err != nil {
		return false, errors.New("could not get RBD mirror info from PV")
	}
	return true, nil
}

func setMirrorStatus(cluster kubeAccess, pv *corev1.PersistentVolume, enable bool) error {
	rbdName, poolName, err := getRBDInfoFromPV(pv)
	if err != nil {
		return err
	}
	var action string
	if enable {
		action = fmt.Sprintf("enable %s snapshot", rbdName)
	} else {
		action = fmt.Sprintf("disable %s", rbdName)
	}
	command := fmt.Sprintf("rbd -p %s mirror image %s", poolName, action)
	_, _, err = executeInToolbox(cluster, command)
	if err != nil {
		return errors.Wrapf(err, "could not change RBD mirror status from PV. Command: %s", command)
	}
	return nil
}

func showMirrorInfo(cluster kubeAccess, pv *corev1.PersistentVolume) error {
	rbdName, poolName, err := getRBDInfoFromPV(pv)
	if err != nil {
		return err
	}
	command := fmt.Sprintf("rbd -p %s mirror image status %s", poolName, rbdName)
	stdout, stderr, err := executeInToolbox(cluster, command)
	// Catch error later, since exit code 22 is thrown when image is not enabled
	if strings.Contains(stderr, "mirroring not enabled on the image") {
		showAlert("mirroring is not enabled on this PVC")
		return errors.New("mirroring is not enabled on this PVC")
	}
	if err != nil {
		showAlert("could not get RBD mirror info from PV")
		return errors.Wrapf(err, "could not get RBD mirror info from PV")
	}
	buttons := make(map[string]func())
	buttons["Close"] = func() { pages.RemovePage("mirrorInfo") }
	showInfo("mirrorInfo", stdout, buttons)
	return nil
}

// getRBDInfoFromPV returns (rbdName, PoolName, nil) or ("", "", error)
func getRBDInfoFromPV(pv *corev1.PersistentVolume) (string, string, error) {
	if pv == nil || pv.Spec.CSI == nil || pv.Spec.CSI.VolumeAttributes == nil {
		return "", "", errors.New("PV does not contain the required information")
	}
	rbdName := pv.Spec.CSI.VolumeAttributes["imageName"]
	poolName := pv.Spec.CSI.VolumeAttributes["pool"]
	if rbdName == "" || poolName == "" {
		return "", "", errors.New("could not get RBD or pool name from PV")
	}
	return rbdName, poolName, nil
}
