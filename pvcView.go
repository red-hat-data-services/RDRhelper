package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var primaryPVCs *tview.Table

func init() {
	log.SetOutput(logFile)
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
			activateSelected(primaryPVCs)
		case 'u':
			deactivateSelected(primaryPVCs)
		}
		return event
	})

	helpText := tview.NewTextView().SetText(`
Keyboard keys:
Selection
	(a) Select all
	(n) Select all in namespace
	(x) Deselect all
Actions on Selection
	(s) Sync PV(C) CR to secondary
	(r) Activate for replication
	(u) Deactivate for replication
	`)

	container := tview.NewFlex().SetDirection(tview.FlexColumn)
	container.AddItem(primaryPVCs, 0, 2, true)
	container.AddItem(helpText, 0, 1, false)

	pages.AddPage("configurePrimary",
		container,
		true,
		false)

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
			cell.SetReference(selected)
		}
	}
}
func selectAllFromNamespaceFromTable(table *tview.Table, selected bool, namespace string) {
	for row := 1; row < table.GetRowCount(); row++ {
		if table.GetCell(row, 0).Text != namespace {
			continue
		}
		for column := 0; column < 2; column++ {
			cell := table.GetCell(row, column)
			if selected {
				cell.SetTextColor(tcell.ColorRed)
			} else {
				cell.SetTextColor(tcell.ColorWhite)
			}
			cell.SetReference(selected)
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

func activateSelected(table *tview.Table) {
	for _, row := range getSelectedRows(table) {
		primaryPVCs.SetCell(row, 2, tview.NewTableCell("active").SetTextColor(tcell.ColorGreen))
	}
}
func deactivateSelected(table *tview.Table) {
	for _, row := range getSelectedRows(table) {
		primaryPVCs.SetCell(row, 2, tview.NewTableCell("inactive").SetTextColor(tcell.ColorRed))
	}
}

func populatePrimaryPVCs() error {

	primaryPVCs.Clear()

	primaryPVCs.
		SetCell(0, 0, tview.NewTableCell("Namespace").SetSelectable(false).SetTextColor(tcell.ColorYellow)).
		SetCell(0, 1, tview.NewTableCell("PVC").SetSelectable(false).SetTextColor(tcell.ColorYellow)).
		SetCell(0, 2, tview.NewTableCell("Replication status").SetSelectable(false).SetTextColor(tcell.ColorYellow))

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
		primaryPVCs.SetCellSimple(currentRow, 0, pvc.Namespace)
		primaryPVCs.SetCellSimple(currentRow, 1, pvc.Name)
		if mirroringEnabled {
			primaryPVCs.SetCell(currentRow, 2, tview.NewTableCell("active").SetTextColor(tcell.ColorGreen))
		} else {
			primaryPVCs.SetCell(currentRow, 2, tview.NewTableCell("inactive").SetTextColor(tcell.ColorRed))
		}
		currentRow += 1
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
