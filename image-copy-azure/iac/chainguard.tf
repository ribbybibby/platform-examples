data "chainguard_group" "group" {
  name = var.group_name
}

resource "chainguard_identity" "azure" {
  parent_id   = data.chainguard_group.group.id
  name        = "azure-${var.name}"
  description = "Identity for Azure Function"

  claim_match {
    issuer   = "https://sts.windows.net/${azurerm_user_assigned_identity.image_copy.tenant_id}/"
    subject  = azurerm_user_assigned_identity.image_copy.principal_id
    audience = "https://management.core.windows.net"
  }
}

data "chainguard_role" "puller" {
  name = "registry.pull"
}

resource "chainguard_rolebinding" "puller" {
  identity = chainguard_identity.azure.id
  role     = data.chainguard_role.puller.items[0].id
  group    = data.chainguard_group.group.id
}

resource "chainguard_subscription" "subscription" {
  parent_id = data.chainguard_group.group.id
  sink      = "https://${azurerm_linux_function_app.image_copy.default_hostname}/api/imagecopy"
}
