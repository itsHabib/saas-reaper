output "control_url" { value = "https://${var.domain}:8443" }
output "edge_pattern" { value = "https://<claim>.${var.domain}" }
output "public_ip" { value = google_compute_address.tunnel.address }
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
