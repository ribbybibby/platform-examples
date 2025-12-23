# Helm Values

The mapper can streamline the process of migrating Helm charts to Chainguard by
mapping the values for you with the `helm-values` subcommand.

## Basic usage

```
$ helm show values argocd/argo-cd | ./image-mapper helm-values -
dex:
    image:
        repository: cgr.dev/chainguard/dex
global:
    image:
        repository: cgr.dev/chainguard/argocd
redis:
    exporter:
        image:
            repository: cgr.dev/chainguard/prometheus-redis-exporter
    image:
        repository: cgr.dev/chainguard/redis
redis-ha:
    exporter:
        image: cgr.dev/chainguard/prometheus-redis-exporter
    haproxy:
        image:
            repository: cgr.dev/chainguard/haproxy
    image:
        repository: cgr.dev/chainguard/redis
server:
    extensions:
        image:
            repository: cgr.dev/chainguard/argocd-extension-installer
```

You can pass this output to commands like `helm install`, `helm upgrade` and `helm
template` with the `-f` flag.

## Verify

Validate that the mapper has mapped all the images to Chainguard by passing the
output to `helm template`.

```
$ helm show values argocd/argo-cd | ./image-mapper helm-values - | helm template argocd/argo-cd -f - | grep 'image:'
          image: cgr.dev/chainguard/argocd:v3.2.1
          image: cgr.dev/chainguard/argocd:v3.2.1
        image: cgr.dev/chainguard/argocd:v3.2.1
        image: cgr.dev/chainguard/argocd:v3.2.1
        image: cgr.dev/chainguard/argocd:v3.2.1
        image: cgr.dev/chainguard/dex:v2.44.0
        image: cgr.dev/chainguard/argocd:v3.2.1
        image: cgr.dev/chainguard/redis:8.2.2-alpine
        image: cgr.dev/chainguard/argocd:v3.2.1
        image: cgr.dev/chainguard/argocd:v3.2.1
```

## Override Repository

Use the `--repository` flag to override the repository with your own mirror or
proxy.

```
$ helm show values argocd/argo-cd | ./image-mapper helm-values - --repository=registry.example.internal/chainguard-mirror
dex:
    image:
        repository: registry.example.internal/chainguard-mirror/dex
global:
    image:
        repository: registry.example.internal/chainguard-mirror/argocd
...
```

