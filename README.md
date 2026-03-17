# ses-imap

An IMAP server that provides read access to emails received by Amazon SES and stored in S3. SES handles all inbound mail delivery; this service is purely a read layer that lets you connect with any standard IMAP client (Thunderbird, Apple Mail, etc.).

## Architecture

```
Inbound email
  -> Amazon SES receipt rule (per mailbox)
      1. S3 action: writes raw RFC 5322 message to S3
      2. Lambda action: parses headers, writes metadata to DynamoDB
      3. (optional) Additional Lambda actions

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
| Terraform | `infra/iac/` | DynamoDB table, Lambda, IAM roles, SES receipt rules, SSM parameters |

## Prerequisites

- Go 1.24+
- AWS account with SES configured for receiving email
- An S3 bucket where SES stores incoming messages
- A Kubernetes cluster (for the IMAP server)
- [just](https://github.com/casey/just) (command runner)
- [podman](https://podman.io/) (for container builds)
- Terraform 1.0+

## Environment Variables

Several Justfile recipes depend on environment variables for account-specific configuration. Set these in your shell or via a `.envrc` file (gitignored by default):

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

## Terraform Configuration

Terraform-specific configuration goes in `infra/iac/terraform.tfvars` (gitignored). The key variable is `mailboxes`, which defines the SES receipt rules to create. Each mailbox gets its own rule with an S3 action and the ingest Lambda, plus any additional Lambdas you specify.

### Basic example (single mailbox)

```hcl
s3_bucket         = "my-ses-email-bucket"
ses_rule_set_name = "default-rule-set"

mailboxes = {
  info = {
    recipients = ["info@example.com"]
    s3_prefix  = "example.com/info"
  }
}
```

### Multiple mailboxes with additional Lambdas and ordering

```hcl
s3_bucket         = "my-ses-email-bucket"
ses_rule_set_name = "default-rule-set"

mailboxes = {
  info = {
    recipients = ["info@example.com"]
    s3_prefix  = "example.com/info"
  }
  alerts = {
    recipients         = ["alerts@example.com"]
    s3_prefix          = "example.com/alerts"
    additional_lambdas = ["arn:aws:lambda:us-east-1:123456789012:function:alert-processor"]
    after              = "info"
  }
}
```

### Domain catch-all

```hcl
s3_bucket         = "my-ses-email-bucket"
ses_rule_set_name = "default-rule-set"

mailboxes = {
  catchall = {
    recipients = ["example.com"]
    s3_prefix  = "example.com/catchall"
  }
}
```

### No SES rules (manage rules separately)

If `mailboxes` is omitted or empty, no SES receipt rules are created. The DynamoDB table, Lambda, IAM roles, and SSM parameters are still provisioned, and you can add the Lambda to your existing SES rules manually.

```hcl
s3_bucket         = "my-ses-email-bucket"
ses_rule_set_name = "default-rule-set"
```

### Mailbox fields

| Field | Required | Description |
|---|---|---|
| `recipients` | Yes | List of email addresses or domains to match |
| `s3_prefix` | Yes | S3 key prefix for storing messages |
| `additional_lambdas` | No | List of Lambda ARNs to invoke in addition to the ingest Lambda |
| `after` | No | Key of another mailbox to place this rule after (controls SES rule ordering) |

## Quick Start

### 1. Create IMAP users

Users are stored as bcrypt hashes in SSM Parameter Store. The IMAP username is the full email address (e.g., `user@example.com`), and the `@` is replaced with `/` in the SSM path. Each user can only access the IMAP mailbox matching their username.

```bash
# Generate a bcrypt hash
HASH=$(htpasswd -bnBC 10 "" 'mypassword' | tr -d ':\n' | sed 's/$2y/$2a/')

# Create a per-user credential
aws ssm put-parameter \
  --name "/ses-imap/users/user/example.com" \
  --type SecureString \
  --value "$HASH"
```

Or via Terraform (keys use `localpart/domain` format):

```hcl
imap_users = {
  "user/example.com" = "$2a$10$..."
}
```

#### Domain-level shared credentials

You can also create a domain-level credential that acts as a shared password for any address on that domain. This is useful for catch-all mailboxes where you don't know the addresses in advance. Per-user credentials take precedence over domain-level ones.

```bash
# Create a domain-level credential
aws ssm put-parameter \
  --name "/ses-imap/users/example.com" \
  --type SecureString \
  --value "$HASH"
```

With this in place, any `@example.com` address can log in using the shared password and access their own mailbox. For example, `random@example.com` logs in, the auth system checks for `/ses-imap/users/random/example.com` first, falls back to `/ses-imap/users/example.com`, and grants access only to the `random@example.com` mailbox.

### 2. Deploy infrastructure

Create `infra/iac/terraform.tfvars` with your configuration (see above), then:

```bash
just init
just plan
just apply
```

### 3. Build and push the Lambda

```bash
just build-lambda
# Upload lambda.zip via Terraform (already handled by just apply)
```

### 4. Build and push the container image

```bash
just setup       # one-time: initialize QEMU for multi-arch builds
just build
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

This matches the SES receipt rule behavior when the S3 action prefix is set to `<domain>/<localpart>` via the `s3_prefix` field in the `mailboxes` variable.

## Configuration

### IMAP Server (environment variables)

| Variable | Default | Description |
|---|---|---|
| `AWS_REGION` | `us-east-1` | AWS region |
| `DYNAMODB_TABLE` | `ses-imap-messages` | DynamoDB table name |
| `S3_BUCKET` | *(required)* | S3 bucket with SES messages |
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
| `DEFAULT_MAILBOX` | `INBOX` | Default mailbox for incoming messages |

### Terraform Variables

| Variable | Default | Description |
|---|---|---|
| `prefix` | `""` | Optional prefix for all resource names |
| `aws_region` | `us-east-1` | AWS region |
| `lambda_zip_path` | `../../lambda.zip` | Path to Lambda zip |
| `s3_bucket` | *(required)* | S3 bucket for SES messages |
| `default_mailbox` | `INBOX` | Default mailbox |
| `ses_rule_set_name` | *(required)* | Existing SES receipt rule set |
| `mailboxes` | `{}` | Map of mailbox definitions (see above) |
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

To see a list of all the available Justfile recipes, type `just`.

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
