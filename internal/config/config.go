// Package config provides configuration for the SES IMAP service.
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config holds all configuration for the service.
type Config struct {
	// AWS region for DynamoDB and S3.
	AWSRegion string

	// DynamoDB table name for message metadata.
	DynamoDBTable string

	// S3 bucket where SES stores raw messages.
	S3Bucket string

	// S3 prefix map: recipient -> S3 key prefix (for Lambda).
	// JSON-encoded map, e.g., {"gordon@example.com":"example.com/gordon"}
	S3PrefixMap map[string]string

	// IMAP server listen address.
	IMAPAddr string

	// SSM Parameter Store prefix for user credentials.
	// Users are stored as {SSMPrefix}/{username} with bcrypt hash values.
	SSMPrefix string

	// Cache TTL for SSM credentials in seconds.
	SSMCacheTTL int

	// Default mailbox for incoming messages.
	DefaultMailbox string

	// Log level: "debug", "info", "warn", "error".
	LogLevel string
}

// FromEnv loads configuration from environment variables.
func FromEnv() (*Config, error) {
	cfg := &Config{
		AWSRegion:      getEnv("AWS_REGION", "us-east-1"),
		DynamoDBTable:  getEnv("DYNAMODB_TABLE", "ses-imap-messages"),
		S3Bucket:       os.Getenv("S3_BUCKET"),
		IMAPAddr:       getEnv("IMAP_ADDR", ":143"),
		SSMPrefix:      getEnv("SSM_PREFIX", "/ses-imap/users"),
		SSMCacheTTL:    getEnvInt("SSM_CACHE_TTL", 300),
		DefaultMailbox: getEnv("DEFAULT_MAILBOX", "INBOX"),
		LogLevel:       getEnv("LOG_LEVEL", "info"),
	}

	if cfg.S3Bucket == "" {
		return nil, fmt.Errorf("S3_BUCKET is required")
	}

	return cfg, nil
}

// LambdaFromEnv loads configuration for the Lambda function.
func LambdaFromEnv() (*Config, error) {
	cfg := &Config{
		AWSRegion:      getEnv("AWS_REGION", "us-east-1"),
		DynamoDBTable:  getEnv("DYNAMODB_TABLE", "ses-imap-messages"),
		S3Bucket:       os.Getenv("S3_BUCKET"),
		DefaultMailbox: getEnv("DEFAULT_MAILBOX", "INBOX"),
	}

	if cfg.S3Bucket == "" {
		return nil, fmt.Errorf("S3_BUCKET is required")
	}

	if raw := os.Getenv("S3_PREFIX_MAP"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.S3PrefixMap); err != nil {
			return nil, fmt.Errorf("parsing S3_PREFIX_MAP: %w", err)
		}
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return fallback
	}
	return n
}
