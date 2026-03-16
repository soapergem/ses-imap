variable "aws_region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

variable "dynamodb_table_name" {
  description = "Name of the DynamoDB table for message metadata"
  type        = string
  default     = "ses-imap-messages"
}

variable "lambda_function_name" {
  description = "Name of the Lambda function"
  type        = string
  default     = "ses-imap-ingest"
}

variable "lambda_zip_path" {
  description = "Path to the Lambda deployment zip file"
  type        = string
  default     = "../../lambda.zip"
}

variable "s3_bucket" {
  description = "S3 bucket where SES stores incoming email"
  type        = string
}

variable "s3_prefix" {
  description = "S3 key prefix for SES messages"
  type        = string
  default     = ""
}

variable "default_mailbox" {
  description = "Default mailbox name for incoming messages"
  type        = string
  default     = "INBOX"
}

variable "ses_rule_set_name" {
  description = "Name of the existing SES receipt rule set"
  type        = string
}

variable "ses_recipients" {
  description = "List of email addresses/domains to match"
  type        = list(string)
}

variable "ssm_prefix" {
  description = "SSM Parameter Store prefix for IMAP user credentials"
  type        = string
  default     = "/ses-imap/users"
}

variable "imap_users" {
  description = "Map of IMAP username to bcrypt password hash. Generate with: htpasswd -bnBC 10 '' 'password' | tr -d ':\\n' | sed 's/$2y/$2a/'"
  type        = map(string)
  default     = {}
  sensitive   = true
}
