variable "region" {
  type        = string
  description = "AWS region for the single tunnel host."
}

variable "domain" {
  type        = string
  description = "Domain tunnels are served beneath, such as tunnel.example.com. Agents and operators use https://<domain>:8443; visitors reach https://<subdomain>.<domain>."
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
  default     = "platform"
  description = "Principal recorded on management audit rows."
}

variable "control_cidrs" {
  type        = list(string)
  default     = []
  description = "Your machines: sources allowed to reach the control port 8443, which is where agents attach and where the management and read APIs live. Empty means anywhere. Register an office range, a VPN range, or each developer's address."
}

variable "edge_cidrs" {
  type        = list(string)
  default     = []
  description = "Sources allowed to reach the tunnels themselves on 443. Empty means the internet, which is what a webhook provider or a demo needs; narrow it to your own address while testing."
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

variable "state_volume_gib" {
  type        = number
  default     = 1
  description = "Size of the separate encrypted volume that holds the claims database. It outlives the instance."
}

variable "caddy_version" {
  type        = string
  default     = "v2.11.4"
  description = "Caddy release built into the TLS edge at apply time."
}

variable "caddy_route53_version" {
  type        = string
  default     = "v1.6.2"
  description = "caddy-dns/route53 module release built into Caddy for DNS-01 challenges."
}

variable "xcaddy_version" {
  type        = string
  default     = "v0.4.7"
  description = "xcaddy release used to build Caddy with the Route 53 module."
}
