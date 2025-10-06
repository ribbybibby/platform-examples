/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	awsauth "chainguard.dev/sdk/auth/aws"
	common "chainguard.dev/sdk/proto/platform/common/v1"
	v1 "chainguard.dev/sdk/proto/platform/registry/v1"

	"chainguard.dev/sdk/sts"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/ecr/types"
	ecrcreds "github.com/awslabs/amazon-ecr-credential-helper/ecr-login"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/kelseyhightower/envconfig"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	amazonKeychain authn.Keychain = authn.NewKeychainFromHelper(ecrcreds.NewECRHelper(ecrcreds.WithLogger(log.Writer())))

	kc = authn.NewMultiKeychain(
		&cgKeychain{
			mu: sync.Mutex{},
		},
		amazonKeychain,
	)
)

var (
	apiEndpoint = "https://console-api.enforce.dev"
	issuerURL   = "https://issuer.enforce.dev"
)

var env = struct {
	OrgName         string        `envconfig:"ORG_NAME" required:"true"`
	OrgID           string        `envconfig:"ORG_ID" required:"true"`
	IdentityID      string        `envconfig:"IDENTITY_ID" required:"true"`
	DstRepoName     string        `envconfig:"DST_REPO_NAME" required:"true"`
	DstRepoURI      string        `envconfig:"DST_REPO_URI" required:"true"`
	Region          string        `envconfig:"AWS_REGION" required:"true"`
	IgnoreReferrers bool          `envconfig:"IGNORE_REFERRERS" required:"true"`
	UpdatedWithin   time.Duration `envconfig:"UPDATED_WITHIN" required:"true"`
}{}

var credsProvider aws.CredentialsProvider

func init() {
	if err := envconfig.Process("", &env); err != nil {
		log.Fatalf("failed to process env var: %s", err)
	}

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("loading AWS config: %s", err)
	}
	credsProvider = cfg.Credentials
}

func main() {
	ctx := context.Background()

	if err := copyImages(ctx); err != nil {
		log.Fatalf("ERROR: %s", err)
	}
}

// copyImages iterates over all the recently updated images in a Chainguard
// organization and copies them to ECR
func copyImages(ctx context.Context) error {
	// Parse the destination repo
	dstRepo, err := name.NewRepository(env.DstRepoURI)
	if err != nil {
		return fmt.Errorf("parsing destination repository: %s: %w", env.DstRepoURI, err)
	}

	// Create a client for ECR
	ecrClient := ecr.New(ecr.Options{
		Credentials: credsProvider,
		Region:      env.Region,
	})

	// Create a client for Chainguard
	registry, err := newRegistryClient(ctx)
	if err != nil {
		return fmt.Errorf("creating registry client: %w", err)
	}

	// List repositories
	repoFilter := &v1.RepoFilter{
		Uidp: &common.UIDPFilter{
			ChildrenOf: env.OrgID,
		},
	}
	repoList, err := registry.ListRepos(ctx, repoFilter)
	if err != nil {
		return fmt.Errorf("listing repos: %w", err)
	}

	// For each repository, list tags
	for _, repo := range repoList.Items {
		// Construct the source repo from the name
		srcRepo, err := name.NewRepository(fmt.Sprintf("cgr.dev/%s/%s", env.OrgName, repo.Name))
		if err != nil {
			return fmt.Errorf("parsing destination repository: %w", err)
		}

		// List repo tags
		filter := &v1.TagFilter{
			Uidp: &common.UIDPFilter{
				ChildrenOf: repo.Id,
			},
			ExcludeReferrers: env.IgnoreReferrers,
			UpdatedSince:     timestamppb.New(time.Now().Add(-env.UpdatedWithin)),
		}
		tagList, err := registry.ListTags(ctx, filter)
		if err != nil {
			return fmt.Errorf("listing tags: %w", err)
		}

		// Ensure the ECR repository exists before we copy tags
		if len(tagList.Items) > 0 {
			if err := createECRRepo(ctx, ecrClient, repo.Name); err != nil {
				return fmt.Errorf("creating ECR repository: %s: %w", repo.Name, err)
			}
		}

		for _, tag := range tagList.Items {
			src := srcRepo.Tag(tag.Name).String()
			dst := dstRepo.Repo(dstRepo.RepositoryStr(), repo.Name).Tag(tag.Name).String()
			// Copy tag
			log.Printf("Copying %s to %s...", src, dst)
			if err := crane.Copy(src, dst, crane.WithAuthFromKeychain(kc)); err != nil {
				return fmt.Errorf("copying image: %w", err)
			}
			log.Printf("Copied %s to %s", src, dst)
		}
	}

	return nil
}

// newRegistryClient creates a new v1.RegistryClient for the Chainguard API
func newRegistryClient(ctx context.Context) (v1.RegistryClient, error) {
	// Generate a token for the Chainguard API
	tok, err := newToken(ctx, apiEndpoint)
	if err != nil {
		return nil, fmt.Errorf("getting token: %w", err)
	}

	// Create client that uses the token
	clients, err := v1.NewClients(ctx, apiEndpoint, tok.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("creating clients: %w", err)
	}

	return clients.Registry(), nil
}

// createECRRepo creates an ECR repo, unless it already exists
func createECRRepo(ctx context.Context, ecrClient *ecr.Client, repoName string) error {
	repo := filepath.Join(env.DstRepoName, repoName)
	in := &ecr.CreateRepositoryInput{
		RepositoryName: &repo,
	}
	if _, err := ecrClient.CreateRepository(ctx, in); err != nil {
		var rae *types.RepositoryAlreadyExistsException
		if errors.As(err, &rae) {
			log.Printf("ECR repo %s already exists", repo)
			return nil
		}

		return fmt.Errorf("creating repository: %s: %w", repo, err)
	}

	return nil
}

// newToken generates a token using the AWS identity configured in the
// environment.
//
// It does this by first generating an AWS token, then exchanging it for a Chainguard token for the
// specified Chainguard identity, which has been set up to be assumed by the AWS identity.
func newToken(ctx context.Context, audience string) (*sts.TokenPair, error) {
	creds, err := credsProvider.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve credentials, %w", err)
	}

	awsTok, err := awsauth.GenerateToken(ctx, creds, issuerURL, env.IdentityID)
	if err != nil {
		return nil, fmt.Errorf("generating AWS token: %w", err)
	}

	exch := sts.New(issuerURL, audience, sts.WithIdentity(env.IdentityID))
	cgTok, err := exch.Exchange(ctx, awsTok)
	if err != nil {
		return nil, fmt.Errorf("exchanging token: %w", err)
	}

	return &cgTok, nil
}

// cgKeychain is an authn.Keychain that provides a Chainguard token capable of pulling from cgr.dev.
type cgKeychain struct {
	mu  sync.Mutex
	tok *sts.TokenPair
}

func (k *cgKeychain) Resolve(res authn.Resource) (authn.Authenticator, error) {
	if res.RegistryStr() != "cgr.dev" {
		return authn.Anonymous, nil
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	if k.tok == nil || k.tok.Expiry.Add(-5*time.Minute).Before(time.Now()) {
		tok, err := newToken(context.Background(), res.RegistryStr())
		if err != nil {
			return nil, fmt.Errorf("getting token: %w", err)
		}
		k.tok = tok
	}

	return &authn.Basic{
		Username: "_token",
		Password: k.tok.AccessToken,
	}, nil
}
