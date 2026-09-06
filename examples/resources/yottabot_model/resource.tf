resource "yottabot_model" "in_house" {
  name        = "acme-summariser-v2"
  vendor      = "Acme"
  family      = "summariser"
  description = "Fine-tuned summariser for incident timelines"
  license     = "proprietary"
  pricing     = "paid"
  status      = "available"
  modalities  = ["text"]
}

# Where the model actually routes is set together with its gateway binding, not
# here — a model is bound atomically or not at all. These are read-only.
output "in_house_routes_via" {
  value = yottabot_model.in_house.hosting
}
