resource "aws_dynamodb_table" "messages" {
  name         = local.dynamodb_table
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
    Project = local.project_tag
  }
}
