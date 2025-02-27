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

// TODO: remove this static token once I have permissions to create and test a
// role binding
resource "azurerm_container_registry_token" "image_copy" {
  name                    = "${var.name}-token"
  container_registry_name = azurerm_container_registry.image_copy.name
  resource_group_name     = azurerm_resource_group.image_copy.name
  scope_map_id            = "${azurerm_container_registry.image_copy.id}/scopeMaps/_repositories_push"
  enabled                 = true
}

// TODO: remove this static password once I have permissions to create and test
// a role binding
resource "azurerm_container_registry_token_password" "image_copy" {
  container_registry_token_id = azurerm_container_registry_token.image_copy.id

  password1 {}
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

// TODO: get permissions to test this
//resource "azurerm_role_assignment" "image_copy_acr_push" {
//  scope                = azurerm_container_registry.image_copy.id
//  role_definition_name = "AcrPush"
//  principal_id         = azurerm_user_assigned_identity.image_copy.id
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
    // TODO: remove this user/pass once I have permissions to create a role
    // binding
    REGISTRY_USERNAME = azurerm_container_registry_token.image_copy.name
    REGISTRY_PASSWORD = azurerm_container_registry_token_password.image_copy.password1[0].value
  }
}
