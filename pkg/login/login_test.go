package login

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"k8s.io/cli-runtime/pkg/genericclioptions"

	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

const testToken = "sha256~secret"

// call records one invocation of the container CLI.
type call struct {
	stdin string
	args  []string
}

// registry returns the registry the call logged in to.
func (c call) registry() string {
	return c.args[len(c.args)-1]
}

// newTestOptions returns Options wired to a fake cluster holding imageStreams
// and a fake container CLI whose logins fail for the registries in failing.
func newTestOptions(imageStreams []runtime.Object, failing ...string) (*Options, *[]call, *bytes.Buffer) {
	calls := &[]call{}
	streams, _, out, _ := genericclioptions.NewTestIOStreams()

	o := &Options{
		token:     testToken,
		namespace: "myproject",
		dynamicClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
			runtime.NewScheme(),
			map[schema.GroupVersionResource]string{imageStreamResource: "ImageStreamList"},
			imageStreams...,
		),
		lookPath: func(string) (string, error) { return "/bin/container", nil },
		run: func(_ context.Context, stdin, _ string, args ...string) ([]byte, error) {
			c := call{stdin: stdin, args: args}
			*calls = append(*calls, c)
			for _, registry := range failing {
				if c.registry() == registry {
					return []byte("Error: unauthorized"), errors.New("exit status 1")
				}
			}
			return nil, nil
		},
		IOStreams: streams,
	}

	return o, calls, out
}

func imageStream(namespace string, status map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "image.openshift.io/v1",
		"kind":       "ImageStream",
		"metadata":   map[string]any{"name": "tools", "namespace": namespace},
		"status":     status,
	}}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name     string
		registry string
		wantErr  bool
	}{
		{name: "bare host", registry: "registry.example.com"},
		{name: "host and port", registry: "registry.example.com:5000"},
		{name: "scheme", registry: "https://registry.example.com", wantErr: true},
		{name: "path", registry: "registry.example.com/myproject", wantErr: true},
		{name: "empty", registry: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &Options{Registries: []string{tt.registry}}
			if err := o.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRunDiscovery(t *testing.T) {
	tests := []struct {
		name         string
		imageStreams []runtime.Object
		unauthorized bool
		want         string
	}{
		{
			name: "public repository in the current namespace",
			imageStreams: []runtime.Object{
				imageStream("myproject", map[string]any{"publicDockerImageRepository": "registry.example.com/myproject/tools"}),
			},
			want: "registry.example.com",
		},
		{
			name: "falls through to the openshift namespace",
			imageStreams: []runtime.Object{
				imageStream("openshift", map[string]any{"publicDockerImageRepository": "registry.example.com:5000/openshift/tools"}),
			},
			want: "registry.example.com:5000",
		},
		{
			name: "no image streams",
		},
		{
			name: "internal repository only",
			imageStreams: []runtime.Object{
				imageStream("myproject", map[string]any{"dockerImageRepository": "image-registry.openshift-image-registry.svc:5000/myproject/tools"}),
			},
		},
		{
			name: "token rejected by the cluster",
			imageStreams: []runtime.Object{
				imageStream("openshift", map[string]any{"publicDockerImageRepository": "registry.example.com/openshift/tools"}),
			},
			unauthorized: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, calls, _ := newTestOptions(tt.imageStreams)
			if tt.unauthorized {
				client, ok := o.dynamicClient.(*dynamicfake.FakeDynamicClient)
				if !ok {
					t.Fatalf("dynamicClient is %T, want the fake", o.dynamicClient)
				}
				client.PrependReactor("list", "imagestreams",
					func(action clienttesting.Action) (bool, runtime.Object, error) {
						// Only the first namespace rejects the token, so that
						// falling through to openshift would show as a login.
						if action.GetNamespace() != o.namespace {
							return false, nil, nil
						}
						return true, nil, apierrors.NewUnauthorized("token expired")
					})
			}

			err := o.Run(context.Background())

			if tt.want == "" {
				if err == nil {
					t.Error("Run() succeeded, want an error")
				}
				if len(*calls) != 0 {
					t.Errorf("container was called %d times, want 0", len(*calls))
				}
				return
			}
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if len(*calls) != 1 || (*calls)[0].registry() != tt.want {
				t.Errorf("container calls = %v, want one login to %s", *calls, tt.want)
			}
		})
	}
}

func TestRunLogin(t *testing.T) {
	o, calls, out := newTestOptions(nil, "bad.example.com")
	o.Registries = []string{"bad.example.com", "good.example.com"}

	err := o.Run(context.Background())

	if err == nil || !strings.Contains(err.Error(), "bad.example.com") {
		t.Errorf("Run() error = %v, want it to name bad.example.com", err)
	}
	if got := out.String(); got != "Logged in to good.example.com\n" {
		t.Errorf("output = %q, want only the good.example.com login", got)
	}
	if len(*calls) != 2 {
		t.Fatalf("container was called %d times, want 2", len(*calls))
	}
	for _, c := range *calls {
		if c.stdin != testToken {
			t.Errorf("login to %s got stdin %q, want the token", c.registry(), c.stdin)
		}
		if strings.Contains(strings.Join(c.args, " "), testToken) {
			t.Errorf("login to %s leaked the token in its arguments: %v", c.registry(), c.args)
		}
	}
}

func TestRunFailsBeforeLogin(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Options)
	}{
		{
			name:  "no token",
			setup: func(o *Options) { o.token = "" },
		},
		{
			name: "container not installed",
			setup: func(o *Options) {
				o.lookPath = func(string) (string, error) { return "", errors.New("not found") }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, calls, _ := newTestOptions(nil)
			o.Registries = []string{"registry.example.com"}
			tt.setup(o)

			if err := o.Run(context.Background()); err == nil {
				t.Error("Run() succeeded, want an error")
			}
			if len(*calls) != 0 {
				t.Errorf("container was called %d times, want 0", len(*calls))
			}
		})
	}
}
