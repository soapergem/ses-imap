# ses-imap

An IMAP server that provides read access to emails received by Amazon SES and stored in S3. SES handles all inbound mail delivery; this service is purely a read layer that lets you connect with any standard IMAP client (Thunderbird, Apple Mail, etc.).

## Architecture

```
Inbound email
  -> Amazon SES receipt rule
      1. S3 action: writes raw RFC 5322 message to S3
      2. Lambda action: parses headers, writes metadata to DynamoDB

IMAP client (Thunderbird, Apple Mail, etc.)
  -> IMAP server (this project, running in K8s)
      -> reads metadata from DynamoDB
      -> fetches message bodies from S3
      -> manages flags/state in DynamoDB
      -> authenticates users via SSM Parameter Store
```

## Components

| Component | Location | Description |
|---|---|---|
| IMAP server | `cmd/imap-server/` | Go binary that serves IMAP backed by DynamoDB + S3 |
| SES Lambda | `cmd/ses-lambda/` | Lambda function triggered by SES to index messages in DynamoDB |
| Store layer | `internal/store/` | DynamoDB metadata + S3 body access + SSM auth |
| Helm chart | `infra/deploy/` | Kubernetes deployment for the IMAP server |
| Terraform | `infra/iac/` | DynamoDB table, Lambda, IAM roles, SES receipt rule, SSM parameters |

## Prerequisites

