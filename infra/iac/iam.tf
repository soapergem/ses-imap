# --------------------------------------------------------------------------
# Lambda execution role
# --------------------------------------------------------------------------

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
  name               = "${local.lambda_function}-role"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume_role.json

  tags = {
    Project = local.project_tag
  }
}

resource "aws_iam_role_policy_attachment" "lambda_basic_execution" {
  role       = aws_iam_role.lambda_role.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

data "aws_iam_policy_document" "lambda_policy" {
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
  name   = "${local.lambda_function}-policy"
  role   = aws_iam_role.lambda_role.id
  policy = data.aws_iam_policy_document.lambda_policy.json
}

# --------------------------------------------------------------------------
# IMAP server policy document (for K8s pod role / IRSA)
# --------------------------------------------------------------------------

data "aws_iam_policy_document" "imap_server" {
  statement {
    effect = "Allow"
    actions = [
      "dynamodb:BatchWriteItem",
      "dynamodb:GetItem",
      "dynamodb:PutItem",
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
      "s3:PutObject",
    ]
    resources = ["arn:aws:s3:::${var.s3_bucket}/*"]
  }

  statement {
    effect = "Allow"
    actions = [
      "ssm:GetParameter",
    ]
    resources = ["arn:aws:ssm:${var.aws_region}:${local.account_id}:parameter${local.ssm_prefix}/*"]
  }
}

# --------------------------------------------------------------------------
# IMAP server IAM user + access key
# --------------------------------------------------------------------------

resource "aws_iam_user" "imap_server" {
  name = "${local.name_prefix}ses-imap-server"

  tags = {
    Project = local.project_tag
  }
}

resource "aws_iam_user_policy" "imap_server" {
  name   = "${local.name_prefix}ses-imap-server-policy"
  user   = aws_iam_user.imap_server.name
  policy = data.aws_iam_policy_document.imap_server.json
}

# Allow the IMAP server user to send email via SES SMTP.
data "aws_iam_policy_document" "ses_send" {
  statement {
    effect    = "Allow"
    actions   = ["ses:SendRawEmail"]
    resources = ["*"]
  }
}

resource "aws_iam_user_policy" "ses_send" {
  name   = "${local.name_prefix}ses-imap-ses-send"
  user   = aws_iam_user.imap_server.name
  policy = data.aws_iam_policy_document.ses_send.json
}

resource "aws_iam_access_key" "imap_server" {
  user = aws_iam_user.imap_server.name
}
