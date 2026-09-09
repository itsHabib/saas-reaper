variable "project_id" {
  type        = string
  description = "Existing billing-enabled GCP project. Use a dedicated tunnel project."
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.project_id))
    error_message = "Use a GCP project ID, not a display name or project number."
  }
}
variable "domain" {
  type        = string
  description = "Tunnel subdomain beneath the existing parent zone; the pack creates its own dedicated zone."
  validation {
    condition     = length(var.domain) <= 253 && can(regex("^([a-z0-9]([a-z0-9-]*[a-z0-9])?\\.)+[a-z]{2,63}$", var.domain))
    error_message = "Use a lowercase DNS name without a trailing dot."
  }
}
variable "dns_zone" {
  type        = string
  description = "Existing publicly delegated parent Cloud DNS zone in project_id. Terraform writes a child NS delegation; the host gets no authority over this parent."
}
variable "acme_email" {
  type        = string
  description = "Certificate renewal contact."
  validation {
    condition     = can(regex("^[a-zA-Z0-9._+%-]+@[a-zA-Z0-9.-]+$", var.acme_email))
    error_message = "Use a plain email address."
  }
}
variable "control_cidrs" {
  type        = list(string)
  description = "Required IPv4 sources permitted to attach agents and call management on 8443."
  validation {
    condition     = length(var.control_cidrs) > 0 && alltrue([for cidr in var.control_cidrs : can(cidrnetmask(cidr))])
    error_message = "Supply at least one IPv4 CIDR; use your public IP with /32."
  }
}
variable "edge_cidrs" {
  type        = list(string)
  default     = ["0.0.0.0/0"]
  description = "Visitor sources on 443; public by default so Slack can reach callbacks."
  validation {
    condition     = length(var.edge_cidrs) > 0 && alltrue([for cidr in var.edge_cidrs : can(cidrnetmask(cidr))])
    error_message = "Supply at least one IPv4 CIDR."
  }
}
variable "region" {
  type    = string
  default = "us-central1"
}
variable "zone" {
  type    = string
  default = "us-central1-a"
}
variable "name" {
  type    = string
  default = "reaper-tunnel"
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,23}[a-z0-9]$", var.name))
    error_message = "Use 6-25 lowercase letters, digits or hyphens, starting with a letter."
  }
}
variable "machine_type" {
  type        = string
  default     = "e2-micro"
  description = "x86 machine type. e2-micro may qualify for the account-wide free tier."
  validation {
    condition     = can(regex("^e2-", var.machine_type))
    error_message = "This pack builds amd64 binaries and supports E2 machines."
  }
}
variable "admin_actor" {
  type    = string
  default = "platform"
  validation {
    condition     = can(regex("^[a-zA-Z0-9@._-]+$", var.admin_actor))
    error_message = "Use a simple principal identifier."
  }
}
variable "state_disk_gib" {
  type    = number
  default = 10
}