- Go 1.24+
- AWS account with SES configured for receiving email
- An S3 bucket where SES stores incoming messages
- A Kubernetes cluster (for the IMAP server)
- [just](https://github.com/casey/just) (command runner)
- [podman](https://podman.io/) (for container builds)
- Terraform 1.0+

## Environment Variables

Several Justfile recipes depend on environment variables for account-specific configuration.

| Variable | Required | Description |
|---|---|---|
| `AWS_ACCOUNT_ID` | Yes | Your AWS account ID (used for ECR image URLs) |
| `S3_BUCKET` | Yes | S3 bucket where SES stores incoming email |
| `ECR_REPO` | No | ECR repository name (default: `ses-imap`) |
| `AWS_REGION` | No | AWS region (default: `us-east-1`) |

Example `.envrc`:

```bash
export AWS_ACCOUNT_ID="123456789012"
export AWS_REGION="us-east-1"
export ECR_REPO="ses-imap"
export S3_BUCKET="my-ses-email-bucket"
```

## Quick Start

### 1. Create IMAP users

Users are stored as bcrypt hashes in SSM Parameter Store under `/ses-imap/users/{username}`.

```bash
# Generate a bcrypt hash
HASH=$(htpasswd -bnBC 10 "" 'mypassword' | tr -d ':\n' | sed 's/$2y/$2a/')

# Store in Parameter Store
aws ssm put-parameter \
  --name "/ses-imap/users/myuser" \
  --type SecureString \
  --value "$HASH"
```

Or via Terraform by setting the `imap_users` variable (see below).

### 2. Deploy infrastructure

```bash
just init

# Create a terraform.tfvars file
cat > infra/iac/terraform.tfvars <<EOF
s3_bucket         = "my-ses-email-bucket"
ses_rule_set_name = "my-rule-set"
ses_recipients    = ["example.com"]

imap_users = {
  "myuser" = "$2a$10$..."  # bcrypt hash from step 1
}
EOF

just plan
just apply
```

### 3. Build and push the Lambda

```bash
just package-lambda
# Upload lambda.zip via Terraform (already handled by apply)
```

### 4. Build and push the container image

```bash
just setup       # one-time: initialize QEMU for multi-arch builds
just build-image
just login
just push
```

### 5. Deploy to Kubernetes

Create the K8s secret with AWS credentials:

```bash
kubectl create secret generic ses-imap \
  --from-literal=aws-access-key=AKIA... \
  --from-literal=aws-secret-key=...
```

Deploy the Helm chart:

```bash
just deploy
```

## S3 Key Layout

The Lambda derives the S3 object key from the recipient email address. For a message to `user@example.com` with SES message ID `abc123`, the expected S3 key is:

```
example.com/user/abc123
```

This matches the default SES receipt rule behavior when the S3 action prefix is set to `<domain>/<localpart>`. If your S3 layout differs, you can set the `S3_PREFIX` environment variable on the Lambda to use a static prefix instead (the key becomes `<S3_PREFIX><message-id>`).

## Configuration

### IMAP Server (environment variables)

| Variable | Default | Description |
|---|---|---|
| `AWS_REGION` | `us-east-1` | AWS region |
| `DYNAMODB_TABLE` | `ses-imap-messages` | DynamoDB table name |
| `S3_BUCKET` | *(required)* | S3 bucket with SES messages |
| `S3_PREFIX` | `""` | S3 key prefix for SES messages |
| `IMAP_ADDR` | `:143` | IMAP listen address |
| `SSM_PREFIX` | `/ses-imap/users` | SSM parameter prefix for user credentials |
| `SSM_CACHE_TTL` | `300` | Credential cache TTL in seconds |
| `DEFAULT_MAILBOX` | `INBOX` | Default mailbox name |

### Lambda (environment variables)

| Variable | Default | Description |
|---|---|---|
| `AWS_REGION` | `us-east-1` | AWS region |
| `DYNAMODB_TABLE` | `ses-imap-messages` | DynamoDB table name |
| `S3_BUCKET` | *(required)* | S3 bucket with SES messages |
| `S3_PREFIX` | `""` | Static S3 key prefix (overrides recipient-based derivation if set) |
| `DEFAULT_MAILBOX` | `INBOX` | Default mailbox for incoming messages |

### Terraform Variables

| Variable | Default | Description |
|---|---|---|
| `aws_region` | `us-east-1` | AWS region |
| `dynamodb_table_name` | `ses-imap-messages` | DynamoDB table name |
| `lambda_function_name` | `ses-imap-ingest` | Lambda function name |
| `lambda_zip_path` | `../../lambda.zip` | Path to Lambda zip |
| `s3_bucket` | *(required)* | S3 bucket for SES messages |
| `s3_prefix` | `""` | S3 key prefix |
| `default_mailbox` | `INBOX` | Default mailbox |
| `ses_rule_set_name` | *(required)* | Existing SES receipt rule set |
| `ses_recipients` | *(required)* | Email addresses/domains to match |
| `ssm_prefix` | `/ses-imap/users` | SSM prefix for IMAP users |
| `imap_users` | `{}` | Map of username to bcrypt hash |

## DynamoDB Schema

Single table (`ses-imap-messages`) with composite key:

| Key | Type | Description |
|---|---|---|
| `mailbox` (PK) | String | Mailbox name or recipient address |
| `uid` (SK) | Number | IMAP UID (0 = mailbox metadata sentinel) |

Message attributes: `s3_key`, `message_id`, `from_addr`, `from_display`, `to_addr`, `subject`, `internal_date`, `size`, `flags`.

Mailbox metadata (uid=0): `uid_next`, `uid_validity`.

## Justfile Recipes

```
just build          # build both IMAP server and Lambda binaries
just build-server   # build IMAP server only
just build-lambda   # build Lambda only
just package-lambda # build + zip Lambda for deployment
just test           # run tests
just build-image    # build multi-arch container image
just login          # authenticate with ECR
just push           # push container image to ECR
just deploy         # install/upgrade Helm chart
just undeploy       # uninstall Helm chart
just init        # terraform init
just plan        # terraform plan
just apply       # terraform apply
just clean          # remove build artifacts
just prune          # remove container images
just setup          # initialize QEMU for multi-arch builds
```

## IMAP Capabilities

Supported:
- LOGIN authentication (via SSM Parameter Store + bcrypt)
- SELECT, LIST, STATUS
- FETCH (headers, body sections, envelope, body structure, flags, sizes)
- STORE (flag updates: \Seen, \Answered, \Flagged, \Deleted, \Draft)
- SEARCH (by flags, header fields, UID ranges)
- EXPUNGE
- IDLE (polls DynamoDB every 30s for new messages)

Not supported (by design):
- APPEND (messages are ingested only via SES)
- COPY/MOVE
- DELETE/RENAME mailboxes
- Full-text body search

## License

MIT
