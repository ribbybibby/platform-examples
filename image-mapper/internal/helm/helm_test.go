package helm

import (
	"testing"

	"github.com/chainguard-dev/customer-success/scripts/image-mapper/internal/mapper"
	"github.com/google/go-cmp/cmp"
)

type mockMapper struct {
	mappings map[string][]string
}

func (m *mockMapper) Map(img string) (*mapper.Mapping, error) {
	return &mapper.Mapping{
		Image:   img,
		Results: m.mappings[img],
	}, nil
}

func TestMapValues(t *testing.T) {
	input := []byte(`
image: quay.io/argoproj/argocd
redis-example:
    exporter:
        enabled: true
        image: ghcr.io/oliver006/redis_exporter
        tag: v1.75.0
    haproxy:
        enabled: false
        image:
            repository: ecr-public.aws.com/docker/library/haproxy
    image:
        registry: ecr-public.aws.com
        repository: docker/library/redis

global:
  revisionHistoryLimit: 3
  image:
    repository: quay.io/argoproj/argocd
    tag: ""

prometheus-example:
  admissionWebhooks:
      deployment:
          image:
              registry: "quay.io"
              repository: prometheus-operator/admission-webhook
      patch:
          image:
              registry: "ghcr.io"
              repository: jkroepke/kube-webhook-certgen
  image:
      registry: ""
      repository: prometheus-operator/prometheus-operator
      tag: ""
      sha: ""
`)

	want := []byte(`global:
    image:
        repository: cgr.dev/chainguard/argocd
image: cgr.dev/chainguard/argocd
prometheus-example:
    admissionWebhooks:
        deployment:
            image:
                registry: cgr.dev
                repository: chainguard/prometheus-admission-webhook
        patch:
            image:
                registry: cgr.dev
                repository: chainguard/kube-webhook-certgen
    image:
        registry: cgr.dev
        repository: chainguard/prometheus-operator
redis-example:
    exporter:
        image: cgr.dev/chainguard/prometheus-redis-exporter
    haproxy:
        image:
            repository: cgr.dev/chainguard/haproxy
`)

	m := &mockMapper{
		mappings: map[string][]string{
			"ecr-public.aws.com/docker/library/haproxy": {
				"cgr.dev/chainguard/haproxy:latest",
			},
			"ghcr.io/jkroepke/kube-webhook-certgen": {
				"cgr.dev/chainguard/kube-webhook-certgen:latest",
			},
			"ghcr.io/oliver006/redis_exporter": {
				"cgr.dev/chainguard/prometheus-redis-exporter:latest",
			},
			"quay.io/argoproj/argocd": {
				"cgr.dev/chainguard/argocd:latest",
			},
			"quay.io/prometheus-operator/admission-webhook": {
				"cgr.dev/chainguard/prometheus-admission-webhook:latest",
			},
			"prometheus-operator/prometheus-operator": {
				"cgr.dev/chainguard/prometheus-operator:latest",
			},
		},
	}

	got, err := MapValues(m, input)
	if err != nil {
		t.Errorf("unexpected error: %s", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("unexpected output:\n%s", diff)
	}
}
