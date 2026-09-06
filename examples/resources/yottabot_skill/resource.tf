resource "yottabot_skill" "node_pressure" {
  slug   = "node-memory-pressure"
  title  = "Diagnose node memory pressure"
  domain = "k8s"

  # Defaults to `private`. `yotta_managed` is not settable — it marks the
  # Yotta-managed skill library, which this resource can read but never write.
  visibility = "customer_visible"
  status     = "active"
}

# Always `customer` for a skill created here.
output "node_pressure_source" {
  value = yottabot_skill.node_pressure.source_kind
}
