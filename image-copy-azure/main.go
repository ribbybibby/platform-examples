/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	cgevents "chainguard.dev/sdk/events"
	"chainguard.dev/sdk/events/registry"
	v1 "chainguard.dev/sdk/proto/platform/registry/v1"
	"chainguard.dev/sdk/sts"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/chrismellard/docker-credential-acr-env/pkg/credhelper"
	"github.com/coreos/go-oidc"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/kelseyhightower/envconfig"
)

var azureKeychain authn.Keychain = authn.NewKeychainFromHelper(credhelper.NewACRCredentialsHelper())

var env = struct {
	APIEndpoint      string `envconfig:"API_ENDPOINT" required:"true"`
	Issuer           string `envconfig:"ISSUER_URL" required:"true"`
	GroupName        string `envconfig:"GROUP_NAME" required:"true"`
	Group            string `envconfig:"GROUP" required:"true"`
	Identity         string `envconfig:"IDENTITY" required:"true"`
	DstRepo          string `envconfig:"DST_REPO" required:"true"`
	HandlerPort      string `envconfig:"FUNCTIONS_CUSTOMHANDLER_PORT" default:"8080" required:"true"`
	RegistryUsername string `envconfig:"REGISTRY_USERNAME" required:"true"`
	RegistryPassword string `envconfig:"REGISTRY_PASSWORD" required:"true"`
}{}

func init() {
	if err := envconfig.Process("", &env); err != nil {
		log.Fatalf("failed to process env var: %s", err)
	}
}

func main() {
	http.HandleFunc("/api/imagecopy", handler)

	log.Println("Starting server on port " + env.HandlerPort)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", env.HandlerPort), nil))
}

func handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, fmt.Errorf("method not allowed: %s", r.Method), http.StatusMethodNotAllowed)
		return
	}

	// Verify the token in the Authorization header
	verifyAuth(w, r)

	// Check that the event is one we care about:
	// - It's a registry push event.
	// - It's not a push error.
	// - It's a tag push.
	if ceType := r.Header.Get("ce-type"); ceType != registry.PushedEventType {
		log.Printf("event type is %q, skipping", ceType)
		return
	}
	body := registry.PushEvent{}
	data := cgevents.Occurrence{Body: &body}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		httpError(w, fmt.Errorf("decoding event body: %w", err), http.StatusBadRequest)
		return
	}
	if body.Error != nil {
		log.Printf("event body has error, skipping: %+v", body.Error)
		return
	}
	if body.Tag == "" || body.Type != "manifest" {
		log.Printf("event body is not a tag push, skipping: %q %q", body.Tag, body.Type)
		return
	}

	// Resolve the repository ID to the name
	repoName, err := resolveRepositoryName(r.Context(), body.RepoID)
	if err != nil {
		httpError(w, fmt.Errorf("resolving repository name: %w", err), http.StatusInternalServerError)
		return
	}

	// Sync src:tag to dst:tag.
	src := "cgr.dev/" + env.GroupName + "/" + repoName + ":" + body.Tag
	dst := filepath.Join(env.DstRepo, repoName) + ":" + body.Tag
	kc := authn.NewMultiKeychain(
		staticKeychain{},
		cgKeychain{},
		azureKeychain,
	)
	log.Printf("Copying %s to %s...", src, dst)
	if err := crane.Copy(src, dst, crane.WithAuthFromKeychain(kc)); err != nil {
		httpError(w, fmt.Errorf("copying image: %w", err), http.StatusInternalServerError)
		return
	}
	log.Printf("Copied %s to %s", src, dst)
}

