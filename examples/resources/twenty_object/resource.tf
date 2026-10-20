resource "twenty_object" "subscription" {
  name_singular  = "subscription"
  name_plural    = "subscriptions"
  label_singular = "Subscription"
  label_plural   = "Subscriptions"
  description    = "Customer subscriptions"
  icon           = "IconRepeat"

  # Apply false before destroying. Destruction drops all fields and records.
  deletion_protection = true
}
