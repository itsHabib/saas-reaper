mock_provider "google" {
  mock_data "google_dns_managed_zone" {
    defaults = { dns_name = "example.com.", name = "example", visibility = "public" }
  }
  mock_resource "google_service_account" {
    defaults = { email = "reaper-tunnel@test-project.iam.gserviceaccount.com" }
  }
}
mock_provider "random" {}
variables {
  project_id    = "test-project"
  domain        = "tunnel.example.com"
  dns_zone      = "example"
  acme_email    = "ops@example.com"
  control_cidrs = ["203.0.113.1/32"]
}
run "retained_disk" {
  command = apply
  plan_options { target = [google_compute_disk.state] }
}
run "safe_defaults" {
  command = plan
  assert {
    condition     = google_compute_instance.tunnel.machine_type == "e2-micro" && google_compute_disk.state.type == "pd-standard"
    error_message = "The default host must use the small VM and standard disk."
  }
  assert {
    condition     = tolist(google_compute_firewall.control.allow)[0].ports == tolist(["8443"]) && tolist(google_compute_firewall.edge.allow)[0].ports == tolist(["443"])
    error_message = "Only the control and edge ports may be exposed."
  }
  assert {
    condition     = google_storage_bucket.artifacts.public_access_prevention == "enforced"
    error_message = "Artifacts must remain private."
  }
}
run "reject_wrong_domain" {
  command = plan
  variables { domain = "tunnel.attacker.com" }
  expect_failures = [google_dns_managed_zone.tunnel]
}
run "reject_cross_zone" {
  command = plan
  variables { zone = "us-central1-b" }
  expect_failures = [google_compute_instance.tunnel]
}
run "reject_empty_control" {
  command = plan
  variables { control_cidrs = [] }
  expect_failures = [var.control_cidrs]
}

run "reject_private_zone" {
  command = plan
  override_data {
    target = data.google_dns_managed_zone.parent[0]
    values = { dns_name = "example.com.", name = "example", visibility = "private" }
  }
  expect_failures = [google_dns_managed_zone.tunnel]
}

run "parent_zone_is_not_granted_to_host" {
  command = plan
  assert {
    condition     = google_dns_managed_zone_iam_member.dns[0].managed_zone == google_dns_managed_zone.tunnel[0].name && google_dns_managed_zone.tunnel[0].dns_name == "${var.domain}." && google_dns_managed_zone_iam_member.dns[0].managed_zone != data.google_dns_managed_zone.parent[0].name
    error_message = "The host must have DNS authority only on its exact dedicated tunnel zone."
  }
  assert {
    condition     = google_dns_record_set.delegation[0].managed_zone == data.google_dns_managed_zone.parent[0].name && google_dns_record_set.delegation[0].type == "NS"
    error_message = "Terraform must delegate the dedicated zone in the parent."
  }
}
run "reject_parent_apex" {
  command = plan
  variables { domain = "example.com" }
  expect_failures = [google_dns_managed_zone.tunnel]
}

run "ipv6_has_no_public_ipv4_or_dns_authority" {
  command = plan
  variables {
    network_mode = "cloudflare_ipv6"
    domain       = "example.com"
    claim_names  = ["probe", "slack"]
  }
  assert {
    condition     = length(google_compute_address.tunnel) == 0 && length(google_compute_instance.tunnel.network_interface[0].access_config) == 0 && length(google_compute_address.ipv6) == 1
    error_message = "IPv6 mode must not allocate or attach public IPv4 and must reserve IPv6."
  }
  assert {
    condition     = google_compute_address.ipv6[0].ip_version == "IPV6" && google_compute_address.ipv6[0].ipv6_endpoint_type == "VM" && google_compute_subnetwork.tunnel.private_ip_google_access
    error_message = "Use a static VM IPv6 reservation and Private Google Access."
  }
  assert {
    condition     = length(google_dns_managed_zone.tunnel) == 0 && length(google_dns_managed_zone_iam_member.dns) == 0 && length(google_project_iam_member.zone_discovery) == 0 && length(google_dns_record_set.tunnel) == 0
    error_message = "Cloudflare mode must not mutate or grant Google DNS zones."
  }
  assert {
    condition     = google_compute_firewall.control.source_ranges == toset(local.cloudflare_ipv6_cidrs) && google_compute_firewall.edge.source_ranges == toset(local.cloudflare_ipv6_cidrs)
    error_message = "Origin ingress must be restricted to Cloudflare IPv6 addresses."
  }
  assert {
    condition     = length(random_password.token) == 3 && local.control_host == "control.example.com"
    error_message = "The origin secret must be separate from management and read credentials."
  }
}
run "ipv6_retains_detached_ipv4_for_migration" {
  command = plan
  variables {
    network_mode            = "cloudflare_ipv6"
    retain_ipv4_reservation = true
    domain                  = "example.com"
    claim_names             = ["probe"]
  }
  assert {
    condition     = length(google_compute_address.tunnel) == 1 && length(google_compute_instance.tunnel.network_interface[0].access_config) == 0
    error_message = "Migration can retain the billable IPv4 reservation without attaching it."
  }
}
run "reject_missing_ipv6_claims" {
  command = plan
  variables { network_mode = "cloudflare_ipv6" }
  expect_failures = [google_compute_instance.tunnel]
}
run "reject_control_claim_collision" {
  command = plan
  variables {
    network_mode = "cloudflare_ipv6"
    claim_names  = ["console"]
    control_name = "console"
  }
  expect_failures = [google_compute_instance.tunnel]
}
run "reject_nested_claim" {
  command = plan
  variables { claim_names = ["nested.claim"] }
  expect_failures = [var.claim_names]
}

run "reject_reserved_claim" {
  command = plan
  variables { claim_names = ["admin"] }
  expect_failures = [var.claim_names]
}

run "ipv6_visitor_defaults" {
  command = plan
  variables {
    network_mode  = "cloudflare_ipv6"
    claim_names   = ["probe"]
    control_cidrs = ["2001:db8::7/128"]
  }
  assert {
    condition     = contains(local.effective_edge_cidrs, "::/0") && contains(local.effective_edge_cidrs, "0.0.0.0/0")
    error_message = "The Cloudflare public edge must default to both visitor address families."
  }
}
run "direct_mode_rejects_ipv6_cidr" {
  command = plan
  variables { control_cidrs = ["2001:db8::7/128"] }
  expect_failures = [var.control_cidrs]
}