func verifyAuth(w http.ResponseWriter, r *http.Request) {
	// Extract the token from the authorization header
	auth := strings.TrimPrefix(r.Header.Get("authorization"), "Bearer ")
	if auth == "" {
		httpError(w, fmt.Errorf("unauthorized: missing authorization header"), http.StatusUnauthorized)
		return
	}

	// Verify that the token was issued by https://issuer.enforce.dev
	provider, err := oidc.NewProvider(r.Context(), env.Issuer)
	if err != nil {
		httpError(w, fmt.Errorf("constructing OIDC provider: %w", err), http.StatusInternalServerError)
		return
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: "customer"})
	tok, err := verifier.Verify(r.Context(), auth)
	if err != nil {
		httpError(w, fmt.Errorf("verifying token: %w", err), http.StatusUnauthorized)
		return
	}

	// The subject of the token should be webhook:<org id>. This indicates
	// that the event was issued by our webhook component, for your
	// organization.
	if !strings.HasPrefix(tok.Subject, "webhook:") {
		httpError(w, fmt.Errorf("subject should be from the Chainguard webhook component"), http.StatusUnauthorized)
		return
	}
	if group := strings.TrimPrefix(tok.Subject, "webhook:"); group != env.Group {
		httpError(w, fmt.Errorf("token intended for incorrect group: got %s, wanted %s", group, env.Group), http.StatusUnauthorized)
		return
	}
}

func httpError(w http.ResponseWriter, err error, code int) {
	msg := fmt.Sprintf("Error: %s", err)
	log.Print(msg)
	http.Error(w, msg, code)
}

func resolveRepositoryName(ctx context.Context, repoID string) (string, error) {
	// Generate a token for the Chainguard API
	tok, err := newToken(ctx, env.APIEndpoint)
	if err != nil {
		return "", fmt.Errorf("getting token: %w", err)
	}

	// Create client that uses the token
	client, err := v1.NewClients(ctx, env.APIEndpoint, tok.AccessToken)
	if err != nil {
		return "", fmt.Errorf("creating clients: %w", err)
	}

	// Lookup the repository name from the ID
	repoList, err := client.Registry().ListRepos(ctx, &v1.RepoFilter{
		Id: repoID,
	})
	if err != nil {
		return "", fmt.Errorf("listing repositories: %w", err)
	}
	for _, repo := range repoList.Items {
		return repo.Name, nil
	}

	return "", fmt.Errorf("couldn't find repository name for id: %s", repoID)
}

// newToken generates a token using the Azure identity of the function.
//
// It does this first by fetching a token from Azure, then exchanging it for a
// Chainguard token for the specified Chainguard identity, which has been set up
// to be assumed by the Azure identity.
func newToken(ctx context.Context, audience string) (*sts.TokenPair, error) {
	// Get Azure credentials from the default chain
	creds, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("getting credentials: %w", err)
	}

	// Fetch a token with the credentials
	tok, err := creds.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{
			"https://management.core.windows.net/.default",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("getting token: %w", err)
	}

	// Exchange the Azure token for a Chainguard token
	exch := sts.New(env.Issuer, audience, sts.WithIdentity(env.Identity))
	cgTok, err := exch.Exchange(ctx, tok.Token)
	if err != nil {
		return nil, fmt.Errorf("exchanging token: %w", err)
	}

	return &cgTok, nil
}

// TODO: remove this once I've sorted out access via role bindings
type staticKeychain struct{}

func (k staticKeychain) Resolve(res authn.Resource) (authn.Authenticator, error) {
	if res.RegistryStr() != env.DstRepo {
		return authn.Anonymous, nil
	}

	return &authn.Basic{
		Username: env.RegistryUsername,
		Password: env.RegistryPassword,
	}, nil
}

// cgKeychain is an authn.Keychain that provides a Chainguard token capable of
// pulling from cgr.dev.
type cgKeychain struct{}

func (k cgKeychain) Resolve(res authn.Resource) (authn.Authenticator, error) {
	if res.RegistryStr() != "cgr.dev" {
		return authn.Anonymous, nil
	}

	tok, err := newToken(context.Background(), res.RegistryStr())
	if err != nil {
		return nil, fmt.Errorf("getting token: %w", err)
	}

	return &authn.Basic{
		Username: "_token",
		Password: tok.AccessToken,
	}, nil
}
