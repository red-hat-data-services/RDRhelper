package main

import (
	"context"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var contextTimeout = 2 * time.Second

func askSeriousForFailover() {
	showModal("sure", "Are you sure you want to start a Failover?",
		[]string{"failOVER", "failBACK", "NO"},
		func(buttonIndex int, buttonLabel string) {
			pages.RemovePage("sure")
			switch buttonLabel {
			case "NO":
				return
			case "failOVER":
				showFailoverNamespaceList(kubeConfigPrimary, kubeConfigSecondary)
			case "failBACK":
				showFailoverNamespaceList(kubeConfigSecondary, kubeConfigPrimary)
			}
		},
	)
}

func showFailoverNamespaceList(from, to kubeAccess) {
	table := tview.NewTable().
		SetSelectable(true, false).
		SetSeparator(tview.Borders.Vertical).
		SetFixed(1, 1).
		SetDoneFunc(func(key tcell.Key) {
			if key == tcell.KeyEscape {
				pages.SwitchToPage("main")
				pages.RemovePage("failoverView")
			}
		})
	table.SetSelectedFunc(func(row int, column int) {
		namespaceCell := table.GetCell(row, 0)
		ref := namespaceCell.GetReference()
		if ref == nil || ref == false {
			namespaceCell.SetTextColor(tcell.ColorRed)
			namespaceCell.SetReference(true)
		} else {
			namespaceCell.SetTextColor(tcell.ColorWhite)
			namespaceCell.SetReference(false)
		}
	})

	tableFrame := tview.NewFrame(table).
		AddText("Select all namespaces with ENTER that should be replicated, then click CONTINUE\nYou can abort with the ESC key.", true, tview.AlignCenter, tcell.ColorWhite).
		SetBorderPadding(0, 0, 0, 0)

	populateTableWithResoreableNamespaces(table, to)

	form := tview.NewForm().
		SetButtonsAlign(tview.AlignCenter).
		AddButton("CONTINUE", func() {
			namespaces := gatherSelctedNamespaces(table)
			workOnFailoverWithNamespaces(from, to, namespaces)
		})

	container := tview.NewFlex().SetDirection(tview.FlexRow)
	container.AddItem(tableFrame, 0, 2, true).
		AddItem(form, 1, 1, false).
		SetBorder(true)

	pages.AddAndSwitchToPage("failoverView",
		container,
		true)
}

func gatherSelctedNamespaces(table *tview.Table) (selectedNamespaces []string) {
	selectedNamespaceMap := make(map[string]interface{})
	selectedRows := getSelectedRows(table, 0)
	for _, row := range selectedRows {
		namespace := table.GetCell(row, 0).Text
		selectedNamespaceMap[namespace] = nil
	}
	for namespace := range selectedNamespaceMap {
		selectedNamespaces = append(selectedNamespaces, namespace)
	}
	return
}

func populateTableWithResoreableNamespaces(table *tview.Table, cluster kubeAccess) {
	table.Clear()
	namespaces, err := getListOfRestoreableNamespaces(cluster)
	if err != nil {
		log.WithError(err).Warnf("Issues when collecting namespaces from the %s cluster for failover")
		return
	}
	row := 0
	for ns := range namespaces {
		table.SetCellSimple(row, 0, ns)
		row += 1
	}
}

func getListOfRestoreableNamespaces(cluster kubeAccess) (restoreableNamespace map[string]interface{}, err error) {
	restoreableNamespace = make(map[string]interface{})
	targetPVs, err := cluster.typedClient.CoreV1().PersistentVolumes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.WithError(err).Warn("Issues when listing pods for PVC list")
		return
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
		if pv.Spec.ClaimRef == nil {
			// The PV does not have the ClaimRef we need...
			continue
		}
		// We assume here for performance reasons, that all released RBD PVs are mirrored PVs
		restoreableNamespace[pv.Spec.ClaimRef.Namespace] = nil
	}
	return
}

func workOnFailoverWithNamespaces(from, to kubeAccess, namespaces []string) {
	failoverLog := tview.NewTextView().
		SetChangedFunc(func() {
			app.Draw()
		})
	pages.AddAndSwitchToPage("failoverAction", failoverLog, true)

	addRowOfTextOutput(failoverLog, "Trying to demote PVs in the %s cluster now...", from.name)
	addRowOfTextOutput(failoverLog, "This is OK to fail")
	err := changePVStatiInNamespaces(from, namespaces, "demote", failoverLog)
	if err != nil {
		addRowOfTextOutput(failoverLog, "Issues when demoting images in the %s cluster: %s", from.name, err)
	}
	addRowOfTextOutput(failoverLog, "Finished demoting PVs in the %s cluster!", from.name)
	addRowOfTextOutput(failoverLog, "Promoting PVs in the %s cluster now...", to.name)
	err = changePVStatiInNamespaces(to, namespaces, "promote", failoverLog)
	if err != nil {
		addRowOfTextOutput(failoverLog, "Issues when promoting images in the %s cluster: %s", to.name, err)
		addRowOfTextOutput(failoverLog, "Bailing out - please consult the log and try again later")
		return
	}
	addRowOfTextOutput(failoverLog, "Finished promoting PVs in the %s cluster!", to.name)

	if !checkForOADP(to) {
		// No OADP installed in target cluster, we are done
		addRowOfTextOutput(failoverLog, "OADP is not installed in the %s cluster - we are done now", to.name)
		return
	}

	addRowOfTextOutput(failoverLog, "Starting namespace recovery in the %s cluster!", to.name)
	err = setNamespacesToRestore(to, namespaces)
	if err != nil {
		showAlert(fmt.Sprintf("Issues when restoring namespaces with OADP: %s\n\nCheck the log for more information", err))
	}
	addRowOfTextOutput(failoverLog, "Recovery CR is created, waiting for Recovery to finish...")

	waitForRecoveryDone(to, failoverLog)

	failoverLog.SetDoneFunc(func(key tcell.Key) {
		pages.SwitchToPage("main")
		pages.RemovePage("failoverAction")
	})
	addRowOfTextOutput(failoverLog, "Recovery is finished")
	addRowOfTextOutput(failoverLog, "Failover from the %s to the %s cluster is done", from.name, to.name)
	addRowOfTextOutput(failoverLog, "Press Enter or Esc to return to the main menu")
}

func changePVStatiInNamespaces(cluster kubeAccess, namespaces []string, action string, failoverLog *tview.TextView) error {
	ctx, cancel := context.WithTimeout(context.Background(), contextTimeout)
	defer cancel()
	pvs, err := cluster.typedClient.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.WithError(err).Warn("Issues when listing pods for PVC list")
		return err
	}
	for _, pv := range pvs.Items {
		if pv.Spec.CSI == nil || pv.Spec.CSI.Driver != "openshift-storage.rbd.csi.ceph.com" {
			// not a CSI backed PV or not a Ceph RBD PV
			continue
		}
		if pv.Spec.ClaimRef == nil {
			// The PV does not have the ClaimRef we need...
			continue
		}
		if !stringInSliceBool(pv.Spec.ClaimRef.Namespace, namespaces) {
			continue
		}

		switch action {
		case "demote":
			err = demotePV(cluster, &pv)
		case "promote":
			err = promotePV(cluster, &pv)
		}
		if err != nil {
			addRowOfTextOutput(failoverLog, "  ❌ failed to change mirror status for PV %s", pv.Name)
		}
		addRowOfTextOutput(failoverLog, "  ✔️ mirror status changed for PV %s", pv.Name)
	}
	return nil
}
