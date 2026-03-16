terraform {
  required_version = ">= 1.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
}

# --------------------------------------------------------------------------
# DynamoDB Table
# --------------------------------------------------------------------------

resource "aws_dynamodb_table" "messages" {
  name         = var.dynamodb_table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "mailbox"
  range_key    = "uid"

  attribute {
    name = "mailbox"
    type = "S"
  }

  attribute {
    name = "uid"
    type = "N"
  }

  tags = {
    Project = "ses-imap"
  }
}

# --------------------------------------------------------------------------
# Lambda Function
# --------------------------------------------------------------------------

resource "aws_lambda_function" "ses_ingest" {
  function_name = var.lambda_function_name
  role          = aws_iam_role.lambda_role.arn
  handler       = "bootstrap"
  runtime       = "provided.al2023"
  architectures = ["arm64"]
  timeout       = 30
  memory_size   = 128

  filename         = var.lambda_zip_path
  source_code_hash = filebase64sha256(var.lambda_zip_path)

  environment {
    variables = {
      DYNAMODB_TABLE  = var.dynamodb_table_name
      S3_BUCKET       = var.s3_bucket
      S3_PREFIX       = var.s3_prefix
      DEFAULT_MAILBOX = var.default_mailbox
    }
  }

  tags = {
    Project = "ses-imap"
  }
}

resource "aws_lambda_permission" "ses_invoke" {
  statement_id   = "AllowSESInvoke"
  action         = "lambda:InvokeFunction"
  function_name  = aws_lambda_function.ses_ingest.function_name
  principal      = "ses.amazonaws.com"
  source_account = data.aws_caller_identity.current.account_id
}

# --------------------------------------------------------------------------
# IAM Role for Lambda
# --------------------------------------------------------------------------

data "aws_caller_identity" "current" {}

data "aws_iam_policy_document" "lambda_assume_role" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "lambda_role" {
  name               = "${var.lambda_function_name}-role"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume_role.json

  tags = {
    Project = "ses-imap"
  }
}

data "aws_iam_policy_document" "lambda_policy" {
  statement {
    effect = "Allow"
    actions = [
      "logs:CreateLogGroup",
      "logs:CreateLogStream",
      "logs:PutLogEvents",
    ]
    resources = ["arn:aws:logs:*:*:*"]
  }

  statement {
    effect    = "Allow"
    actions   = ["s3:GetObject"]
    resources = ["arn:aws:s3:::${var.s3_bucket}/*"]
  }

  statement {
    effect = "Allow"
    actions = [
      "dynamodb:PutItem",
      "dynamodb:GetItem",
      "dynamodb:UpdateItem",
      "dynamodb:Query",
    ]
    resources = [aws_dynamodb_table.messages.arn]
  }
}

resource "aws_iam_role_policy" "lambda_policy" {
  name   = "${var.lambda_function_name}-policy"
  role   = aws_iam_role.lambda_role.id
  policy = data.aws_iam_policy_document.lambda_policy.json
}

# --------------------------------------------------------------------------
# SES Receipt Rule (added to an existing rule set)
# --------------------------------------------------------------------------

resource "aws_ses_receipt_rule" "ingest" {
  name          = "ses-imap-ingest"
  rule_set_name = var.ses_rule_set_name
  recipients    = var.ses_recipients
  enabled       = true
  scan_enabled  = true

  # Action 1: Store raw message in S3 (you may already have this).
  s3_action {
    bucket_name       = var.s3_bucket
    object_key_prefix = var.s3_prefix
    position          = 1
  }

  # Action 2: Invoke Lambda to index message metadata in DynamoDB.
  lambda_action {
    function_arn    = aws_lambda_function.ses_ingest.arn
    invocation_type = "Event"
    position        = 2
  }
}

# --------------------------------------------------------------------------
# SSM Parameter Store - IMAP Users
# --------------------------------------------------------------------------
# Users are stored as SecureString parameters at /ses-imap/users/{username}.
# Values are bcrypt hashes of the user's password.
#
# To create a user manually:
#   HASH=$(htpasswd -bnBC 10 "" 'mypassword' | tr -d ':\n' | sed 's/$2y/$2a/')
#   aws ssm put-parameter \
#     --name "/ses-imap/users/myuser" \
#     --type SecureString \
#     --value "$HASH"
#
# The Terraform below creates initial users from var.imap_users.

# --------------------------------------------------------------------------
# IAM Policy Document for IMAP Server (K8s pod role)
# --------------------------------------------------------------------------
# Attach this policy to whatever IAM role your IMAP server pod assumes
# (e.g., via IRSA or a node instance role).

data "aws_iam_policy_document" "imap_server" {
  statement {
    effect = "Allow"
    actions = [
      "dynamodb:GetItem",
      "dynamodb:Query",
      "dynamodb:UpdateItem",
      "dynamodb:DeleteItem",
    ]
    resources = [aws_dynamodb_table.messages.arn]
  }

  statement {
    effect = "Allow"
    actions = [
      "s3:GetObject",
      "s3:HeadObject",
    ]
    resources = ["arn:aws:s3:::${var.s3_bucket}/*"]
  }

  statement {
    effect = "Allow"
    actions = [
      "ssm:GetParameter",
    ]
    resources = ["arn:aws:ssm:${var.aws_region}:${data.aws_caller_identity.current.account_id}:parameter${var.ssm_prefix}/*"]
  }
}

resource "aws_ssm_parameter" "imap_user" {
  for_each = var.imap_users

  name  = "${var.ssm_prefix}/${each.key}"
  type  = "SecureString"
  value = each.value

  tags = {
    Project = "ses-imap"
  }
}
