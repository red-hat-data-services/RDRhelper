package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/pkg/errors"
	"github.com/rivo/tview"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var primaryPVCs, secondaryPVCs *tview.Table
var pvcStatusFrame *tview.Frame

func setPVCViewPage(table *tview.Table, currentCluster, otherCluster kubeAccess) {
	// Check if the tools Pod is available
	_, err := getToolsPod(currentCluster)
	if err != nil {
		showAlert("The Tools Pod is not ready. Please check that the install has completed successfully.")
		return
	}

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
			setPVStati(currentCluster, otherCluster, true, table)
		case 'u':
			setPVStati(currentCluster, otherCluster, false, table)
		case 's':
			go populatePVCTable(table, currentCluster)
		case 'i':
			row, _ := table.GetSelection()
			pvcStatus := table.GetCell(row, 2).Text
			if pvcStatus == "inactive" {
				showAlert("mirroring is not enabled on this PVC")
				break
			}
			pvReference := table.GetCell(row, 0).GetReference()
			pv := pvReference.(corev1.PersistentVolume)
			showMirrorInfo(currentCluster, &pv)
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
		AddText(fmt.Sprintf("PVCs in %s cluster", currentCluster.name), true, tview.AlignCenter, tcell.ColorWhite)
	pvcInfoFrame.SetBorder(true)

	pages.AddAndSwitchToPage("pvcView",
		pvcInfoFrame,
		true)
	go populatePVCTable(table, currentCluster)
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
// column is the column in which the reference bool will be expected
func getSelectedRows(table *tview.Table, column int) []int {
	result := []int{}
	for row := 1; row < table.GetRowCount(); row++ {
		referenceCell := table.GetCell(row, column)
		if referenceCell.GetReference() != nil && referenceCell.GetReference().(bool) {
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
func setPVStati(currentCluster, otherCluster kubeAccess, enable bool, table *tview.Table) {
	statusText := "active"
	statusColor := tcell.ColorGreen
	if !enable {
		statusText = "inactive"
		statusColor = tcell.ColorRed
	}
	for _, row := range getSelectedRows(table, 1) {
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
		err := setMirrorStatus(currentCluster, &pv, enable)
		if err != nil {
			log.WithError(err).WithField("pvName", pv.Name).Warn("Could not change PV mirror status")
			continue
		}
		table.SetCell(row, 2, tview.NewTableCell(statusText).SetTextColor(statusColor))
	}
	ensureActivePVCsBackuped(currentCluster, table)
	syncPVs(currentCluster, otherCluster)
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

// syncPVs ensures that PVs in the from cluster are present in the to cluster
// it also tries to clean up old PVs in the to cluster that are not migrated any more
func syncPVs(from, to kubeAccess) error {
	var mirroredPVs []corev1.PersistentVolume
	pvs, err := from.typedClient.CoreV1().PersistentVolumes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.WithError(err).Warn("Issues when listing pods for PVC list")
		return err
	}
	for _, pv := range pvs.Items {
		pvc := pv.Spec.ClaimRef
		if pvc == nil {
			// This happens for unbound PVs, we skip those
			continue
		}
		mirroringEnabled, err := checkMirrorStatus(from, &pv)
		if err != nil {
			log.WithField("PV", pv.Name).WithError(err).Warn("Issues when fetching mirror status")
			continue
		}
		if !mirroringEnabled {
			continue
		}
		mirroredPVs = append(mirroredPVs, pv)
	}
	targetPVs, err := to.typedClient.CoreV1().PersistentVolumes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.WithError(err).Warn("Issues when listing pods for PVC list")
		return err
	}
	// Filter for PVs in Released state, these are most likely our mirrored PVs
	// field-selector does not support status.phase for PVs :/
	for _, pv := range targetPVs.Items {
		if pv.Status.Phase != "Released" {
			continue
		}
		if pv.Spec.CSI == nil || pv.Spec.CSI.Driver != "openshift-storage.rbd.csi.ceph.com" {
			// not a CSI backed PV or not a Ceph RBD PV
			continue
		}
		mirroringEnabled, err := checkMirrorStatus(to, &pv)
		if err != nil {
			log.WithField("PV", pv.Name).WithError(err).Warn("Issues when fetching mirror status")
			continue
		}
		if !mirroringEnabled {
			// If the PV is in released state and backed by Ceph-RBD,
			// it is most likely dangling (not mirrored any more) and we remove it
			err = to.typedClient.CoreV1().PersistentVolumes().Delete(context.TODO(), pv.Name, metav1.DeleteOptions{})
			if err != nil {
				log.WithField("PV", pv.Name).WithError(err).Warnf("Issues when deleting dangling PV from %s cluster", to.name)
			}
			continue
		}
		// Do something finally!
		// If the PV is in the list of mirroredPVs, this PV is already synced, remove it from the list
		// If the PV is NOT in the mirroredPV list, it gets removed from the to cluster
		if present, index := PVInSlice(pv, mirroredPVs); present {
			mirroredPVs = RemovePVFromSlice(mirroredPVs, index)
		} else {
			// We should not reach this, since if the image is NOT mirrored, it will also not be mirrored on the target cluster
			err = to.typedClient.CoreV1().PersistentVolumes().Delete(context.TODO(), pv.Name, metav1.DeleteOptions{})
			if err != nil {
				log.WithField("PV", pv.Name).WithError(err).Warnf("Issues when deleting dangling PV from %s cluster", to.name)
				continue
			}
		}
	}
	// Once we reach this point, the mirroredPVs slice only contains PVs that are mirrored on the primary, but not yet synced on the secondary cluster
	log.Infof("Syncing %d PVs to the %s cluster", len(mirroredPVs), to.name)
	failureDuringCreation := false
	for _, pv := range mirroredPVs {
		pv.ResourceVersion = ""
		_, err = to.typedClient.CoreV1().PersistentVolumes().Create(context.TODO(), &pv, metav1.CreateOptions{})
		if err != nil {
			failureDuringCreation = true
			log.WithField("PV", pv.Name).WithError(err).Warnf("Issues when creating PV in the %s cluster", to.name)
			continue
		}
	}
	if failureDuringCreation {
		showAlert(fmt.Sprintf("There were errors when creating PVs in the %s cluster. Please check the log for more information", to.name))
	}

	return nil
}

func PVInSlice(pv corev1.PersistentVolume, slice []corev1.PersistentVolume) (bool, int) {
	for index, slicePV := range slice {
		if pv.Name == slicePV.Name {
			return true, index
		}
	}
	return false, -1
}

func RemovePVFromSlice(slice []corev1.PersistentVolume, index int) []corev1.PersistentVolume {
	return append(slice[:index], slice[index+1:]...)
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
		SetCell(0, 0, &tview.TableCell{Text: "Namespace", NotSelectable: true, Color: tcell.ColorYellow, BackgroundColor: tcell.ColorBlack}).
		SetCell(0, 1, &tview.TableCell{Text: "PVC", NotSelectable: true, Color: tcell.ColorYellow, BackgroundColor: tcell.ColorBlack}).
		SetCell(0, 2, &tview.TableCell{Text: "Replication status", NotSelectable: true, Color: tcell.ColorYellow, BackgroundColor: tcell.ColorBlack})

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
			Text:            pvc.Namespace,
			Expansion:       1,
			Color:           tcell.ColorWhite,
			BackgroundColor: tcell.ColorBlack,
			Reference:       pv,
		})
		table.SetCell(currentRow, 1, &tview.TableCell{
			Text:            pvc.Name,
			Expansion:       2,
			Color:           tcell.ColorWhite,
			BackgroundColor: tcell.ColorBlack,
		})
		if mirroringEnabled {
			table.SetCell(currentRow, 2, &tview.TableCell{
				Text:            "active",
				Expansion:       1,
				Color:           tcell.ColorGreen,
				BackgroundColor: tcell.ColorBlack,
			})
		} else {
			table.SetCell(currentRow, 2, &tview.TableCell{
				Text:            "inactive",
				Expansion:       1,
				Color:           tcell.ColorRed,
				BackgroundColor: tcell.ColorBlack,
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
