output "public_ip" {
  value       = aws_eip.tunnel.public_ip
  description = "Elastic IP the apex and wildcard records point at."
}

output "control_url" {
  value       = "https://${var.domain}:8443"
  description = "Where agents connect and where the management and read APIs live."
}

output "edge_pattern" {
  value       = "https://<subdomain>.${var.domain}"
  description = "Where visitors reach a claimed tunnel."
}

output "admin_token" {
  value       = random_password.admin_token.result
  sensitive   = true
  description = "Management token minted by this apply. Read it with `terraform output -raw admin_token`."
}

output "read_token" {
  value       = random_password.read_token.result
  sensitive   = true
  description = "Read token minted by this apply. Read it with `terraform output -raw read_token`."
}

output "claim_example" {
  value       = "curl -X POST https://${var.domain}:8443/v1/tunnels -H \"Authorization: Bearer $(terraform output -raw admin_token)\" -H 'Content-Type: application/json' -d '{\"subdomain\":\"acme\"}'"
  description = "Issue a credential for one subdomain; the token in the response is shown once."
}

output "agent_example" {
  value       = "REAPER_TUNNEL_AGENT_SERVER=https://${var.domain}:8443 REAPER_TUNNEL_AGENT_TOKEN=rtk_... REAPER_TUNNEL_AGENT_TARGET=http://127.0.0.1:3000 reaper-tunnel-agent"
  description = "Run on the developer's machine to expose a local port at https://acme.<domain>."
}
