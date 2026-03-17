variable "aws_region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

variable "default_mailbox" {
  description = "Default mailbox name for incoming messages"
  type        = string
  default     = "INBOX"
}

variable "imap_users" {
  description = "Map of IMAP username to bcrypt password hash. Generate with: htpasswd -bnBC 10 '' 'password' | tr -d ':\\n' | sed 's/$2y/$2a/'"
  type        = map(string)
  default     = {}
}

variable "lambda_zip_path" {
  description = "Path to the Lambda deployment zip file"
  type        = string
  default     = "../../lambda.zip"
}

variable "mailboxes" {
  description = "Map of mailbox definitions. Each key becomes part of the SES rule name. Use 'after' to reference another mailbox key for ordering."
  type = map(object({
    recipients         = list(string)
    s3_prefix          = string
    additional_lambdas = optional(list(string), [])
    after              = optional(string)
  }))
  default = {}
}

variable "prefix" {
  description = "Optional prefix for all resource names (e.g., 'myapp' produces 'myapp-ses-imap-messages')"
  type        = string
  default     = ""
}

variable "s3_bucket" {
  description = "S3 bucket where SES stores incoming email"
  type        = string
}

variable "ses_rule_set_name" {
  description = "Name of the existing SES receipt rule set"
  type        = string
}
