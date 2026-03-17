# Grant SES permission to invoke additional Lambdas referenced by mailbox rules.
resource "aws_lambda_permission" "ses_invoke_additional" {
  for_each = local.additional_lambda_arns

  statement_id   = "AllowSESInvoke-${replace(each.value, "/[^a-zA-Z0-9]/", "-")}"
  action         = "lambda:InvokeFunction"
  function_name  = each.value
  principal      = "ses.amazonaws.com"
  source_account = local.account_id
}

resource "aws_ses_receipt_rule" "mailbox" {
  for_each = var.mailboxes

  name          = "${local.name_prefix}ses-imap-${each.key}"
  rule_set_name = var.ses_rule_set_name
  recipients    = each.value.recipients
  enabled       = true
  scan_enabled  = true
  after         = each.value.after == null ? null : "${local.name_prefix}ses-imap-${each.value.after}"

  s3_action {
    bucket_name       = var.s3_bucket
    object_key_prefix = each.value.s3_prefix
    position          = 1
  }

  # Ingest Lambda: indexes message metadata in DynamoDB.
  lambda_action {
    function_arn    = aws_lambda_function.ses_ingest.arn
    invocation_type = "Event"
    position        = 2
  }

  # Additional Lambdas per mailbox (e.g., existing per-user processors).
  dynamic "lambda_action" {
    for_each = each.value.additional_lambdas
    content {
      function_arn    = lambda_action.value
      invocation_type = "Event"
      position        = 3 + lambda_action.key
    }
  }

  depends_on = [aws_lambda_permission.ses_invoke, aws_lambda_permission.ses_invoke_additional]
}
