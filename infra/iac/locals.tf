data "aws_caller_identity" "this" {}

locals {
  account_id      = data.aws_caller_identity.this.account_id
  name_prefix     = var.prefix == "" ? "" : "${var.prefix}-"
  dynamodb_table  = "${local.name_prefix}ses-imap-messages"
  lambda_function = "${local.name_prefix}ses-imap-ingest"
  ssm_prefix      = "/${local.name_prefix}ses-imap/users"
  project_tag     = "${local.name_prefix}ses-imap"

  # Build a map of recipient -> S3 prefix from the mailboxes variable.
  # Flattens multiple recipients per mailbox into individual entries.
  s3_prefix_map = merge([
    for _, mb in var.mailboxes : {
      for r in mb.recipients : r => mb.s3_prefix
    }
  ]...)
}
