module gitlab.consulting.redhat.com/cblum/asyncDRhelper

go 1.16

require (
	github.com/gdamore/tcell/v2 v2.2.0
	github.com/openshift/ocs-operator v0.0.1-alpha1.0.20210329143343-282f7dedfebd
	github.com/pkg/errors v0.9.1
	github.com/rivo/tview v0.0.0-20210312174852-ae9464cc3598
	github.com/rook/rook v1.5.8-0.20210219161258-2744b997ad89
	github.com/sirupsen/logrus v1.8.1
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.20.5
	k8s.io/apimachinery v0.20.5
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/kubectl v0.18.8
	sigs.k8s.io/controller-runtime v0.8.3
)

// This looks "horrible", but is due to the Rook including k8s.io/kubernetes directly which is not recommended,
// ee https://github.com/kubernetes/kubernetes/issues/79384#issuecomment-505725449
replace (
	github.com/googleapis/gnostic => github.com/googleapis/gnostic v0.4.1

	github.com/openshift/api => github.com/openshift/api v0.0.0-20201203102015-275406142edb // required for Quickstart CRD
	golang.org/x/text => golang.org/x/text v0.3.3
	google.golang.org/grpc => google.golang.org/grpc v1.26.0
	k8s.io/api => k8s.io/api v0.20.5
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.20.5
	k8s.io/apimachinery => k8s.io/apimachinery v0.20.5
	k8s.io/apiserver => k8s.io/apiserver v0.20.5
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.20.5
	k8s.io/client-go => k8s.io/client-go v0.20.5
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.20.5
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.20.5
	k8s.io/code-generator => k8s.io/code-generator v0.20.5
	k8s.io/component-base => k8s.io/component-base v0.20.5
	k8s.io/component-helpers => k8s.io/component-helpers v0.20.5
	k8s.io/controller-manager => k8s.io/controller-manager v0.20.5
	k8s.io/cri-api => k8s.io/cri-api v0.20.5
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.20.5
	k8s.io/gengo => k8s.io/gengo v0.0.0-20201113003025-83324d819ded
	k8s.io/heapster => k8s.io/heapster v1.2.0-beta.1
	k8s.io/klog/v2 => k8s.io/klog/v2 v2.4.0
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.20.5
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.20.5
	k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20201113171705-d219536bb9fd
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.20.5
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.20.5
	k8s.io/kubectl => k8s.io/kubectl v0.20.5
	k8s.io/kubelet => k8s.io/kubelet v0.20.5
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.20.5
	k8s.io/metrics => k8s.io/metrics v0.20.5
	k8s.io/mount-utils => k8s.io/mount-utils v0.20.5
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.20.5
	k8s.io/system-validators => k8s.io/system-validators v1.2.0
	k8s.io/utils => k8s.io/utils v0.0.0-20201110183641-67b214c5f920
)
