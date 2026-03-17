package store

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"golang.org/x/crypto/bcrypt"

	"github.com/soapergem/ses-imap/internal/config"
)

// userEntry is a cached user credential.
type userEntry struct {
	bcryptHash string
	fetchedAt  time.Time
}

// Auth provides IMAP authentication backed by SSM Parameter Store.
// Parameters are stored as SecureString at {prefix}/{username} with
// bcrypt-hashed passwords as values.
type Auth struct {
	ssm      *ssm.Client
	prefix   string
	cacheTTL time.Duration

	mu    sync.RWMutex
	cache map[string]*userEntry
}

// NewAuth creates a new Auth provider.
func NewAuth(cfg *config.Config, ssmClient *ssm.Client) *Auth {
	return &Auth{
		ssm:      ssmClient,
		prefix:   cfg.SSMPrefix,
		cacheTTL: time.Duration(cfg.SSMCacheTTL) * time.Second,
		cache:    make(map[string]*userEntry),
	}
}

// Authenticate checks a username/password against the SSM-stored bcrypt hash.
// Returns nil on success, or an error on failure.
func (a *Auth) Authenticate(ctx context.Context, username, password string) error {
	hash, err := a.getBcryptHash(ctx, username)
	if err != nil {
		return fmt.Errorf("authentication failed")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return fmt.Errorf("authentication failed")
	}

	return nil
}

// getBcryptHash retrieves the bcrypt hash for a user, using cache if fresh.
func (a *Auth) getBcryptHash(ctx context.Context, username string) (string, error) {
	// Check cache first.
	a.mu.RLock()
	entry, ok := a.cache[username]
	a.mu.RUnlock()

	if ok && time.Since(entry.fetchedAt) < a.cacheTTL {
		return entry.bcryptHash, nil
	}

	// Fetch from SSM. Replace @ with / so that "gordon@example.com"
	// maps to the parameter path "/ses-imap/users/gordon/example.com".
	paramName := a.prefix + "/" + strings.Replace(username, "@", "/", 1)
	hash, err := a.fetchParam(ctx, paramName)

	// If the per-user parameter doesn't exist, fall back to a domain-level
	// parameter (e.g., "/ses-imap/users/example.com") as a shared credential.
	if err != nil {
		if parts := strings.SplitN(username, "@", 2); len(parts) == 2 {
			domainParam := a.prefix + "/" + parts[1]
			hash, err = a.fetchParam(ctx, domainParam)
		}
	}

	if err != nil {
		log.Printf("SSM lookup failed for user %q: %v", username, err)
		return "", fmt.Errorf("user not found")
	}

	// Update cache.
	a.mu.Lock()
	a.cache[username] = &userEntry{
		bcryptHash: hash,
		fetchedAt:  time.Now(),
	}
	a.mu.Unlock()

	return hash, nil
}

// fetchParam retrieves a single SSM parameter value.
func (a *Auth) fetchParam(ctx context.Context, name string) (string, error) {
	result, err := a.ssm.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", err
	}
	hash := aws.ToString(result.Parameter.Value)
	if hash == "" {
		return "", fmt.Errorf("empty parameter value for %q", name)
	}
	return hash, nil
}
