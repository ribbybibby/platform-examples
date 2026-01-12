package mapper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// TestRepoClientListRepos tests the basic HTTP repoClient
func TestRepoClientListRepos(t *testing.T) {
	testCases := []struct {
		name           string
		responseBody   interface{}
		responseStatus int
		expectError    bool
		expectedRepos  []Repo
	}{
		{
			name: "successful response",
			responseBody: map[string]interface{}{
				"data": map[string]interface{}{
					"repos": []map[string]interface{}{
						{
							"name":        "nginx",
							"catalogTier": "APPLICATION",
							"aliases":     []string{"nginx-alias"},
							"activeTags":  []string{"latest", "1.25"},
							"tags": []map[string]interface{}{
								{"name": "latest"},
								{"name": "1.25"},
							},
						},
						{
							"name":        "redis",
							"catalogTier": "APPLICATION",
							"aliases":     []string{},
							"activeTags":  []string{"7.0"},
							"tags": []map[string]interface{}{
								{"name": "7.0"},
							},
						},
					},
				},
			},
			responseStatus: http.StatusOK,
			expectError:    false,
			expectedRepos: []Repo{
				{
					Name:        "nginx",
					CatalogTier: "APPLICATION",
					Aliases:     []string{"nginx-alias"},
					ActiveTags:  []string{"latest", "1.25"},
					Tags: []Tag{
						{Name: "latest"},
						{Name: "1.25"},
					},
				},
				{
					Name:        "redis",
					CatalogTier: "APPLICATION",
					Aliases:     []string{},
					ActiveTags:  []string{"7.0"},
					Tags: []Tag{
						{Name: "7.0"},
					},
				},
			},
		},
		{
			name:           "http error - 500",
			responseBody:   map[string]interface{}{},
			responseStatus: http.StatusInternalServerError,
			expectError:    true,
		},
		{
			name:           "http error - 404",
			responseBody:   map[string]interface{}{},
			responseStatus: http.StatusNotFound,
			expectError:    true,
		},
		{
			name:           "invalid json response",
			responseBody:   "invalid json",
			responseStatus: http.StatusOK,
			expectError:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request method and headers
				if r.Method != http.MethodPost {
					t.Errorf("expected POST request, got %s", r.Method)
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("expected Content-Type: application/json, got %s", r.Header.Get("Content-Type"))
				}
				if r.Header.Get("User-Agent") != "image-mapper" {
					t.Errorf("expected User-Agent: image-mapper, got %s", r.Header.Get("User-Agent"))
				}

				w.WriteHeader(tc.responseStatus)
				if str, ok := tc.responseBody.(string); ok {
					w.Write([]byte(str))
				} else {
					json.NewEncoder(w).Encode(tc.responseBody)
				}
			}))
			defer server.Close()

			// Create a client with the test server URL
			client := NewRepoClient(server.URL)

			ctx := context.Background()
			result, err := client.ListRepos(ctx)

			if tc.expectError {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(result.Repos) != len(tc.expectedRepos) {
				t.Errorf("expected %d repos, got %d", len(tc.expectedRepos), len(result.Repos))
			}

			// Compare repos (ignoring time fields)
			opts := cmpopts.IgnoreFields(RepoList{}, "FetchedAt")
			if diff := cmp.Diff(tc.expectedRepos, result.Repos, opts); diff != "" {
				t.Errorf("repos mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestCachingRepoClient tests the in-memory caching wrapper
func TestCachingRepoClient(t *testing.T) {
	t.Run("cache hit within duration", func(t *testing.T) {
		// Create a mock client that counts calls
		callCount := 0
		mockClient := &mockRepoClient{
			repos: []Repo{
				{Name: "nginx", CatalogTier: "APPLICATION"},
			},
		}
		// Wrap the mock to count calls
		countingClient := &countingRepoClient{
			client:    mockClient,
			callCount: &callCount,
		}

		cachingClient := NewCachingRepoClient(1*time.Hour, countingClient)

		ctx := context.Background()

		// First call should fetch
		result1, err := cachingClient.ListRepos(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if callCount != 1 {
			t.Errorf("expected 1 call to underlying client, got %d", callCount)
		}

		// Second call should use cache
		result2, err := cachingClient.ListRepos(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if callCount != 1 {
			t.Errorf("expected 1 call to underlying client (cached), got %d", callCount)
		}

		// Results should be identical
		if diff := cmp.Diff(result1, result2); diff != "" {
			t.Errorf("cached result mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("cache miss after duration", func(t *testing.T) {
		callCount := 0
		mockClient := &mockRepoClient{
			repos: []Repo{
				{Name: "nginx", CatalogTier: "APPLICATION"},
			},
		}
		countingClient := &countingRepoClient{
			client:    mockClient,
			callCount: &callCount,
		}

		// Very short cache duration
		cachingClient := NewCachingRepoClient(1*time.Millisecond, countingClient)

		ctx := context.Background()

		// First call
		_, err := cachingClient.ListRepos(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if callCount != 1 {
			t.Errorf("expected 1 call, got %d", callCount)
		}

		// Wait for cache to expire
		time.Sleep(2 * time.Millisecond)

		// Second call should fetch again
		_, err = cachingClient.ListRepos(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if callCount != 2 {
			t.Errorf("expected 2 calls after cache expiry, got %d", callCount)
		}
	})

	t.Run("propagates errors from underlying client", func(t *testing.T) {
		errorClient := &errorRepoClient{
			err: context.Canceled,
		}

		cachingClient := NewCachingRepoClient(1*time.Hour, errorClient)

		ctx := context.Background()
		_, err := cachingClient.ListRepos(ctx)
		if err == nil {
			t.Error("expected error from underlying client")
		}
	})
}

// TestFileCachingRepoClient tests the file-based caching wrapper
func TestFileCachingRepoClient(t *testing.T) {
	t.Run("cache miss - fetch and write", func(t *testing.T) {
		// Create a temporary cache directory
		tempDir := t.TempDir()

		callCount := 0
		mockClient := &mockRepoClient{
			repos: []Repo{
				{Name: "nginx", CatalogTier: "APPLICATION"},
				{Name: "redis", CatalogTier: "APPLICATION"},
			},
		}
		countingClient := &countingRepoClient{
			client:    mockClient,
			callCount: &callCount,
		}

		fileClient := &fileCachingRepoClient{
			repoClient:    countingClient,
			cacheDuration: 1 * time.Hour,
			cacheDir:      filepath.Join(tempDir, "test-cache"),
		}

		ctx := context.Background()

		// First call should fetch and write to cache
		result, err := fileClient.ListRepos(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if callCount != 1 {
			t.Errorf("expected 1 call to underlying client, got %d", callCount)
		}
		if len(result.Repos) != 2 {
			t.Errorf("expected 2 repos, got %d", len(result.Repos))
		}

		// Verify cache file was created
		cacheFile := filepath.Join(tempDir, "test-cache", "repos.json")
		if _, err := os.Stat(cacheFile); os.IsNotExist(err) {
			t.Error("cache file was not created")
		}
	})

	t.Run("cache hit from file", func(t *testing.T) {
		// Create a temporary cache directory
		tempDir := t.TempDir()
		cacheDir := filepath.Join(tempDir, "test-cache")
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			t.Fatalf("failed to create cache dir: %v", err)
		}

		// Write a cache file
		cachedRepoList := &RepoList{
			Repos: []Repo{
				{Name: "cached-nginx", CatalogTier: "APPLICATION"},
			},
			FetchedAt: time.Now(),
		}
		cacheData, _ := json.Marshal(cachedRepoList)
		cacheFile := filepath.Join(cacheDir, "repos.json")
		if err := os.WriteFile(cacheFile, cacheData, 0644); err != nil {
			t.Fatalf("failed to write cache file: %v", err)
		}

		// Create a client that should never be called
		callCount := 0
		mockClient := &mockRepoClient{
			repos: []Repo{
				{Name: "fresh-nginx", CatalogTier: "APPLICATION"},
			},
		}
		countingClient := &countingRepoClient{
			client:    mockClient,
			callCount: &callCount,
		}

		fileClient := &fileCachingRepoClient{
			repoClient:    countingClient,
			cacheDuration: 1 * time.Hour,
			cacheDir:      cacheDir,
		}

		ctx := context.Background()

		// Should read from cache
		result, err := fileClient.ListRepos(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if callCount != 0 {
			t.Errorf("expected 0 calls to underlying client (cached), got %d", callCount)
		}
		if len(result.Repos) != 1 || result.Repos[0].Name != "cached-nginx" {
			t.Errorf("expected cached data, got %+v", result.Repos)
		}
	})

	t.Run("cache expired - refetch", func(t *testing.T) {
		// Create a temporary cache directory
		tempDir := t.TempDir()
		cacheDir := filepath.Join(tempDir, "test-cache")
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			t.Fatalf("failed to create cache dir: %v", err)
		}

		// Write an expired cache file
		cachedRepoList := &RepoList{
			Repos: []Repo{
				{Name: "old-nginx", CatalogTier: "APPLICATION"},
			},
			FetchedAt: time.Now().Add(-2 * time.Hour), // 2 hours ago
		}
		cacheData, _ := json.Marshal(cachedRepoList)
		cacheFile := filepath.Join(cacheDir, "repos.json")
		if err := os.WriteFile(cacheFile, cacheData, 0644); err != nil {
			t.Fatalf("failed to write cache file: %v", err)
		}

		// Create a client with fresh data
		callCount := 0
		mockClient := &mockRepoClient{
			repos: []Repo{
				{Name: "fresh-nginx", CatalogTier: "APPLICATION"},
			},
		}
		countingClient := &countingRepoClient{
			client:    mockClient,
			callCount: &callCount,
		}

		fileClient := &fileCachingRepoClient{
			repoClient:    countingClient,
			cacheDuration: 1 * time.Hour,
			cacheDir:      cacheDir,
		}

		ctx := context.Background()

		// Should fetch fresh data
		result, err := fileClient.ListRepos(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if callCount != 1 {
			t.Errorf("expected 1 call to underlying client (cache expired), got %d", callCount)
		}
		if len(result.Repos) != 1 || result.Repos[0].Name != "fresh-nginx" {
			t.Errorf("expected fresh data, got %+v", result.Repos)
		}
	})

	t.Run("invalid cache file - refetch", func(t *testing.T) {
		// Create a temporary cache directory
		tempDir := t.TempDir()
		cacheDir := filepath.Join(tempDir, "test-cache")
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			t.Fatalf("failed to create cache dir: %v", err)
		}

		// Write invalid JSON to cache file
		cacheFile := filepath.Join(cacheDir, "repos.json")
		if err := os.WriteFile(cacheFile, []byte("invalid json"), 0644); err != nil {
			t.Fatalf("failed to write cache file: %v", err)
		}

		callCount := 0
		mockClient := &mockRepoClient{
			repos: []Repo{
				{Name: "fresh-nginx", CatalogTier: "APPLICATION"},
			},
		}
		countingClient := &countingRepoClient{
			client:    mockClient,
			callCount: &callCount,
		}

		fileClient := &fileCachingRepoClient{
			repoClient:    countingClient,
			cacheDuration: 1 * time.Hour,
			cacheDir:      cacheDir,
		}

		ctx := context.Background()

		// Should handle invalid cache and fetch fresh data
		_, err := fileClient.ListRepos(ctx)
		if err == nil {
			t.Error("expected error from invalid cache file")
		}
	})

	t.Run("no cache file - fetch and create", func(t *testing.T) {
		// Create a temporary cache directory that doesn't have a cache file
		tempDir := t.TempDir()
		cacheDir := filepath.Join(tempDir, "test-cache")

		callCount := 0
		mockClient := &mockRepoClient{
			repos: []Repo{
				{Name: "nginx", CatalogTier: "APPLICATION"},
			},
		}
		countingClient := &countingRepoClient{
			client:    mockClient,
			callCount: &callCount,
		}

		fileClient := &fileCachingRepoClient{
			repoClient:    countingClient,
			cacheDuration: 1 * time.Hour,
			cacheDir:      cacheDir,
		}

		ctx := context.Background()

		// Should fetch and create cache
		result, err := fileClient.ListRepos(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if callCount != 1 {
			t.Errorf("expected 1 call to underlying client, got %d", callCount)
		}

		// Verify cache directory and file were created
		cacheFile := filepath.Join(cacheDir, "repos.json")
		if _, err := os.Stat(cacheFile); os.IsNotExist(err) {
			t.Error("cache file was not created")
		}

		// Verify cache content
		var cached RepoList
		cacheData, err := os.ReadFile(cacheFile)
		if err != nil {
			t.Fatalf("failed to read cache file: %v", err)
		}
		if err := json.Unmarshal(cacheData, &cached); err != nil {
			t.Fatalf("failed to unmarshal cache: %v", err)
		}
		if len(cached.Repos) != 1 || cached.Repos[0].Name != "nginx" {
			t.Errorf("cache content mismatch, got %+v", cached.Repos)
		}

		// Compare with ignore time fields
		opts := cmpopts.IgnoreFields(RepoList{}, "FetchedAt")
		if diff := cmp.Diff(&cached, result, opts); diff != "" {
			t.Errorf("cached data mismatch (-cached +result):\n%s", diff)
		}
	})
}

// Helper types for testing

// countingRepoClient wraps a RepoClient and counts calls
type countingRepoClient struct {
	client    RepoClient
	callCount *int
}

func (c *countingRepoClient) ListRepos(ctx context.Context) (*RepoList, error) {
	*c.callCount++
	return c.client.ListRepos(ctx)
}

// errorRepoClient returns a predefined error
type errorRepoClient struct {
	err error
}

func (c *errorRepoClient) ListRepos(ctx context.Context) (*RepoList, error) {
	return nil, c.err
}

// TestParseRepo tests the parseRepo helper function
func TestParseRepo(t *testing.T) {
	testCases := []struct {
		name        string
		input       string
		expected    string
		expectError bool
	}{
		{
			name:        "registry hostname",
			input:       "cgr.dev",
			expected:    "cgr.dev",
			expectError: false,
		},
		{
			name:        "repository name",
			input:       "cgr.dev/chainguard",
			expected:    "cgr.dev/chainguard",
			expectError: false,
		},
		{
			name:        "nested repository",
			input:       "gcr.io/my-project/my-repo",
			expected:    "gcr.io/my-project/my-repo",
			expectError: false,
		},
		{
			name:        "invalid format",
			input:       "not:a:valid:repo",
			expected:    "",
			expectError: true,
		},
		{
			name:        "empty string returns index.docker.io",
			input:       "",
			expected:    "index.docker.io",
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := parseRepo(tc.input)
			if tc.expectError {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, result)
			}
		})
	}
}

// TestFixAliases tests the fixAliases helper function
func TestFixAliases(t *testing.T) {
	t.Run("fixes known problematic aliases", func(t *testing.T) {
		repos := []Repo{
			{
				Name:    "argocd-repo-server",
				Aliases: []string{"quay.io/argoproj/argocd"},
			},
			{
				Name:    "argo-cli",
				Aliases: []string{"wrong-alias"},
			},
			{
				Name:    "argo-events",
				Aliases: []string{"another-wrong-alias"},
			},
			{
				Name:    "vault-k8s",
				Aliases: []string{"wrong"},
			},
		}

		fixed := fixAliases(repos)

		// Check argocd-repo-server should have no aliases
		if len(fixed[0].Aliases) != 0 {
			t.Errorf("expected argocd-repo-server to have 0 aliases, got %d", len(fixed[0].Aliases))
		}

		// Check argo-cli should have correct alias
		if len(fixed[1].Aliases) != 1 || fixed[1].Aliases[0] != "quay.io/argoproj/argocli" {
			t.Errorf("expected argo-cli to have alias 'quay.io/argoproj/argocli', got %v", fixed[1].Aliases)
		}

		// Check argo-events should have correct alias
		if len(fixed[2].Aliases) != 1 || fixed[2].Aliases[0] != "quay.io/argoproj/argo-events" {
			t.Errorf("expected argo-events to have alias 'quay.io/argoproj/argo-events', got %v", fixed[2].Aliases)
		}

		// Check vault-k8s should have correct alias
		if len(fixed[3].Aliases) != 1 || fixed[3].Aliases[0] != "hashicorp/vault-k8s" {
			t.Errorf("expected vault-k8s to have alias 'hashicorp/vault-k8s', got %v", fixed[3].Aliases)
		}
	})

	t.Run("leaves unknown repos unchanged", func(t *testing.T) {
		repos := []Repo{
			{
				Name:    "nginx",
				Aliases: []string{"nginx-alias"},
			},
			{
				Name:    "redis",
				Aliases: []string{"redis-alias-1", "redis-alias-2"},
			},
		}

		fixed := fixAliases(repos)

		// Nginx should be unchanged
		if len(fixed[0].Aliases) != 1 || fixed[0].Aliases[0] != "nginx-alias" {
			t.Errorf("expected nginx aliases unchanged, got %v", fixed[0].Aliases)
		}

		// Redis should be unchanged
		if len(fixed[1].Aliases) != 2 {
			t.Errorf("expected redis to have 2 aliases, got %d", len(fixed[1].Aliases))
		}
	})

	t.Run("handles empty repo list", func(t *testing.T) {
		repos := []Repo{}
		fixed := fixAliases(repos)
		if len(fixed) != 0 {
			t.Errorf("expected empty list, got %d repos", len(fixed))
		}
	})

	t.Run("handles mixed known and unknown repos", func(t *testing.T) {
		repos := []Repo{
			{
				Name:    "nginx",
				Aliases: []string{"should-stay"},
			},
			{
				Name:    "argo-cli",
				Aliases: []string{"should-be-replaced"},
			},
			{
				Name:    "postgres",
				Aliases: []string{"another-stays"},
			},
		}

		fixed := fixAliases(repos)

		// Nginx unchanged
		if fixed[0].Aliases[0] != "should-stay" {
			t.Errorf("expected nginx alias unchanged, got %v", fixed[0].Aliases)
		}

		// argo-cli fixed
		if fixed[1].Aliases[0] != "quay.io/argoproj/argocli" {
			t.Errorf("expected argo-cli alias fixed, got %v", fixed[1].Aliases)
		}

		// postgres unchanged
		if fixed[2].Aliases[0] != "another-stays" {
			t.Errorf("expected postgres alias unchanged, got %v", fixed[2].Aliases)
		}
	})
}

// TestNewFileCachingRepoClient tests the constructor
func TestNewFileCachingRepoClient(t *testing.T) {
	mockClient := &mockRepoClient{
		repos: []Repo{},
	}

	client, err := NewFileCachingRepoClient(1*time.Hour, mockClient)
	if err != nil {
		t.Fatalf("unexpected error creating file caching client: %v", err)
	}

	if client == nil {
		t.Error("expected non-nil client")
	}

	// Verify it implements the interface
	var _ RepoClient = client
}
