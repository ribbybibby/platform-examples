variable "name" {
  type        = string
  description = "A name for the function and all the resources created by this module."
}

variable "group_name" {
  type        = string
  description = "The name of the Chainguard group that we are subscribing to. For instance: 'your.org.com'."
}

variable "subscription_id" {
  type        = string
  description = "The Azure subscription to create resources under"
}
variable "location" {
  type        = string
  description = "The location to create resources in"
  default     = "westus"
}
