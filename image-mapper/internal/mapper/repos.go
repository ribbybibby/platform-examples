package mapper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
)

// RepoList describes a list of repos in the catalog
type RepoList struct {
	Repos     []Repo    `json:"repos"`
	FetchedAt time.Time `json:"fetchedAt"`
}

// Repo describes a repo in the catalog
type Repo struct {
	Name        string   `json:"name"`
	CatalogTier string   `json:"catalogTier"`
	Aliases     []string `json:"aliases"`
	ActiveTags  []string `json:"activeTags"`
	Tags        []Tag    `json:"tags"`
}

// Tag is a tag in a repository
type Tag struct {
	Name string `json:"name"`
}

// RepoClient lists repos in the catalog
type RepoClient interface {
	ListRepos(ctx context.Context) (*RepoList, error)
}

type repoClient struct {
	url string
}

// NewRepoClient returns a repo client that lists repositories from the
// Chainguard catalog
func NewRepoClient(url string) RepoClient {
	return &repoClient{url: url}
}

// ListRepos lists repositories via a GraphQL query
func (rc *repoClient) ListRepos(ctx context.Context) (*RepoList, error) {
	log.Printf("Fetching list of repositories from Chainguard catalog...")
	c := &http.Client{}

	body := struct {
		Query string `json:"query"`
	}{
		Query: `
query ChainguardPrivateImageCatalog {
  repos(filter: {uidp: {childrenOf: "ce2d1984a010471142503340d670612d63ffb9f6"}}) {
    name
    aliases
    catalogTier
    activeTags
    tags(filter: {excludeDates: true, excludeEpochs: true, excludeReferrers: true}) {
      name
    }
  }
}
`,
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rc.url, &buf)
	if err != nil {
		return nil, fmt.Errorf("constructing request: %w", err)
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("User-Agent", "image-mapper")

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var data struct {
		Data struct {
			Repos []Repo `json:"repos"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("unmarshaling body: %w", err)
	}

	return &RepoList{
		Repos:     fixAliases(data.Data.Repos),
		FetchedAt: time.Now(),
	}, nil
}

type cachingRepoClient struct {
	repoList      *RepoList
	repoClient    RepoClient
	cacheDuration time.Duration
	lock          sync.RWMutex
}

// NewCachingRepoClient returns a repo client that wraps the provided repo
// client, caching the results for the indicated amount of time
func NewCachingRepoClient(cacheDuration time.Duration, repoClient RepoClient) RepoClient {
	return &cachingRepoClient{
		repoClient:    repoClient,
		cacheDuration: cacheDuration,
		lock:          sync.RWMutex{},
	}
}

// ListRepos lists repos from an in memory cache until the cache duration is
// exceeded, at which point it'll list repos using the wrapped client
func (rc *cachingRepoClient) ListRepos(ctx context.Context) (*RepoList, error) {
	rc.lock.Lock()
	defer rc.lock.Unlock()

	if rc.repoList != nil && time.Since(rc.repoList.FetchedAt) < rc.cacheDuration {
		return rc.repoList, nil
	}

	repoList, err := rc.repoClient.ListRepos(ctx)
	if err != nil {
		return nil, err
	}

	rc.repoList = repoList

	return repoList, nil
}

type fileCachingRepoClient struct {
	repoClient    RepoClient
	cacheDuration time.Duration
	cacheDir      string
}

// NewFileCachingRepoClient constructs a repo client that caches repos to a
// location on disk for the amount of time indicated by cache duration before
// fetching them again with the wrapped client
func NewFileCachingRepoClient(cacheDuration time.Duration, repoClient RepoClient) (RepoClient, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("finding cache dir: %w", err)
	}

	return &fileCachingRepoClient{
		repoClient:    repoClient,
		cacheDuration: cacheDuration,
		cacheDir:      filepath.Join(cacheDir, "chainguard-image-mapper"),
	}, nil
}

// ListRepos will list repos from a file on disk until the cache duration
// exceeded, at which point it will list repos from the wrapped client
func (rc *fileCachingRepoClient) ListRepos(ctx context.Context) (*RepoList, error) {
	cachedList, err := rc.getRepoList(ctx)
	if err != nil && !errors.Is(err, errNotFoundInCache) {
		return nil, err
	}
	if cachedList != nil && time.Since(cachedList.FetchedAt) < rc.cacheDuration {
		return cachedList, nil
	}

	repoList, err := rc.repoClient.ListRepos(ctx)
	if err != nil {
		return nil, err
	}

	if err := rc.putRepoList(ctx, repoList); err != nil {
		return nil, err
	}

	return repoList, nil
}

var errNotFoundInCache = errors.New("not found in cache")

func (rc *fileCachingRepoClient) getRepoList(ctx context.Context) (*RepoList, error) {
	cacheData, err := os.ReadFile(filepath.Join(rc.cacheDir, "repos.json"))
	if os.IsNotExist(err) {
		return nil, errNotFoundInCache
	}
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	var repoList RepoList
	if err := json.Unmarshal(cacheData, &repoList); err != nil {
		return nil, fmt.Errorf("unmarshaling data: %w", err)
	}

	return &repoList, nil
}

func (rc *fileCachingRepoClient) putRepoList(ctx context.Context, repoList *RepoList) error {
	if err := os.MkdirAll(rc.cacheDir, 0755); err != nil {
		return fmt.Errorf("creating cache directory: %w", err)
	}

	data, err := json.Marshal(repoList)
	if err != nil {
		return fmt.Errorf("marshaling repo list: %w", err)
	}

	if err := os.WriteFile(filepath.Join(rc.cacheDir, "repos.json"), data, 0644); err != nil {
		return fmt.Errorf("writing cache file: %w", err)
	}

	return nil
}

func flattenTags(tags []Tag) []string {
	var flattened []string
	for _, tag := range tags {
		flattened = append(flattened, tag.Name)
	}

	return flattened
}

// parseRepo parses a 'repository' as expressed as a registry hostname
// (foo.bar.com) or a repository name (foo.bar.com/foo/bar).
func parseRepo(repo string) (string, error) {
	if ref, err := name.NewRegistry(repo); err == nil {
		return ref.String(), nil
	}

	if ref, err := name.NewRepository(repo); err == nil {
		return ref.String(), nil
	}

	return "", fmt.Errorf("can't parse repository: %s", repo)
}

// fixAliases corrects some notoriously incorrect aliases in the repository
// data. Generally these are cases where we associate multiple images in the
// same 'family' with every image in the 'family'.
//
// Naturally, this should be fixed in the actual data but that's
// non-trivial to do at the moment. So, until such time, we'll do it here to
// improve the results in the short term.
func fixAliases(repos []Repo) []Repo {
	for i, repo := range repos {
		for name, aliases := range aliasesFixes {
			if repo.Name != name {
				continue
			}
			repos[i].Aliases = aliases
		}
	}

	return repos
}

var aliasesFixes = map[string][]string{
	"argocd-repo-server":      {},
	"argocd-repo-server-fips": {},
	"argo-cli": {
		"quay.io/argoproj/argocli",
	},
	"argo-cli-fips": {
		"quay.io/argoproj/argocli",
	},
	"argo-events": {
		"quay.io/argoproj/argo-events",
	},
	"argo-events-fips": {
		"quay.io/argoproj/argo-events",
	},
	"argo-exec": {
		"quay.io/argoproj/argoexec",
	},
	"argo-exec-fips": {
		"quay.io/argoproj/argoexec",
	},
	"argo-workflowcontroller": {
		"quay.io/argoproj/workflow-controller",
	},
	"argo-workflowcontroller-fips": {
		"quay.io/argoproj/workflow-controller",
	},
	"crossplane-aws": {
		"ghcr.io/crossplane-contrib/provider-family-aws",
	},
	"crossplane-aws-cloudformation": {
		"ghcr.io/crossplane-contrib/provider-aws-cloudformation",
	},
	"crossplane-aws-cloudformation-fips": {
		"ghcr.io/crossplane-contrib/provider-aws-cloudformation",
	},
	"crossplane-aws-cloudfront": {
		"ghcr.io/crossplane-contrib/provider-aws-cloudfront",
	},
	"crossplane-aws-cloudfront-fips": {
		"ghcr.io/crossplane-contrib/provider-aws-cloudfront",
	},
	"crossplane-aws-cloudwatchlogs": {
		"ghcr.io/crossplane-contrib/provider-aws-cloudwatchlogs",
	},
	"crossplane-aws-cloudwatchlogs-fips": {
		"ghcr.io/crossplane-contrib/provider-aws-cloudwatchlogs",
	},
	"crossplane-aws-dynamodb": {
		"ghcr.io/crossplane-contrib/provider-aws-dynamodb",
	},
	"crossplane-aws-dynamodb-fips": {
		"ghcr.io/crossplane-contrib/provider-aws-dynamodb",
	},
	"crossplane-aws-ec2": {
		"ghcr.io/crossplane-contrib/provider-aws-ec2",
	},
	"crossplane-aws-ec2-fips": {
		"ghcr.io/crossplane-contrib/provider-aws-ec2",
	},
	"crossplane-aws-eks": {
		"ghcr.io/crossplane-contrib/provider-aws-eks",
	},
	"crossplane-aws-eks-fips": {
		"ghcr.io/crossplane-contrib/provider-aws-eks",
	},
	"crossplane-aws-elasticache": {
		"ghcr.io/crossplane-contrib/provider-aws-elasticache",
	},
	"crossplane-aws-elasticache-fips": {
		"ghcr.io/crossplane-contrib/provider-aws-elasticache",
	},
	"crossplane-aws-fips": {
		"ghcr.io/crossplane-contrib/provider-family-aws",
	},
	"crossplane-aws-firehose": {
		"ghcr.io/crossplane-contrib/provider-aws-firehose",
	},
	"crossplane-aws-firehose-fips": {
		"ghcr.io/crossplane-contrib/provider-aws-firehose",
	},
	"crossplane-aws-iam": {
		"ghcr.io/crossplane-contrib/provider-aws-iam",
	},
	"crossplane-aws-iam-fips": {
		"ghcr.io/crossplane-contrib/provider-aws-iam",
	},
	"crossplane-aws-kinesis": {
		"ghcr.io/crossplane-contrib/provider-aws-kinesis",
	},
	"crossplane-aws-kinesis-fips": {
		"ghcr.io/crossplane-contrib/provider-aws-kinesis",
	},
	"crossplane-aws-kms": {
		"ghcr.io/crossplane-contrib/provider-aws-kms",
	},
	"crossplane-aws-kms-fips": {
		"ghcr.io/crossplane-contrib/provider-aws-kms",
	},
	"crossplane-aws-lambda": {
		"ghcr.io/crossplane-contrib/provider-aws-lambda",
	},
	"crossplane-aws-lambda-fips": {
		"ghcr.io/crossplane-contrib/provider-aws-lambda",
	},
	"crossplane-aws-memorydb": {
		"ghcr.io/crossplane-contrib/provider-aws-memorydb",
	},
	"crossplane-aws-memorydb-fips": {
		"ghcr.io/crossplane-contrib/provider-aws-memorydb",
	},
	"crossplane-aws-rds": {
		"ghcr.io/crossplane-contrib/provider-aws-rds",
	},
	"crossplane-aws-rds-fips": {
		"ghcr.io/crossplane-contrib/provider-aws-rds",
	},
	"crossplane-aws-route53": {
		"ghcr.io/crossplane-contrib/provider-aws-route53",
	},
	"crossplane-aws-route53-fips": {
		"ghcr.io/crossplane-contrib/provider-aws-route53",
	},
	"crossplane-aws-s3": {
		"ghcr.io/crossplane-contrib/provider-aws-s3",
	},
	"crossplane-aws-s3-fips": {
		"ghcr.io/crossplane-contrib/provider-aws-s3",
	},
	"crossplane-aws-sns": {
		"ghcr.io/crossplane-contrib/provider-aws-sns",
	},
	"crossplane-aws-sns-fips": {
		"ghcr.io/crossplane-contrib/provider-aws-sns",
	},
	"crossplane-aws-sqs": {
		"ghcr.io/crossplane-contrib/provider-aws-sqs",
	},
	"crossplane-aws-sqs-fips": {
		"ghcr.io/crossplane-contrib/provider-aws-sqs",
	},
	"cert-manager-acmesolver": {
		"quay.io/jetstack/cert-manager-acmesolver",
	},
	"cert-manager-acmesolver-fips": {
		"quay.io/jetstack/cert-manager-acmesolver",
	},
	"cert-manager-acmesolver-iamguarded": {
		"quay.io/jetstack/cert-manager-acmesolver",
	},
	"cert-manager-acmesolver-iamguarded-fips": {
		"quay.io/jetstack/cert-manager-acmesolver",
	},
	"cert-manager-cainjector": {
		"quay.io/jetstack/cert-manager-cainjector",
	},
	"cert-manager-cainjector-fips": {
		"quay.io/jetstack/cert-manager-cainjector",
	},
	"cert-manager-cainjector-iamguarded": {
		"quay.io/jetstack/cert-manager-cainjector",
	},
	"cert-manager-cainjector-iamguarded-fips": {
		"quay.io/jetstack/cert-manager-cainjector",
	},
	"cert-manager-cmctl": {
		"quay.io/jetstack/cmctl",
	},
	"cert-manager-cmctl-fips": {
		"quay.io/jetstack/cmctl",
	},
	"cert-manager-webhook": {
		"quay.io/jetstack/cert-manager-webhook",
	},
	"cert-manager-webhook-fips": {
		"quay.io/jetstack/cert-manager-webhook",
	},
	"cert-manager-webhook-iamguarded": {
		"quay.io/jetstack/cert-manager-webhook",
	},
	"cert-manager-webhook-iamguarded-fips": {
		"quay.io/jetstack/cert-manager-webhook",
	},
	"flux": {
		"ghcr.io/fluxcd/flux-cli",
	},
	"flux-fips": {
		"ghcr.io/fluxcd/flux-cli",
	},
	"flux-helm-controller": {
		"ghcr.io/fluxcd/helm-controller",
	},
	"flux-helm-controller-fips": {
		"ghcr.io/fluxcd/helm-controller",
	},
	"flux-image-automation-controller": {
		"ghcr.io/fluxcd/image-automation-controller",
	},
	"flux-image-automation-controller-fips": {
		"ghcr.io/fluxcd/image-automation-controller",
	},
	"flux-image-reflector-controller": {
		"ghcr.io/fluxcd/image-reflector-controller",
	},
	"flux-image-reflector-controller-fips": {
		"ghcr.io/fluxcd/image-reflector-controller",
	},
	"flux-kustomize-controller": {
		"ghcr.io/fluxcd/kustomize-controller",
	},
	"flux-kustomize-controller-fips": {
		"ghcr.io/fluxcd/kustomize-controller",
	},
	"flux-notification-controller": {
		"ghcr.io/fluxcd/notification-controller",
	},
	"flux-notification-controller-fips": {
		"ghcr.io/fluxcd/notification-controller",
	},
	"flux-source-controller": {
		"ghcr.io/fluxcd/source-controller",
	},
	"flux-source-controller-fips": {
		"ghcr.io/fluxcd/source-controller",
	},
	"kyverno-cli": {
		"ghcr.io/kyverno/kyverno-cli",
	},
	"kyverno-cli-fips": {
		"ghcr.io/kyverno/kyverno-cli-fips",
	},
	"kyverno": {
		"ghcr.io/kyverno/kyverno",
	},
	"kyverno-fips": {
		"ghcr.io/kyverno/kyverno",
	},
	"kyvernopre": {
		"ghcr.io/kyverno/kyvernopre",
	},
	"kyvernopre-fips": {
		"ghcr.io/kyverno/kyvernopre",
	},
	"kyverno-background-controller": {
		"ghcr.io/kyverno/background-controller",
	},
	"kyverno-background-controller-fips": {
		"ghcr.io/kyverno/background-controller",
	},
	"kyverno-cleanup-controller": {
		"ghcr.io/kyverno/cleanup-controller",
	},
	"kyverno-cleanup-controller-fips": {
		"ghcr.io/kyverno/cleanup-controller",
	},
	"kyverno-reports-controller": {
		"ghcr.io/kyverno/reports-controller",
	},
	"kyverno-reports-controller-fips": {
		"ghcr.io/kyverno/reports-controller",
	},
	"minio-client": {
		"quay.io/minio/mc",
	},
	"minio-client-fips": {
		"quay.io/minio/mc",
	},
	"minio-operator": {
		"quay.io/minio/operator",
	},
	"minio-operator-fips": {
		"quay.io/minio/operator",
	},
	"minio-operator-sidecar": {
		"quay.io/minio/operator-sidecar",
	},
	"minio-operator-sidecar-fips": {
		"quay.io/minio/operator-sidecar",
	},
	"mongodb-kubernetes-operator-readinessprobe": {
		"quay.io/mongodb/mongodb-kubernetes-readinessprobe",
	},
	"mongodb-kubernetes-operator-readinessprobe-fips": {
		"quay.io/mongodb/mongodb-kubernetes-readinessprobe",
	},
	"mongodb-kubernetes-operator-version-upgrade-post-start-hook": {
		"quay.io/mongodb/mongodb-kubernetes-operator-version-upgrade-post-start-hook",
	},
	"mongodb-kubernetes-operator-version-upgrade-post-start-hook-fips": {
		"quay.io/mongodb/mongodb-kubernetes-operator-version-upgrade-post-start-hook",
	},
	"postgres-cloudnative-pg": {
		"ghcr.io/cloudnative-pg/postgresql",
	},
	"postgres-cloudnative-pg-fips": {
		"ghcr.io/cloudnative-pg/postgresql",
	},
	"vault-k8s": {
		"hashicorp/vault-k8s",
	},
}
