# BitVoice configuration as code with Terraform or OpenTofu, through BitVoice's REST API
# (described at https://<your BitVoice>/api/openapi.json).
#
# It uses the generic REST provider Mastercard/restapi (Apache-2.0). A provider of BitVoice's own would
# need HashiCorp's plugin SDK, which is MPL-licensed; BitVoice keeps to MIT, BSD, ISC and Apache-2.0.
#
#   export TF_VAR_bitvoice_token=bv_...   # Administrators, API tokens
#   terraform init && terraform apply

terraform {
  required_providers {
    restapi = {
      source  = "Mastercard/restapi"
      version = "1.20.0"
    }
  }
}

variable "bitvoice_url" {
  type    = string
  default = "https://pbx.example.com:8443"
}

variable "bitvoice_token" {
  type      = string
  sensitive = true
}

provider "restapi" {
  uri                  = var.bitvoice_url
  write_returns_object = true
  # Answers wrap the object: {"item": {...}, "rev": 12}.
  id_attribute = "item/id"
  headers = {
    Authorization = "Bearer ${var.bitvoice_token}"
    Content-Type  = "application/json"
  }
}

resource "restapi_object" "front_desk" {
  path = "/api/extensions"
  data = jsonencode({
    number      = "100"
    name        = "Front desk"
    enabled     = true
    outbound    = true
    ringSeconds = 25
    location    = "Main office, floor 1"
    voicemail   = { enabled = true, maxSeconds = 180 }
  })
}

resource "restapi_object" "sales" {
  path = "/api/ring-groups"
  data = jsonencode({
    number      = "600"
    name        = "Sales"
    members     = ["100"]
    strategy    = "all"
    ringSeconds = 20
  })
  depends_on = [restapi_object.front_desk]
}

resource "restapi_object" "main_number" {
  path = "/api/inbound-routes"
  data = jsonencode({
    name        = "Main number"
    did         = "+12125550100"
    destination = { type = "ringgroup", target = "600" }
  })
  depends_on = [restapi_object.sales]
}
