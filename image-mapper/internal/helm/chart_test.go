package helm

import (
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestMapChart(t *testing.T) {
	want := []byte(`redis-ha:
    image:
        repository: cgr.dev/chainguard/redis # Original: ecr-public.aws.com/docker/library/redis
    configmapTest:
        image:
            repository: cgr.dev/chainguard/shellcheck # Original: koalaman/shellcheck
            tag: v0.11.0 # Original: v0.10.0
    haproxy:
        image:
            repository: cgr.dev/chainguard/haproxy # Original: ecr-public.aws.com/docker/library/haproxy
    sysctlImage:
        registry: cgr.dev # Original: public.ecr.aws/docker/library
        repository: chainguard/busybox # Original: busybox
    exporter:
        # Failed to map: ghcr.io/oliver006/redis_exporter:v1.75.0: no results found
        image: ghcr.io/oliver006/redis_exporter
global:
    image:
        repository: cgr.dev/chainguard/argocd # Original: quay.io/argoproj/argocd
dex:
    image:
        repository: cgr.dev/chainguard/dex # Original: ghcr.io/dexidp/dex
redis:
    image:
        repository: cgr.dev/chainguard/redis # Original: ecr-public.aws.com/docker/library/redis
    exporter:
        image:
            repository: cgr.dev/chainguard/prometheus-redis-exporter # Original: ghcr.io/oliver006/redis_exporter
server:
    extensions:
        image:
            repository: cgr.dev/chainguard/argocd-extension-installer # Original: quay.io/argoprojlabs/argocd-extension-installer
`)

	m := &mockMapper{
		mappings: map[string][]string{
			"ecr-public.aws.com/docker/library/haproxy": {
				"cgr.dev/chainguard/haproxy:latest",
			},
			"ecr-public.aws.com/docker/library/redis": {
				"cgr.dev/chainguard/redis:latest",
			},
			"ecr-public.aws.com/docker/library/redis:8.2.2-alpine": {
				"cgr.dev/chainguard/redis:8.2.2-alpine",
			},
			"ghcr.io/dexidp/dex": {
				"cgr.dev/chainguard/dex:latest",
			},
			"ghcr.io/dexidp/dex:v2.44.0": {
				"cgr.dev/chainguard/dex:v2.44.0",
			},
			"ghcr.io/oliver006/redis_exporter": {
				"cgr.dev/chainguard/prometheus-redis-exporter:latest",
			},
			"ghcr.io/oliver006/redis_exporter:v1.80.1": {
				"cgr.dev/chainguard/prometheus-redis-exporter:v1.80.1",
			},
			"koalaman/shellcheck": {
				"cgr.dev/chainguard/shellcheck",
			},
			"koalaman/shellcheck:v0.10.0": {
				"cgr.dev/chainguard/shellcheck:v0.11.0",
			},
			"public.ecr.aws/docker/library/busybox:1.34.1": {
				"cgr.dev/chainguard/busybox:1.34.1",
			},
			"quay.io/argoproj/argocd": {
				"cgr.dev/chainguard/argocd:latest",
			},
			"quay.io/argoprojlabs/argocd-extension-installer": {
				"cgr.dev/chainguard/argocd-extension-installer:latest",
			},
			"quay.io/argoprojlabs/argocd-extension-installer:v0.0.9": {
				"cgr.dev/chainguard/argocd-extension-installer:v0.0.9",
			},
		},
	}

	got, err := mapChart(m, "testdata/argo-cd")
	if err != nil {
		t.Fatalf("unexpected error mapping chart: %s", err)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("unexpected values:\n%s", diff)
	}
}

func TestMapChartIntegration(t *testing.T) {
	if v := os.Getenv("IMAGE_MAPPER_RUN_INTEGRATION_TESTS"); v == "" {
		t.Skip()
	}

	want := []byte(`redis-ha:
    image:
        repository: cgr.dev/chainguard/redis # Original: ecr-public.aws.com/docker/library/redis
        tag: 8.2.2 # Original: 8.2.2-alpine
    configmapTest:
        image:
            repository: cgr.dev/chainguard/shellcheck # Original: koalaman/shellcheck
            tag: v0.11.0 # Original: v0.10.0
    haproxy:
        image:
            repository: cgr.dev/chainguard/haproxy # Original: ecr-public.aws.com/docker/library/haproxy
    sysctlImage:
        registry: cgr.dev # Original: public.ecr.aws/docker/library
        repository: chainguard/busybox # Original: busybox
        tag: 1.36.0 # Original: 1.34.1
    exporter:
        image: cgr.dev/chainguard/prometheus-redis-exporter # Original: ghcr.io/oliver006/redis_exporter
        tag: 1.75.0 # Original: v1.75.0
global:
    image:
        repository: cgr.dev/chainguard/argocd # Original: quay.io/argoproj/argocd
dex:
    image:
        repository: cgr.dev/chainguard/dex # Original: ghcr.io/dexidp/dex
redis:
    image:
        repository: cgr.dev/chainguard/redis # Original: ecr-public.aws.com/docker/library/redis
        tag: 8.2.2 # Original: 8.2.2-alpine
    exporter:
        image:
            repository: cgr.dev/chainguard/prometheus-redis-exporter # Original: ghcr.io/oliver006/redis_exporter
server:
    extensions:
        image:
            repository: cgr.dev/chainguard/argocd-extension-installer # Original: quay.io/argoprojlabs/argocd-extension-installer
`)

	m, err := NewMapper(t.Context())
	if err != nil {
		t.Fatalf("unexpected error constructing mapper: %s", err)
	}

	got, err := mapChart(m, "testdata/argo-cd")
	if err != nil {
		t.Fatalf("unexpected error mapping chart: %s", err)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("unexpected values:\n%s", diff)
	}
}
