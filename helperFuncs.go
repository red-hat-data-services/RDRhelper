package main

import (
	"context"

	"github.com/pkg/errors"
	"github.com/rivo/tview"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func showModal(modalTitle string, modalText string, buttons []string, doneFunc func(buttonIndex int, buttonLabel string)) {
	modal := tview.NewModal().
		SetText(modalText).
		AddButtons(buttons).
		SetDoneFunc(doneFunc)

	pages.AddPage(modalTitle,
		modal,
		false,
		false,
	)
	pages.ShowPage(modalTitle)
}

func showAlert(alertText string) {
	showModal("alert", alertText, []string{"OK"}, func(buttonIndex int, buttonLabel string) { pages.RemovePage("alert") })
}

func showInfo(pageTitle string, information string, buttons map[string]func()) {
	InfoBox := tview.NewTextView().SetText(information)
	form := tview.NewForm()
	layout := tview.NewFlex()
	layout.
		AddItem(InfoBox, 0, 3, false).
		AddItem(form, 0, 1, true).
		SetDirection(tview.FlexRow)
	layout.SetBorder(true)
	form.SetButtonsAlign(tview.AlignCenter)
	for button, selected := range buttons {
		form.AddButton(button, selected)
	}
	pages.AddAndSwitchToPage(pageTitle,
		layout,
		true,
	)
}

func checkForOADP(cluster kubeAccess) (OADPpresent bool) {
	OADPpresent = false
	// Check if OADP is available
	podlist, err := cluster.typedClient.CoreV1().Pods("oadp-operator").List(context.TODO(), metav1.ListOptions{LabelSelector: "component=velero"})
	if err == nil && len(podlist.Items) > 0 {
		OADPpresent = true
	}
	return
}

func stringInSlice(needle string, stack []string) (int, error) {
	for i, hay := range stack {
		if hay == needle {
			return i, nil
		}
	}
	return -1, errors.Errorf("Could not find %s in the slice", needle)
}
func stringInSliceBool(needle string, stack []string) bool {
	_, err := stringInSlice(needle, stack)
	return err == nil
}
