# oc-container-login

[![GoDoc](https://godoc.org/github.com/cblecker/oc-container-login?status.svg)](https://godoc.org/github.com/cblecker/oc-container-login)
[![CI](https://github.com/cblecker/oc-container-login/actions/workflows/ci.yml/badge.svg)](https://github.com/cblecker/oc-container-login/actions/workflows/ci.yml)

A [cli plugin] that logs Apple's [container] CLI in to a registry with your
OpenShift token. It is the `oc registry login` of `container`, which keeps its
registry credentials in the macOS Keychain and so ignores the files
`oc registry login` writes.

[cli plugin]: https://kubernetes.io/docs/tasks/extend-kubectl/kubectl-plugins/
[container]: https://github.com/apple/container

## Installation
There are two ways to install this plugin:

Go-based install:
```bash
go install github.com/cblecker/oc-container-login@latest
```

Download from releases page: https://github.com/cblecker/oc-container-login/releases/latest

## Use

`container`'s services must be running (`container system start`), and you must
be logged in to the cluster with a token (`oc login`); client certificates and
exec credential plugins carry no token to hand to the registry.

```
Log in to a registry with Apple's container CLI using your OpenShift token.

 The token of your current session is passed to 'container registry login', which checks it against the registry and stores it in the macOS Keychain.

 Without --registry, the public hostname of the cluster's integrated registry is detected from an image stream in the current namespace or the openshift namespace.

Usage:
  oc container login [flags]

Examples:
  # Log in to the cluster's integrated registry
  oc container login

  # Log in to specific registries with the token from another context
  oc container login --context my-context --registry registry.example.com --registry proxy.example.com:8443

Flags:
      --registry stringArray   Registry host[:port] to log in to. May be repeated. Defaults to the public hostname of the cluster's integrated registry.
```

The usual kubeconfig flags (`--context`, `--kubeconfig`, `--namespace`,
`--token`, ...) are also accepted. Place them after `oc container login`.
