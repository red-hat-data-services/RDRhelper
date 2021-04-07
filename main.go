package main

import (
	"os"

	"github.com/rivo/tview"
	"github.com/sirupsen/logrus"
)

var pages = tview.NewPages()
var app = tview.NewApplication()
var logFile *os.File
var log = logrus.New()

func init() {
	log.SetFormatter(&logrus.TextFormatter{
		DisableColors:    false,
		FullTimestamp:    false,
		DisableTimestamp: true,
	})
}

func main() {
	logFile, err := os.Create("asyncDRhelper.log")
	if err != nil {
		panic(err)
	}
	log.Out = logFile

	pages.AddAndSwitchToPage("main",
		tview.NewList().
			AddItem("Install", "Install AsyncDR", '1', func() { showBlockPoolChoice() }).
			AddItem("Verify Install", "Verify correct AsyncDR installation", '2', func() { pages.ShowPage("notImplemented") }).
			AddItem("Configure Primary", "Configure PVs for DR on the primary side", '3', func() { setPVCViewPage(); pages.SwitchToPage("configurePrimary") }).
			AddItem("Configure Secondary", "Configure PVs for DR on the secondary side", '4', nil).
			AddItem("Configure Kubeconfigs", "Configure which Kubeconfigs to use for primary and secondary locations", '5', func() { showConfigPage() }).
			AddItem("Failover / Failback", "Failover to secondary or Failback to primary location", '9', func() {
				log.Info("Selected 9")
				status, err := getCephStatus(kubeConfigPrimary)
				if err != nil {
					log.WithError(err).Warn("ISSUE")
				} else {
					log.WithField("status", status).Infof("Got ceph status!")
				}
			}).
			AddItem("Quit", "Press to exit app", 'q', func() { app.Stop() }),
		true)

	pages.AddPage("notImplemented",
		tview.NewModal().
			SetText("This is not implemented yet").
			AddButtons([]string{"OK"}).
			SetDoneFunc(func(buttonIndex int, buttonLabel string) { pages.HidePage("notImplemented") }),
		false,
		false)

	frame = tview.NewFrame(pages)
	updateFrame()

	readConfig()

	if err := app.SetRoot(frame, true).Run(); err != nil {
		panic(err)
	}
}

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
