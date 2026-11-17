# oc-container-login

A kubectl/oc plugin that logs Apple's `container` CLI in to a registry with the
token of the current OpenShift session. Invoked as `oc container login`.

## Build Commands

```bash
# Build the binary
go build

# Run tests
go test -race ./...

# Run linter (CI uses golangci-lint v2.15.0, configured in .golangci.yml)
golangci-lint run

# Validate the release config
goreleaser check

# Install to $GOPATH/bin
go install
```

## Architecture

Two Go files following kubectl plugin conventions:

- **oc-container-login.go** - Entry point
- **pkg/login/login.go** - Cobra command and plugin logic in the Validate/Complete/Run pattern

`Validate` runs before `Complete` so that flag errors are reported without a
kubeconfig.

### Login flow

1. Take the bearer token from the kubeconfig. Sessions without one (client
   certificates, exec credential plugins) are an error.
2. Use the `--registry` hosts, or discover the integrated registry's public
   hostname from `status.publicDockerImageRepository` of an image stream in the
   current namespace, then the `openshift` namespace. Image streams are read
   with the dynamic client to avoid depending on `openshift/client-go`.
3. Run `container registry login --username user --password-stdin HOST` for each
   host with the token on stdin, 30 seconds per host. Every host is attempted
   and the failures are returned together.

The token must never reach a command line or the output.

### Tests

`Options` has two seams, `run` (executes `container`) and `lookPath`, which the
tests replace along with a fake dynamic client. CI runs on Linux, where
`container` does not exist, so tests must not call the real one.

## CI/CD

- `.github/workflows/ci.yml` - golangci-lint, plus build and race-enabled tests on macOS and Linux; the `conclusion` job is the one check to require in branch protection
- `.github/workflows/go_release.yml` - GoReleaser on tags; darwin/arm64 only, the one platform `container` supports
