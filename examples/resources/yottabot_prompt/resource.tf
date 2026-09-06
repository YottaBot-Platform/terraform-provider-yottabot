resource "yottabot_prompt" "summariser" {
  name        = "incident-summariser"
  description = "Condenses an incident timeline for a status page"

  # Versions are immutable once published. Change `body` or `variables` and you
  # must bump `version` in the same apply — re-publishing an existing version is
  # refused, which is what stops an edit silently changing what agents already
  # pinned to that version execute.
  version   = "1.0.0"
  body      = "Summarise the following incident for a public status page:\n\n{{timeline}}"
  variables = ["timeline"]
}
