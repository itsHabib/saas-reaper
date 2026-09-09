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
    target = data.google_dns_managed_zone.parent
    values = { dns_name = "example.com.", name = "example", visibility = "private" }
  }
  expect_failures = [google_dns_managed_zone.tunnel]
}

run "parent_zone_is_not_granted_to_host" {
  command = plan
  assert {
    condition     = google_dns_managed_zone_iam_member.dns.managed_zone == google_dns_managed_zone.tunnel.name && google_dns_managed_zone.tunnel.dns_name == "${var.domain}." && google_dns_managed_zone_iam_member.dns.managed_zone != data.google_dns_managed_zone.parent.name
    error_message = "The host must have DNS authority only on its exact dedicated tunnel zone."
  }
  assert {
    condition     = google_dns_record_set.delegation.managed_zone == data.google_dns_managed_zone.parent.name && google_dns_record_set.delegation.type == "NS"
    error_message = "Terraform must delegate the dedicated zone in the parent."
  }
}
run "reject_parent_apex" {
  command = plan
  variables { domain = "example.com" }
  expect_failures = [google_dns_managed_zone.tunnel]
}
