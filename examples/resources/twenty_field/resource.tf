data "twenty_object" "company" {
  name_singular = "company"
}

resource "twenty_field" "customer_tier" {
  object_id   = data.twenty_object.company.id
  name        = "customerTier"
  type        = "TEXT"
  label       = "Customer tier"
  description = "Customer support tier"

  # Apply false before destroying. Deletion permanently removes field data.
  deletion_protection = true
}
