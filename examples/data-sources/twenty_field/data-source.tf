data "twenty_object" "company" {
  name_singular = "company"
}

data "twenty_field" "company_name" {
  object_id = data.twenty_object.company.id
  name      = "name"
}

# Alternatively, specify field_id alone with the native field UUID.
