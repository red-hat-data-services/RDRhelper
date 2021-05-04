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
	logFile, err := os.OpenFile("RDRhelper.log",
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
		InsertItem(0, "Verify Install", "Verify correct RDR installation", '2', func() { showVerifyPage(kubeConfigPrimary,kubeConfigSecondary) }).
		InsertItem(0, "Install", "Install RDR", '1', func() {
			log.Info("Checking requirements")
			showModal("checkRequirement", "checking requirements for install...", []string{}, nil)
			go showBlockPoolChoice()
		})

	if checkForOMAPGenerator(kubeConfigPrimary) && checkForOMAPGenerator(kubeConfigSecondary) {
		mainMenu.
			InsertItem(2, "Failover / Failback", "Failover to secondary or Failback to primary location", '9', func() { askSeriousForFailover() }).
			InsertItem(2, "Configure Secondary", "Configure PVs for DR on the secondary side", '4', func() { setPVCViewPage(secondaryPVCs, kubeConfigSecondary, kubeConfigPrimary) }).
			InsertItem(2, "Configure Primary", "Configure PVs for DR on the primary side", '3', func() { setPVCViewPage(primaryPVCs, kubeConfigPrimary, kubeConfigSecondary) })
	}
}
