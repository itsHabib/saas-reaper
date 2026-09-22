output "control_url" { value = "https://${local.control_host}:8443" }
output "edge_pattern" { value = "https://<claim>.${var.domain}" }
output "public_ip" { value = try(google_compute_address.tunnel[0].address, null) }
output "admin_token" {
  value     = random_password.token["admin"].result
  sensitive = true
}
output "read_token" {
  value     = random_password.token["read"].result
  sensitive = true
}
output "startup_log_command" {
  value = "gcloud compute instances get-serial-port-output ${var.name} --project=${var.project_id} --zone=${var.zone}"
}

output "ipv6_address" { value = try(google_compute_address.ipv6[0].address, null) }
output "cloudflare_dns_records" {
  value = local.ipv6 ? [for host in local.public_hosts : { name = host, type = "AAAA", content = google_compute_address.ipv6[0].address, proxied = true }] : []
}
output "cloudflare_origin_header_value" {
  value     = try(random_password.token["origin"].result, null)
  sensitive = true
}
output "cloudflare_origin_rule_expression" {
  value = local.ipv6 ? "http.host in {${join(" ", [for host in local.public_hosts : format("\"%s\"", host)])}}" : null
}
