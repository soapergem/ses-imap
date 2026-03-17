output "dynamodb_table_arn" {
  description = "ARN of the DynamoDB messages table"
  value       = aws_dynamodb_table.messages.arn
}

output "dynamodb_table_name" {
  description = "Name of the DynamoDB messages table"
  value       = local.dynamodb_table
}

output "lambda_function_arn" {
  description = "ARN of the SES ingest Lambda function"
  value       = aws_lambda_function.ses_ingest.arn
}

output "lambda_role_arn" {
  description = "ARN of the Lambda execution role"
  value       = aws_iam_role.lambda_role.arn
}

output "ssm_prefix" {
  description = "SSM Parameter Store prefix for IMAP users"
  value       = local.ssm_prefix
}

output "imap_server_access_key_id" {
  description = "AWS access key ID for the IMAP server"
  value       = aws_iam_access_key.imap_server.id
}

output "imap_server_secret_access_key" {
  description = "AWS secret access key for the IMAP server"
  value       = aws_iam_access_key.imap_server.secret
  sensitive   = true
}

output "ses_smtp_password_v4" {
  description = "SES SMTP password (for sending email via SES SMTP interface)"
  value       = aws_iam_access_key.imap_server.ses_smtp_password_v4
  sensitive   = true
}
