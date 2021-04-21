package main

import (
	"os"

	"github.com/rivo/tview"
	"github.com/sirupsen/logrus"
)

var pages = tview.NewPages()
var app = tview.NewApplication()
var log = logrus.New()
var mainMenu = tview.NewList()

func init() {
	log.SetFormatter(&logrus.TextFormatter{
		DisableColors: false,
		FullTimestamp: false,
	})
}

func main() {
	logFile, err := os.OpenFile("asyncDRhelper.log",
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		panic(err)
	}
	log.Out = logFile

	pages.SetChangedFunc(pagesChangedFunc)

	pages.AddAndSwitchToPage("main",
		mainMenu,
		true)

	appFrame = tview.NewFrame(pages)

	readConfig()

	if err := app.SetRoot(appFrame, true).Run(); err != nil {
		panic(err)
	}
}

func pagesChangedFunc() {
	name, _ := pages.GetFrontPage()
	if name != "main" {
		return
	}

	mainMenu.Clear().
		AddItem("Configure Kubeconfigs", "Configure which Kubeconfigs to use for primary and secondary locations", '5', func() { showConfigPage() }).
		AddItem("Quit", "Press to exit app", 'q', func() { app.Stop() })

	if kubeConfigPrimary.path == "" || kubeConfigSecondary.path == "" {
		return
	}
	mainMenu.
		InsertItem(0, "Verify Install", "Verify correct AsyncDR installation", '2', func() { showAlert("This is not implemented yet") }).
		InsertItem(0, "Install", "Install AsyncDR", '1', func() {
			log.Info("Checking requirements")
			showModal("checkRequirement", "checking requirements for install...", []string{}, nil)
			go showBlockPoolChoice()
		})

	if checkForOMAPGenerator(kubeConfigPrimary) && checkForOMAPGenerator(kubeConfigSecondary) {
		mainMenu.
			InsertItem(2, "Failover / Failback", "Failover to secondary or Failback to primary location", '9', func() {}).
			InsertItem(2, "Configure Secondary", "Configure PVs for DR on the secondary side", '4', func() { setPVCViewPage(secondaryPVCs, kubeConfigSecondary) }).
			InsertItem(2, "Configure Primary", "Configure PVs for DR on the primary side", '3', func() { setPVCViewPage(primaryPVCs, kubeConfigPrimary) })
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
