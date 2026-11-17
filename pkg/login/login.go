// Package login implements the oc container login command.
package login

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"k8s.io/cli-runtime/pkg/genericclioptions"

	"k8s.io/client-go/dynamic"

	"k8s.io/kubectl/pkg/util/templates"
)

const (
	containerBinary = "container"

	// registryUsername is the placeholder user name `oc registry login` uses.
	// The integrated registry authenticates on the token alone.
	registryUsername = "user"

	openShiftNamespace = "openshift"

	// loginTimeout bounds each `container registry login`, whose registry ping
	// has no deadline of its own.
	loginTimeout = 30 * time.Second
)

var imageStreamResource = schema.GroupVersionResource{
	Group:    "image.openshift.io",
	Version:  "v1",
	Resource: "imagestreams",
}

var (
	loginLong = templates.LongDesc(`
	  Log in to a registry with Apple's container CLI using your OpenShift token.

	  The token of your current session is passed to 'container registry login',
	  which checks it against the registry and stores it in the macOS Keychain.

	  Without --registry, the public hostname of the cluster's integrated registry
	  is detected from an image stream in the current namespace or the openshift
	  namespace.`)

	loginExample = templates.Examples(`
	  # Log in to the cluster's integrated registry
	  oc container login

	  # Log in to specific registries with the token from another context
	  oc container login --context my-context --registry registry.example.com --registry proxy.example.com:8443`)
)

// runFunc runs a command with stdin as its standard input and returns its
// combined output.
type runFunc func(ctx context.Context, stdin, name string, args ...string) ([]byte, error)

// Options are options supported by the login command.
type Options struct {
	configFlags *genericclioptions.ConfigFlags

	// Registries are the registry hosts to log in to. When empty, the
	// integrated registry of the cluster is discovered.
	Registries []string

	token         string
	namespace     string
	dynamicClient dynamic.Interface

	lookPath func(file string) (string, error)
	run      runFunc

	// args is the slice of strings containing any arguments passed
	args []string

	genericclioptions.IOStreams
}

// NewOptions provides an instance of Options with default values.
func NewOptions(streams genericclioptions.IOStreams) *Options {
	return &Options{
		configFlags: genericclioptions.NewConfigFlags(true),

		lookPath: exec.LookPath,
		run:      runCommand,

		IOStreams: streams,
	}
}

// NewCmdLogin provides a cobra command wrapping Options.
func NewCmdLogin(streams genericclioptions.IOStreams) *cobra.Command {
	o := NewOptions(streams)

	cmd := &cobra.Command{
		Use:          "oc container login",
		Short:        "Log in to a registry with Apple's container CLI using your OpenShift token",
		Long:         loginLong,
		Example:      loginExample,
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			o.args = args

			// Validate runs first so that bad flags are reported without
			// needing a kubeconfig.
			if err := o.Validate(); err != nil {
				return err
			}
			if err := o.Complete(); err != nil {
				return err
			}

			return o.Run(c.Context())
		},
	}

	cmd.Flags().StringArrayVar(&o.Registries, "registry", o.Registries, "Registry host[:port] to log in to. May be repeated. Defaults to the public hostname of the cluster's integrated registry.")
	o.configFlags.AddFlags(cmd.Flags())

	return cmd
}

// Validate ensures that the arguments and flag values are usable.
func (o *Options) Validate() error {
	if len(o.args) > 0 {
		return errors.New("no arguments are allowed")
	}

	for _, registry := range o.Registries {
		// container prepends the scheme itself, so anything but a bare host
		// would produce a broken URL.
		if registry == "" || strings.ContainsFunc(registry, func(r rune) bool { return r == '/' || unicode.IsSpace(r) }) {
			return fmt.Errorf("invalid --registry %q: must be a bare host[:port], without scheme or path", registry)
		}
	}

	return nil
}

// Complete loads the token, namespace and client from the kubeconfig.
func (o *Options) Complete() error {
	clientConfig, err := o.configFlags.ToRESTConfig()
	if err != nil {
		return err
	}
	o.token = clientConfig.BearerToken

	o.namespace, _, err = o.configFlags.ToRawKubeConfigLoader().Namespace()
	if err != nil {
		return err
	}

	o.dynamicClient, err = dynamic.NewForConfig(clientConfig)

	return err
}

// Run logs in to each registry, discovering the integrated registry when none
// was given. Every registry is attempted; the error covers all that failed.
func (o *Options) Run(ctx context.Context) error {
	if o.token == "" {
		return errors.New("no token is in use for this session; log in with 'oc login' (client certificates and exec credential plugins carry no token)")
	}

	if _, err := o.lookPath(containerBinary); err != nil {
		return fmt.Errorf("the %s CLI is required: %w", containerBinary, err)
	}

	registries := o.Registries
	if len(registries) == 0 {
		registry, err := o.findPublicHostname(ctx)
		if err != nil {
			return err
		}
		registries = []string{registry}
	}

	var errs []error
	for _, registry := range registries {
		if err := o.login(ctx, registry); err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := fmt.Fprintf(o.Out, "Logged in to %s\n", registry); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// findPublicHostname returns the public hostname of the integrated registry,
// read from an image stream in the current namespace or the openshift
// namespace. Unlike `oc registry login` it never returns the internal
// hostname, which is unreachable from outside the cluster.
func (o *Options) findPublicHostname(ctx context.Context) (string, error) {
	for _, namespace := range []string{o.namespace, openShiftNamespace} {
		imageStreams, err := o.dynamicClient.Resource(imageStreamResource).Namespace(namespace).List(ctx, metav1.ListOptions{Limit: 1})
		if apierrors.IsUnauthorized(err) {
			return "", errors.New("the cluster rejected your token; log in again with 'oc login'")
		}
		if err != nil || len(imageStreams.Items) == 0 {
			continue
		}

		repository, _, _ := unstructured.NestedString(imageStreams.Items[0].Object, "status", "publicDockerImageRepository")
		if registry, _, _ := strings.Cut(repository, "/"); registry != "" {
			return registry, nil
		}
	}

	return "", errors.New("the public hostname of the integrated registry could not be determined; specify one with --registry")
}

// login stores the token for registry in container's keychain. The token is
// passed on stdin so that it never appears in the process arguments.
func (o *Options) login(ctx context.Context, registry string) error {
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()

	out, err := o.run(ctx, o.token, containerBinary, "registry", "login", "--username", registryUsername, "--password-stdin", registry)
	if err == nil {
		return nil
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("login to %s timed out after %s", registry, loginTimeout)
	}
	if output := strings.TrimSpace(string(out)); output != "" {
		return fmt.Errorf("login to %s failed: %w: %s", registry, err, output)
	}

	return fmt.Errorf("login to %s failed: %w", registry, err)
}

func runCommand(ctx context.Context, stdin, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	// Without WaitDelay, a process that outlives the kill and still holds the
	// output pipe would block CombinedOutput past the timeout.
	cmd.WaitDelay = time.Second

	return cmd.CombinedOutput()
}
