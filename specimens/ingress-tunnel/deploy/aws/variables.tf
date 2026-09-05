variable "region" {
  type        = string
  description = "AWS region for the single tunnel host."
}

variable "domain" {
  type        = string
  description = "Domain tunnels are served beneath, such as tunnel.example.com. Agents connect to https://<domain>; visitors reach https://<subdomain>.<domain>."
}

variable "hosted_zone_id" {
  type        = string
  description = "Route 53 hosted zone that contains the domain. The pack writes the apex and wildcard records and lets Caddy answer DNS-01 challenges in it."
}

variable "acme_email" {
  type        = string
  description = "Contact address Caddy registers with the certificate authority."
}

variable "admin_actor" {
  type        = string
  description = "Principal recorded on management audit rows."
}

variable "admin_token_secret_arn" {
  type        = string
  description = "Secrets Manager ARN holding the management token."
}

variable "read_token_secret_arn" {
  type        = string
  description = "Secrets Manager ARN holding the read token."
}

variable "name" {
  type        = string
  default     = "reaper-tunnel"
  description = "Prefix for every resource the pack creates."
}

variable "instance_type" {
  type        = string
  default     = "t4g.nano"
  description = "A single small instance is enough: the tunnel is a multiplexer, not a compute job."
}

variable "architecture" {
  type        = string
  default     = "arm64"
  description = "CPU architecture of the instance type; also selects the AMI and the cross-compile target."
  validation {
    condition     = contains(["arm64", "x86_64"], var.architecture)
    error_message = "architecture must be arm64 or x86_64."
  }
}

variable "vpc_id" {
  type        = string
  default     = null
  description = "VPC for the host. Defaults to the account's default VPC."
}

variable "subnet_id" {
  type        = string
  default     = null
  description = "Public subnet for the host. Defaults to the first subnet of the chosen VPC."
}

variable "allowed_cidrs" {
  type        = list(string)
  default     = ["0.0.0.0/0"]
  description = "Sources allowed to reach ports 80 and 443. Agents and visitors both arrive here, so narrowing this narrows who can use the tunnels."
}
