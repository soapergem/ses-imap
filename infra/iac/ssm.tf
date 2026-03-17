# Users are stored as SecureString parameters at <ssm_prefix>/<localpart>/<domain>.
# The IMAP username is the full email address (e.g., "gordon@example.com"),
# and the @ is replaced with / in the SSM path.
#
# Values are bcrypt hashes of the user's password.
#
# To create a user manually:
#   HASH=$(htpasswd -bnBC 10 "" 'mypassword' | tr -d ':\n' | sed 's/$2y/$2a/')
#   aws ssm put-parameter \
#     --name "/ses-imap/users/gordon/example.com" \
#     --type SecureString \
#     --value "$HASH"
#
# The Terraform below creates users from var.imap_users.
# Keys should use the "localpart/domain" format (e.g., "gordon/example.com").

resource "aws_ssm_parameter" "imap_user" {
  for_each = var.imap_users

  name  = "${local.ssm_prefix}/${each.key}"
  type  = "SecureString"
  value = each.value

  tags = {
    Project = local.project_tag
  }
}
