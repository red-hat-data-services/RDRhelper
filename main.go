package main

import (
	"io"
	"os"

	"github.com/rivo/tview"
	log "github.com/sirupsen/logrus"
)

var pages = tview.NewPages()
var app = tview.NewApplication()
var logMultiWriter io.Writer
var logFile *os.File

func init() {
	log.SetFormatter(&log.TextFormatter{
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
	log.SetOutput(logFile)

	pages.AddAndSwitchToPage("main",
		tview.NewList().
			AddItem("Install", "Install AsyncDR", '1', func() { installReplication() }).
			AddItem("Verify Install", "Verify correct AsyncDR installation", '2', func() { pages.ShowPage("notImplemented") }).
			AddItem("Configure Primary", "Configure PVs for DR on the primary side", '3', func() { populatePrimaryPVCs(); pages.SwitchToPage("configurePrimary") }).
			AddItem("Configure Secondary", "Configure PVs for DR on the secondary side", '4', nil).
			AddItem("Configure Kubeconfigs", "Configure which Kubeconfigs to use for primary and secondary locations", '5', func() { showConfigPage() }).
			AddItem("Failover / Failback", "Failover to secondary or Failback to primary location", '9', func() {
				log.Info("Selected 9")
				_, err := getCephStatus(kubeConfigPrimary)
				if err != nil {
					log.WithError(err).Warn("ISSUE")
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
