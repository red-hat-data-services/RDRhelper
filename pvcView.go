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

func setPVCViewPage(table *tview.Table, cluster kubeAccess) {
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
			activateSelected(cluster, table)
		case 'u':
			deactivateSelected(cluster, table)
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

func activateSelected(cluster kubeAccess, table *tview.Table) {
	for _, row := range getSelectedRows(table) {
		statusCell := table.GetCell(row, 2)
		if statusCell.Text == "active" {
			// PV already active
			continue
		}
		pvReference := table.GetCell(row, 0).GetReference()
		if pvReference == nil {
			log.WithField("row", row).Warn("Could not get PV reference for row")
		}
		pv := pvReference.(corev1.PersistentVolume)
		err := setMirrorStatus(cluster, &pv, true)
		if err != nil {
			log.WithError(err).Warn("Could not change PV mirror status")
			return
		}
		table.SetCell(row, 2, tview.NewTableCell("active").SetTextColor(tcell.ColorGreen))
	}
}
func deactivateSelected(cluster kubeAccess, table *tview.Table) {
	for _, row := range getSelectedRows(table) {
		statusCell := table.GetCell(row, 2)
		if statusCell.Text == "inactive" {
			// PV already inactive
			continue
		}
		pvReference := table.GetCell(row, 0).GetReference()
		if pvReference == nil {
			log.WithField("row", row).Warn("Could not get PV reference for row")
		}
		pv := pvReference.(corev1.PersistentVolume)
		err := setMirrorStatus(cluster, &pv, false)
		if err != nil {
			log.WithError(err).Warn("Could not change PV mirror status")
			return
		}
		table.SetCell(row, 2, tview.NewTableCell("inactive").SetTextColor(tcell.ColorRed))
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
	if pv.Spec.CSI == nil {
		return false, errors.New("PV has no CSI spec")
	}
	rbdName := pv.Spec.CSI.VolumeAttributes["imageName"]
	if rbdName == "" {
		return false, errors.New("could not get RBD name from PV")
	}
	command := fmt.Sprintf("rbd -p %s mirror image status %s", "ocs-storagecluster-cephblockpool", rbdName)
	_, stderr, err := executeInToolbox(cluster, command)
	// Catch error later, since exit code 22 is thrown when image is not enabled
	log.Infof("DEBUG:: %s", stderr)
	if strings.Contains(stderr, "mirroring not enabled on the image") {
		return false, nil
	}
	if err != nil {
		return false, errors.New("could not get RBD mirror info from PV")
	}
	return true, nil
}

func setMirrorStatus(cluster kubeAccess, pv *corev1.PersistentVolume, enable bool) error {
	if pv.Spec.CSI == nil {
		return errors.New("PV has no CSI spec")
	}
	rbdName := pv.Spec.CSI.VolumeAttributes["imageName"]
	if rbdName == "" {
		return errors.New("could not get RBD name from PV")
	}
	var action string
	if enable {
		action = fmt.Sprintf("enable %s snapshot", rbdName)
	} else {
		action = fmt.Sprintf("disable %s", rbdName)
	}
	command := fmt.Sprintf("rbd -p %s mirror image %s", "ocs-storagecluster-cephblockpool", action)
	_, _, err := executeInToolbox(cluster, command)
	if err != nil {
		return errors.Wrapf(err, "could not change RBD mirror status from PV. Command: %s", command)
	}
	return nil
}

func showMirrorInfo(cluster kubeAccess, pv *corev1.PersistentVolume) error {
	if pv == nil || pv.Spec.CSI == nil {
		return errors.New("PV has no CSI spec")
	}
	rbdName := pv.Spec.CSI.VolumeAttributes["imageName"]
	if rbdName == "" {
		return errors.New("could not get RBD name from PV")
	}
	command := fmt.Sprintf("rbd -p %s mirror image status %s", "ocs-storagecluster-cephblockpool", rbdName)
	stdout, stderr, err := executeInToolbox(cluster, command)
	// Catch error later, since exit code 22 is thrown when image is not enabled
	log.Infof("DEBUG:: %s", stderr)
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
