# Importing is also the recovery path after a destroy. `terraform destroy`
# RETIRES a Context provider rather than deleting it: the row stays, still
# holding its unique (account, type, external_id) key, so a later apply with
# the same type + external_id is refused. Adopt the existing row instead, then
# set state = "active" in config and apply.
terraform import yottabot_context_provider.github_org 7a2f4b88-91c3-4e5d-b06a-2f8e1c9d3b45
