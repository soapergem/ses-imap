resource "aws_lambda_function" "ses_ingest" {
  function_name = local.lambda_function
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
      DYNAMODB_TABLE  = local.dynamodb_table
      S3_BUCKET       = var.s3_bucket
      DEFAULT_MAILBOX = var.default_mailbox
      S3_PREFIX_MAP   = jsonencode(local.s3_prefix_map)
    }
  }

  tags = {
    Project = local.project_tag
  }
}

resource "aws_lambda_permission" "ses_invoke" {
  statement_id   = "AllowSESInvoke"
  action         = "lambda:InvokeFunction"
  function_name  = aws_lambda_function.ses_ingest.function_name
  principal      = "ses.amazonaws.com"
  source_account = local.account_id
}
