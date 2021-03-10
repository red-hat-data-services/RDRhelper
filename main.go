package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/briandowns/spinner"
	"github.com/mgutz/ansi"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

var kubeConfig *clientcmdapi.Config
var textGood = ansi.ColorCode("yellow+b")
var textAttention = ansi.ColorCode("red+b")
var textReset = ansi.ColorCode("reset")

var appConfig = struct {
	KubeConfigSourcePath string `yaml:"kubeConfigSourcePath"`
	KubeConfigTargetPath string `yaml:"kubeConfigTargetPath"`
}{}

func init() {
	// Log as JSON instead of the default ASCII formatter.
	// log.SetFormatter(&log.JSONFormatter{})

	// Output to stdout instead of the default stderr
	// Can be any io.Writer, see below for File example
	log.SetOutput(os.Stdout)

	// Only log the warning severity or above.
	log.SetLevel(log.InfoLevel)
}

func main() {
	mode := ""
	prompt := &survey.Select{
		Message: "What do you want to do?",
		Options: []string{"install", "configure", "uninstall"},
	}
	survey.AskOne(prompt, &mode)

	err := readConfig()
	if err != nil {
		os.Exit(1)
	}

	switch mode {
	case "install":
		install()
	case "configure":
		configure()
	default:
		log.Infof("%s is not implemented yet...", mode)

	}
}

func validateKubeConfig(val interface{}) error {
	path := val.(string)
	info, err := os.Stat(path)
	if err != nil {
		return errors.Wrapf(err, "The given file does not exist")
	}
	if info.IsDir() {
		return errors.Errorf("%s is a directory, not a file", path)
	}
	_, err = clientcmd.LoadFromFile(path)
	if err != nil {
		return errors.Wrapf(err, "failed to load kubeconfig file at %s", path)
	}
	return nil
}

func checkOCSReady(target string) {
	fmt.Printf("Verifying that OCS is ready on the %s cluster\n", target)

	// Check
	// oc get storagecluster -n openshift-storage ocs-storagecluster -o jsonpath='{.status.phase}{"\n"}'

	s := spinner.New(spinner.CharSets[39], 150*time.Millisecond)
	s.Start()
	time.Sleep(2 * time.Second)
	s.Stop()
	fmt.Printf("%s  %s OCS is ready\n%s", textGood, target, textReset)
}

func enableOmapGenerator(target string) {
	fmt.Printf("Activating the OMAP Generator on the %s cluster\n", target)
	fmt.Printf("  OMAP Generator created on the %s cluster\n", target)

	// Set
	// oc patch cm rook-ceph-operator-config -n openshift-storage --type json --patch  '[{ "op": "add", "path": "/data/CSI_ENABLE_OMAP_GENERATOR", "value": "true" }]'
	// Validate that csi-omap-generator Pods appear

	s := spinner.New(spinner.CharSets[39], 150*time.Millisecond)
	s.Start()
	time.Sleep(2 * time.Second)
	s.Stop()
	fmt.Printf("%s  %s OMAP Generator is ready\n%s", textGood, target, textReset)
}

func patchRBDforMirror(target string) {
	fmt.Printf("Patching block storage pool for async replication on the %s cluster\n", target)

	// Set
	// oc apply a modified CephBlockPool

	s := spinner.New(spinner.CharSets[39], 150*time.Millisecond)
	s.Start()
	time.Sleep(2 * time.Second)
	s.Stop()
	fmt.Printf("%s  %s Block storage pool is ready\n%s", textGood, target, textReset)
}

func createMirrorBootstrapSecret(target string) {
	fmt.Printf("Setting up bootstrap secrets for replication on the %s cluster\n", target)

	fmt.Printf("%s  %s determining the bootstrap secret\n%s", textGood, target, textReset)

	s := spinner.New(spinner.CharSets[39], 150*time.Millisecond)
	s.Start()
	time.Sleep(2 * time.Second)
	s.Stop()

	fmt.Printf("%s  %s determining the other cluster's name\n%s", textGood, target, textReset)

	s.Restart()
	time.Sleep(2 * time.Second)
	s.Stop()

	fmt.Printf("%s  %s saving the bootstrap secret\n%s", textGood, target, textReset)

	s.Restart()
	time.Sleep(2 * time.Second)
	s.Stop()

	fmt.Printf("%s  %s bootstrap secrets configured\n%s", textGood, target, textReset)
}

