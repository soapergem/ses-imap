output "dynamodb_table_arn" {
  description = "ARN of the DynamoDB messages table"
  value       = aws_dynamodb_table.messages.arn
}

output "dynamodb_table_name" {
  description = "Name of the DynamoDB messages table"
  value       = local.dynamodb_table
}

output "imap_server_policy_json" {
  description = "IAM policy JSON for the IMAP server (attach to your K8s pod role)"
  value       = data.aws_iam_policy_document.imap_server.json
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
