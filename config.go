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
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	controllerClient "sigs.k8s.io/controller-runtime/pkg/client"
)

var frame *tview.Frame

var appConfig = struct {
	KubeConfigPrimaryPath   string `yaml:"kubeConfigPrimaryPath"`
	KubeConfigSecondaryPath string `yaml:"kubeConfigSecondaryPath"`
}{}

type kubeAccess struct {
	name             string
	path             string
	config           clientcmdapi.Config
	typedClient      *kubernetes.Clientset
	dynamicClient    dynamic.Interface
	controllerClient controllerClient.Client
}

var primaryLocation, secondaryLocation string
var kubeConfigPrimary, kubeConfigSecondary kubeAccess

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
	access, err := validateKubeConfig(path)
	if err != nil {
		return
	}
	appConfig.KubeConfigPrimaryPath = path
	access.name = "primary"
	kubeConfigPrimary = access
	url, err := url.Parse(kubeConfigPrimary.config.Clusters[kubeConfigPrimary.config.Contexts[kubeConfigPrimary.config.CurrentContext].Cluster].Server)
	if err != nil {
		return
	}
	primaryLocation = strings.TrimPrefix(url.Hostname(), "api.")
	updateFrame()
}
func secondaryKubeConfChanged(path string) {
	access, err := validateKubeConfig(path)
	if err != nil {
		return
	}
	appConfig.KubeConfigSecondaryPath = path
	access.name = "secondary"
	kubeConfigSecondary = access
	url, err := url.Parse(kubeConfigSecondary.config.Clusters[kubeConfigSecondary.config.Contexts[kubeConfigSecondary.config.CurrentContext].Cluster].Server)
	if err != nil {
		return
	}
	secondaryLocation = strings.TrimPrefix(url.Hostname(), "api.")
	updateFrame()
}

func validateKubeConfig(path string) (kubeAccess, error) {
	info, err := os.Stat(path)
	if err != nil {
		return kubeAccess{}, errors.Wrapf(err, "The given file does not exist")
	}
	if info.IsDir() {
		return kubeAccess{}, errors.Errorf("%s is a directory, not a file", path)
	}
	fileConfig, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return kubeAccess{}, errors.Wrapf(err, "failed to load kubeconfig file at %s", path)
	}
	restConfig, err := clientcmd.NewDefaultClientConfig(*fileConfig, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return kubeAccess{}, errors.Wrapf(err, "failed to instantiate rest client", path)
	}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return kubeAccess{}, errors.Wrapf(err, "failed to load kubeconfig as kubernetes client", path)
	}
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return kubeAccess{}, errors.Wrapf(err, "failed to load kubeconfig as kubernetes client", path)
	}
	cClient, err := controllerClient.New(restConfig, controllerClient.Options{})
	if err != nil {
		return kubeAccess{}, errors.Wrapf(err, "failed to load kubeconfig as kubernetes client", path)
	}
	return kubeAccess{
		path:             path,
		config:           *fileConfig,
		typedClient:      clientset,
		dynamicClient:    dynamicClient,
		controllerClient: cClient,
	}, nil
}
