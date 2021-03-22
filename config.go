package main

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/pkg/errors"
	"github.com/rivo/tview"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

var frame *tview.Frame

var appConfig = struct {
	KubeConfigPrimaryPath   string `yaml:"kubeConfigPrimaryPath"`
	KubeConfigSecondaryPath string `yaml:"kubeConfigSecondaryPath"`
}{}

var kubeConfigPrimary clientcmdapi.Config
var kubeConfigSecondary clientcmdapi.Config
var primaryClient *kubernetes.Clientset
var secondaryClient *kubernetes.Clientset
var primaryLocation, secondaryLocation string

func updateFrame() {
	frame.Clear()
	frame.
		AddText("Async DR Helper Tool", true, tview.AlignCenter, tcell.ColorWhite).
		AddText(fmt.Sprintf("Primary: %s", primaryLocation), false, tview.AlignLeft, tcell.ColorWhite).
		AddText(fmt.Sprintf("Secondary: %s", secondaryLocation), false, tview.AlignRight, tcell.ColorWhite)
}

func readConfig() error {
	home, err := os.UserHomeDir()
	if err != nil {
		log.WithError(err).Warn("Could not determine user's home directory")
		return err
	}
	f, err := os.Open(path.Join(home, "/.config/asyncDRhelper.conf"))
	if err != nil {
		return writeNewConfig()
	}
	defer f.Close()

	decoder := yaml.NewDecoder(f)
	err = decoder.Decode(&appConfig)
	if err != nil {
		log.WithError(err).Warn("Could not understand config")
		return writeNewConfig()
	}
	// Call conf changed to set the config
	primaryKubeConfChanged(appConfig.KubeConfigPrimaryPath)
	secondaryKubeConfChanged(appConfig.KubeConfigSecondaryPath)
	return nil
}

func writeNewConfig() error {
	home, err := os.UserHomeDir()
	if err != nil {
		pages.AddPage("error",
			tview.NewModal().
				SetText("Could not determine user's home directory").
				AddButtons([]string{"OK"}).
				SetDoneFunc(func(buttonIndex int, buttonLabel string) { pages.HidePage("error") }),
			true,
			true)
		log.WithError(err).Warn("Could not determine user's home directory")
		return err
	}
	f, err := os.Create(path.Join(home, "/.config/asyncDRhelper.conf"))
	if err != nil {

		pages.AddPage("error",
			tview.NewModal().
				SetText("Could not create config on disk").
				AddButtons([]string{"OK"}).
				SetDoneFunc(func(buttonIndex int, buttonLabel string) { pages.HidePage("error") }),
			true,
			true)
		log.WithError(err).Warn("Could not create config on disk")
		return err
	}
	defer f.Close()

	encoder := yaml.NewEncoder(f)
	err = encoder.Encode(appConfig)
	if err != nil {
		pages.AddPage("error",
			tview.NewModal().
				SetText("Could not write config on disk").
				AddButtons([]string{"OK"}).
				SetDoneFunc(func(buttonIndex int, buttonLabel string) { pages.HidePage("error") }),
			true,
			true)
		log.WithError(err).Warn("Could not write config to disk")
		return err
	}
	return nil
}

func showConfigPage() {
	form := tview.NewForm().
		AddInputField("primary KubeConf location", appConfig.KubeConfigPrimaryPath, 0, nil, primaryKubeConfChanged).
		AddInputField("secondary KubeConf location", appConfig.KubeConfigSecondaryPath, 0, nil, secondaryKubeConfChanged)
	form.
		AddButton("Go back", func() { writeNewConfig(); pages.SwitchToPage("main") }).
		SetCancelFunc(func() { writeNewConfig(); pages.SwitchToPage("main") })
	form.SetBorder(true).
		SetTitle("Configuration").SetTitleAlign(tview.AlignLeft)
	pages.AddAndSwitchToPage("KubeConfiguration", form, true)
}

func primaryKubeConfChanged(path string) {
	config, client, err := validateKubeConfig(path)
	if err != nil {
		return
	}
	appConfig.KubeConfigPrimaryPath = path
	kubeConfigPrimary = *config
	primaryClient = client
	url, err := url.Parse(kubeConfigPrimary.Clusters[kubeConfigPrimary.Contexts[kubeConfigPrimary.CurrentContext].Cluster].Server)
	if err != nil {
		return
	}
	primaryLocation = strings.TrimPrefix(url.Hostname(), "api.")
	updateFrame()
}
func secondaryKubeConfChanged(path string) {
	config, client, err := validateKubeConfig(path)
	if err != nil {
		return
	}
	appConfig.KubeConfigSecondaryPath = path
	kubeConfigSecondary = *config
	secondaryClient = client
	url, err := url.Parse(kubeConfigSecondary.Clusters[kubeConfigSecondary.Contexts[kubeConfigSecondary.CurrentContext].Cluster].Server)
	if err != nil {
		return
	}
	secondaryLocation = strings.TrimPrefix(url.Hostname(), "api.")
	updateFrame()
}

func validateKubeConfig(path string) (*clientcmdapi.Config, *kubernetes.Clientset, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "The given file does not exist")
	}
	if info.IsDir() {
		return nil, nil, errors.Errorf("%s is a directory, not a file", path)
	}
	fileConfig, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to load kubeconfig file at %s", path)
	}

	restConfig, err := clientcmd.NewDefaultClientConfig(*fileConfig, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to instantiate rest client", path)
	}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to load kubeconfig as kubernetes client", path)
	}
	return fileConfig, clientset, nil
}
