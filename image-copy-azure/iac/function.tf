provider "azurerm" {
  subscription_id = var.subscription_id
  features {}
}

resource "azurerm_resource_group" "image_copy" {
  name     = var.name
  location = var.location
}

resource "azurerm_storage_account" "image_copy" {
  name                     = var.name
  resource_group_name      = azurerm_resource_group.image_copy.name
  location                 = azurerm_resource_group.image_copy.location
  account_tier             = "Standard"
  account_replication_type = "LRS"
}

locals {
  // Construct the name of the zip file by hashing the files we're including in
  // the archive. That means the name of the file will change when the contents
  // do, causing the function to reload the code.
  zip_file_hash = sha256(join("#", [
    filesha256("${path.module}/function/image-copy-azure"),
    filesha256("${path.module}/function/host.json"),
    filesha256("${path.module}/function/imagecopy/function.json"),
  ]))
  zip_output_path = "${path.module}/function-${local.zip_file_hash}.zip"
}

resource "archive_file" "image_copy" {
  type        = "zip"
  source_dir  = "${path.module}/function"
  output_path = local.zip_output_path
}

resource "azurerm_container_registry" "image_copy" {
  name                = var.name
  resource_group_name = azurerm_resource_group.image_copy.name
  location            = azurerm_resource_group.image_copy.location
  sku                 = "Basic"
  admin_enabled       = false
}

resource "azurerm_service_plan" "image_copy" {
  name                = var.name
  location            = azurerm_resource_group.image_copy.location
  resource_group_name = azurerm_resource_group.image_copy.name
  os_type             = "Linux"
  sku_name            = "Y1"
}

resource "azurerm_user_assigned_identity" "image_copy" {
  name                = var.name
  resource_group_name = azurerm_resource_group.image_copy.name
  location            = azurerm_resource_group.image_copy.location
}

// TODO: I need permissions to test this in our Azure account
//resource "azurerm_role_assignment" "acr_push_role" {
//  scope                = azurerm_container_registry.acr.id
//  role_definition_name = "AcrPush"
//  principal_id         = azurerm_user_assigned_identity.identity.id
//}

resource "azurerm_application_insights" "image_copy" {
  name                = var.name
  location            = azurerm_resource_group.image_copy.location
  resource_group_name = azurerm_resource_group.image_copy.name
  application_type    = "web"
}

resource "azurerm_linux_function_app" "image_copy" {
  name                       = var.name
  location                   = azurerm_resource_group.image_copy.location
  resource_group_name        = azurerm_resource_group.image_copy.name
  service_plan_id            = azurerm_service_plan.image_copy.id
  storage_account_name       = azurerm_storage_account.image_copy.name
  storage_account_access_key = azurerm_storage_account.image_copy.primary_access_key
  zip_deploy_file            = archive_file.image_copy.output_path

  site_config {
    application_insights_connection_string = azurerm_application_insights.image_copy.connection_string
    application_stack {
      use_custom_runtime = true
    }
  }

  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.image_copy.id]
  }

  app_settings = {
    WEBSITE_RUN_FROM_PACKAGE = "1"
    API_ENDPOINT             = "https://console-api.enforce.dev"
    ISSUER_URL               = "https://issuer.enforce.dev"
    GROUP_NAME               = var.group_name
    GROUP                    = data.chainguard_group.group.id
    IDENTITY                 = chainguard_identity.azure.id
    DST_REPO                 = azurerm_container_registry.image_copy.login_server
    AZURE_CLIENT_ID          = azurerm_user_assigned_identity.image_copy.client_id
  }
}
