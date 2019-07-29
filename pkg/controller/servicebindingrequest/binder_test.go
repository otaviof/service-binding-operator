package servicebindingrequest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	"github.com/redhat-developer/service-binding-operator/pkg/apis/apps/v1alpha1"
	"github.com/redhat-developer/service-binding-operator/test/mocks"
)

var binder *Binder
var binderFakeClient client.Client

func TestBinder(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	ns := "binder"
	name := "service-binding-request"

	s := scheme.Scheme
	matchLabels := map[string]string{
		"connects-to": "database",
		"environment": "binder",
	}

	sbr := mocks.ServiceBindingRequestMock(ns, name, name, matchLabels)
	s.AddKnownTypes(v1alpha1.SchemeGroupVersion, &sbr)

	require.Nil(t, appsv1.AddToScheme(s))
	d := mocks.DeploymentMock(ns, name, matchLabels)
	s.AddKnownTypes(appsv1.SchemeGroupVersion, &d)

	objs := []runtime.Object{&sbr, &d}
	binderFakeClient = fake.NewFakeClientWithScheme(s, objs...)
	binder = NewBinder(context.TODO(), binderFakeClient, &sbr)

	require.NotNil(t, binder)
}

func TestBinderGetListGVK(t *testing.T) {
	gvk, err := binder.getListGVK()

	assert.Nil(t, err)
	assert.Equal(t, gvk.Kind, "DeploymentList")
}

func TestBinderSearch(t *testing.T) {
	list, err := binder.search()

	assert.Nil(t, err)
	assert.Equal(t, 1, len(list.Items))
}

func TestBinderAppendEnvFrom(t *testing.T) {
	secretName := "secret"
	d := mocks.DeploymentMock("binder", "binder", map[string]string{})
	list := binder.appendEnvFrom(d.Spec.Template.Spec.Containers[0].EnvFrom, secretName)

	assert.Equal(t, 1, len(list))
	assert.Equal(t, secretName, list[0].SecretRef.Name)
}
