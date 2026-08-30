terraform {
  required_providers {
    yottabot = {
      source  = "YottaBot-Platform/yottabot"
      version = "~> 0.1"
    }
  }
}

# Static bearer token. Convenient for local and manual runs.
provider "yottabot" {
  endpoint = "https://yottabot.example.com"
  token    = var.yottabot_token
}

# Service-account client credentials. Preferred for CI and any unattended
# apply: the audit trail then names the service account rather than whichever
# human happened to run it.
provider "yottabot" {
  alias    = "service_account"
  endpoint = "https://yottabot.example.com"

  user_id         = var.yottabot_service_account_user_id
  kid             = var.yottabot_service_account_kid
  private_key_pem = var.yottabot_service_account_private_key_pem
}