func createMirrorCRD(target string) {
	fmt.Printf("Creating Mirror CR on the %s cluster\n", target)

	yaml := `    apiVersion: ceph.rook.io/v1
    kind: CephRBDMirror
    metadata:
    name: rbd-mirror
    namespace: openshift-storage
    spec:
    # the number of rbd-mirror daemons to deploy
    count: 1
    peers:
        secretNames:
        # list of Kubernetes Secrets containing the peer token
        - 59b89021-3ee2-4a25-b087-b43ee80b3dde-openshift-storage
        resources:
        # The pod requests and limits
        limits:
            cpu: "1"
            memory: "2Gi"
        requests:
            cpu: "1"
            memory: "2Gi"
`
	fmt.Println(yaml)
	fmt.Printf("%s  %s waiting for mirror Pod to be Ready\n%s", textGood, target, textReset)
	s := spinner.New(spinner.CharSets[39], 150*time.Millisecond)
	s.Start()
	time.Sleep(2 * time.Second)
	s.Stop()
	fmt.Printf("%s  %s mirror Pod is up and Ready\n%s", textGood, target, textReset)
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
	return nil
}

func writeNewConfig() error {
	qs := []*survey.Question{
		{
			Name: "KubeConfigSourcePath",
			Prompt: &survey.Input{
				Message: fmt.Sprint("Point me to the kubeconfig file of the ", textAttention, "source", textReset, " cluster:"),
				Suggest: func(toComplete string) []string {
					files, _ := filepath.Glob(toComplete + "*")
					return files
				},
			},
			Validate: validateKubeConfig,
		},
		{
			Name: "KubeConfigTargetPath",
			Prompt: &survey.Input{
				Message: fmt.Sprint("Point me to the kubeconfig file of the ", textAttention, "target", textReset, " cluster:"),
				Suggest: func(toComplete string) []string {
					files, _ := filepath.Glob(toComplete + "*")
					return files
				},
			},
			Validate: validateKubeConfig,
		},
	}

	// perform the questions
	err := survey.Ask(qs, &appConfig)
	if err != nil {
		fmt.Println(err.Error())
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.WithError(err).Warn("Could not determine user's home directory")
		return err
	}
	f, err := os.Create(path.Join(home, "/.config/asyncDRhelper.conf"))
	if err != nil {
		log.WithError(err).Warn("Could not create config on disk")
		return err
	}
	defer f.Close()

	encoder := yaml.NewEncoder(f)
	err = encoder.Encode(appConfig)
	if err != nil {
		log.WithError(err).Warn("Could not write config to disk")
		return err
	}
	return nil
}

func install() {

	checkOCSReady("source")
	checkOCSReady("target")
	enableOmapGenerator("source")
	enableOmapGenerator("target")
	patchRBDforMirror("source")
	patchRBDforMirror("target")
	createMirrorBootstrapSecret("source")
	createMirrorBootstrapSecret("target")
	createMirrorCRD("source")
	createMirrorCRD("target")

	fmt.Println("Everything is ready now - restart this application in config mode to set up PVCs for replication")
}

func configure() {
	pvs := []string{}
	prompt := &survey.MultiSelect{
		Message: "Which PVCs should be activated for Mirroring:",
		Options: []string{
			"MongoDB-important",
			"PostgreSQL-staging",
			"PostgreSQL-prod",
			"Scratch",
			"Wordpress",
			"Wordpress-plugins",
			"temp",
			"testthis",
		},
		PageSize: 20,
	}
	survey.AskOne(prompt, &pvs)

	s := spinner.New(spinner.CharSets[39], 150*time.Millisecond)
	s.Start()
	time.Sleep(2 * time.Second)
	s.Stop()

	fmt.Printf("%s  PVs ready for mirroring on source cluster\n%s", textGood, textReset)
	s.Restart()
	time.Sleep(2 * time.Second)
	s.Stop()

	fmt.Printf("%s  PVs ready for mirroring on target cluster\n%s", textGood, textReset)

}
