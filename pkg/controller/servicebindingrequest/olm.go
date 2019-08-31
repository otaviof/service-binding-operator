package servicebindingrequest

import (
	"strings"

	"github.com/go-logr/logr"
	olmv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

// OLM represents the actions this operator needs to take upon Operator-Lifecycle-Manager resources,
// Like ClusterServiceVersions (CSV) and CRD-Descriptions.
type OLM struct {
	client dynamic.Interface // kubernetes dynamic client
	ns     string            // namespace
	logger logr.Logger       // logger instance
}

const (
	csvResource = "clusterserviceversions"
)

// listCSVs simple list to all CSV objects in the cluster.
func (o *OLM) listCSVs() ([]unstructured.Unstructured, error) {
	gvr := olmv1alpha1.SchemeGroupVersion.WithResource(csvResource)
	resourceClient := o.client.Resource(gvr).Namespace(o.ns)
	csvs, err := resourceClient.List(metav1.ListOptions{})
	if err != nil {
		o.logger.Error(err, "during listing CSV objects from cluster")
		return nil, err
	}
	return csvs.Items, nil
}

// extractOwnedCRDs from a list of CSV objects.
func (o *OLM) extractOwnedCRDs(
	csvs []unstructured.Unstructured,
) ([]unstructured.Unstructured, error) {
	crds := []unstructured.Unstructured{}
	for _, csv := range csvs {
		ownedPath := []string{"spec", "customresourcedefinitions", "owned"}
		logger := o.logger.WithValues("OwnedPath", ownedPath, "CSV.Name", csv.GetName())

		ownedCRDs, exists, err := unstructured.NestedSlice(csv.Object, ownedPath...)
		if err != nil {
			logger.Error(err, "on extracting nested slice")
			return nil, err
		}
		if !exists {
			continue
		}

		for _, crd := range ownedCRDs {
			data := crd.(map[string]interface{})
			crds = append(crds, unstructured.Unstructured{Object: data})
		}
	}

	return crds, nil
}

// ListCSVOwnedCRDs return a unstructured list of CRD objects from "owned" section in CSVs.
func (o *OLM) ListCSVOwnedCRDs() ([]unstructured.Unstructured, error) {
	csvs, err := o.listCSVs()
	if err != nil {
		o.logger.Error(err, "on listting CSVs")
		return nil, err
	}

	return o.extractOwnedCRDs(csvs)
}

type eachOwnedCRDFn func(crd *olmv1alpha1.CRDDescription)

// loopCRDDescritions takes a function as parameter and excute it against given list of owned CRDs.
func (o *OLM) loopCRDDescritions(ownedCRDs []unstructured.Unstructured, fn eachOwnedCRDFn) error {
	for _, u := range ownedCRDs {
		crdDescription := &olmv1alpha1.CRDDescription{}
		err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, crdDescription)
		if err != nil {
			o.logger.Error(err, "on converting from unstructured to CRD")
			return err
		}
		fn(crdDescription)
	}

	return nil
}

// SelectCRDByGVK return a single CRD based on a given GVK.
func (o *OLM) SelectCRDByGVK(gvk schema.GroupVersionKind) (*olmv1alpha1.CRDDescription, error) {
	logger := o.logger.WithValues("Selector.GVK", gvk)
	ownedCRDs, err := o.ListCSVOwnedCRDs()
	if err != nil {
		logger.Error(err, "on listing owned CRDs")
		return nil, err
	}

	crdDescriptions := []*olmv1alpha1.CRDDescription{}
	err = o.loopCRDDescritions(ownedCRDs, func(crd *olmv1alpha1.CRDDescription) {
		logger = logger.WithValues(
			"CRDDescription.Name", crd.Name,
			"CRDDescription.Version", crd.Version,
			"CRDDescription.Kind", crd.Kind,
		)
		logger.Info("Inspecting CRDDescription object...")
		// checking for suffix since is expected to have object type as prefix
		if !strings.EqualFold(strings.ToLower(crd.Kind), strings.ToLower(gvk.Kind)) {
			return
		}
		if crd.Version != "" && strings.ToLower(gvk.Version) != strings.ToLower(crd.Version) {
			return
		}
		logger.Info("CRDDescription object matches selector!")
		crdDescriptions = append(crdDescriptions, crd)
	})
	if err != nil {
		return nil, err
	}

	if len(crdDescriptions) == 0 {
		logger.Info("No CRD could be found for GVK.")
		return nil, nil
	}
	return crdDescriptions[0], nil
}

// extractGVKs loop owned objects and extract the GVK information from them.
func (o *OLM) extractGVKs(
	crdDescriptions []unstructured.Unstructured,
) ([]schema.GroupVersionKind, error) {
	gvks := []schema.GroupVersionKind{}
	err := o.loopCRDDescritions(crdDescriptions, func(crd *olmv1alpha1.CRDDescription) {
		_, gv := schema.ParseResourceArg(crd.Name)
		gvks = append(gvks, schema.GroupVersionKind{
			Group:   gv.Group,
			Version: crd.Version,
			Kind:    crd.Kind,
		})
	})
	if err != nil {
		return []schema.GroupVersionKind{}, err
	}
	return gvks, nil
}

// ListCSVOwnedCRDsAsGVKs return the list of owned CRDs from all CSV objects as a list of GVKs.
func (o *OLM) ListCSVOwnedCRDsAsGVKs() ([]schema.GroupVersionKind, error) {
	csvs, err := o.listCSVs()
	if err != nil {
		o.logger.Error(err, "on listting CSVs")
		return nil, err
	}
	return o.extractGVKs(csvs)
}

// ListGVKsFromCSVNamespacedName return the list of owned GVKs for a given CSV namespaced named.
func (o *OLM) ListGVKsFromCSVNamespacedName(
	namespacedName types.NamespacedName,
) ([]schema.GroupVersionKind, error) {
	logger := o.logger.WithValues("CSV.NamespacedName", namespacedName)
	gvr := olmv1alpha1.SchemeGroupVersion.WithResource(csvResource)
	resourceClient := o.client.Resource(gvr).Namespace(namespacedName.Namespace)
	u, err := resourceClient.Get(namespacedName.Name, metav1.GetOptions{})
	if err != nil {
		logger.Error(err, "on reading CSV object")
		return []schema.GroupVersionKind{}, err
	}
	var unstructuredCSV unstructured.Unstructured
	unstructuredCSV = *u
	csvs := []unstructured.Unstructured{unstructuredCSV}
	return o.extractGVKs(csvs)
}

// NewOLM instantiate a new OLM.
func NewOLM(client dynamic.Interface, ns string) *OLM {
	return &OLM{
		client: client,
		ns:     ns,
		logger: logf.Log.WithName("olm"),
	}
}
