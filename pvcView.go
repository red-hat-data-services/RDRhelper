package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/pkg/errors"
	"github.com/rivo/tview"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var primaryPVCs *tview.Table

func init() {
	log.SetOutput(logFile)
}

func setPVCViewPage() {
	primaryPVCs = tview.NewTable().
		SetSelectable(true, false).
		SetSeparator(tview.Borders.Vertical).
		SetFixed(1, 1).
		SetDoneFunc(func(key tcell.Key) {
			if key == tcell.KeyEscape {
				pages.SwitchToPage("main")
			}
		}).
		SetSelectedFunc(func(row int, column int) {
			namespaceCell := primaryPVCs.GetCell(row, 0)
			pvcCell := primaryPVCs.GetCell(row, 1)
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

	primaryPVCs.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Rune() {
		case 'a':
			selectAllFromTable(primaryPVCs, true)
		case 'n':
			row, _ := primaryPVCs.GetSelection()
			namespace := primaryPVCs.GetCell(row, 0).Text
			selectAllFromNamespaceFromTable(primaryPVCs, true, namespace)
		case 'x':
			selectAllFromTable(primaryPVCs, false)
		case 'r':
			activateSelected(kubeConfigPrimary, primaryPVCs)
		case 'u':
			deactivateSelected(kubeConfigPrimary, primaryPVCs)
		case 's':
			go populatePrimaryPVCs()
		case 'i':
			row, _ := primaryPVCs.GetSelection()
			pvcStatus := primaryPVCs.GetCell(row, 2).Text
			if pvcStatus == "inactive" {
				showAlert("mirroring is not enabled on this PVC")
				break
			}
			pvReference := primaryPVCs.GetCell(row, 0).GetReference()
			pv := pvReference.(corev1.PersistentVolume)
			showMirrorInfo(kubeConfigPrimary, &pv)
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
Actions on Selection
	(r) Activate for replication
	(u) Deactivate for replication
	`)
	helperTextFrame := tview.NewFrame(helpText).
		SetBorders(0, 1, 0, 0, 3, 0)

	container := tview.NewFlex().SetDirection(tview.FlexColumn)
	container.AddItem(primaryPVCs, 0, 2, true)
	container.AddItem(helperTextFrame, 0, 1, false)

	pages.AddPage("configurePrimary",
		container,
		true,
		false)
	go populatePrimaryPVCs()
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

func populatePrimaryPVCs() error {

	primaryPVCs.Clear()

	primaryPVCs.
		SetCell(0, 0, &tview.TableCell{Text: "Namespace", NotSelectable: true, Color: tcell.ColorYellow}).
		SetCell(0, 1, &tview.TableCell{Text: "PVC", NotSelectable: true, Color: tcell.ColorYellow}).
		SetCell(0, 2, &tview.TableCell{Text: "Replication status", NotSelectable: true, Color: tcell.ColorYellow})

	pvs, err := kubeConfigPrimary.typedClient.CoreV1().PersistentVolumes().List(context.TODO(), metav1.ListOptions{})
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
		mirroringEnabled, err := checkMirrorStatus(kubeConfigPrimary, &pv)
		if err != nil {
			log.WithField("PV", pv.Name).WithError(err).Warn("Issues when fetching mirror status")
			continue
		}
		primaryPVCs.SetCell(currentRow, 0, &tview.TableCell{
			Text:      pvc.Namespace,
			Expansion: 1,
			Color:     tcell.ColorWhite,
			Reference: pv,
		})
		primaryPVCs.SetCell(currentRow, 1, &tview.TableCell{
			Text:      pvc.Name,
			Expansion: 2,
			Color:     tcell.ColorWhite,
		})
		if mirroringEnabled {
			primaryPVCs.SetCell(currentRow, 2, &tview.TableCell{
				Text:      "active",
				Expansion: 1,
				Color:     tcell.ColorGreen,
			})
		} else {
			primaryPVCs.SetCell(currentRow, 2, &tview.TableCell{
				Text:      "inactive",
				Expansion: 1,
				Color:     tcell.ColorRed,
			})
		}
		currentRow += 1
		app.Draw()
	}

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
