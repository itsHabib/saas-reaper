locals {
  effective_edge_cidrs  = var.edge_cidrs == null ? (local.ipv6 ? ["0.0.0.0/0", "::/0"] : ["0.0.0.0/0"]) : var.edge_cidrs
  ipv6                  = var.network_mode == "cloudflare_ipv6"
  cloudflare_ipv6_cidrs = ["2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32"]
  control_host          = local.ipv6 ? "${var.control_name}.${var.domain}" : var.domain
  public_hosts          = concat([local.control_host], [for name in sort(tolist(var.claim_names)) : "${name}.${var.domain}"])
  specimen              = abspath("${path.module}/../..")
  build                 = abspath("${path.module}/.build")
  sources               = [for file in sort(fileset(local.specimen, "{cmd,internal}/**/*.go")) : file if !endswith(file, "_test.go")]
  server_digest = sha256(join("", concat(
    [for file in local.sources : filesha256("${local.specimen}/${file}")],
    [filesha256("${local.specimen}/go.mod"), filesha256("${local.specimen}/go.sum"), "linux-amd64"],
  )))
  caddy_version  = "v2.11.4"
  dns_version    = "v1.1.0"
  xcaddy_version = "v0.4.7"
  caddy_digest   = sha256("${local.caddy_version}|${local.dns_version}|${local.xcaddy_version}|linux-amd64")
  server_path    = "${local.build}/server-${local.server_digest}"
  caddy_path     = "${local.build}/caddy-${local.caddy_digest}"
}

resource "google_project_service" "required" {
  for_each           = toset(["compute.googleapis.com", "dns.googleapis.com", "iam.googleapis.com", "secretmanager.googleapis.com", "storage.googleapis.com"])
  service            = each.value
  disable_on_destroy = false
}

data "google_dns_managed_zone" "parent" {
  count      = local.ipv6 ? 0 : 1
  name       = var.dns_zone
  depends_on = [google_project_service.required]
}

# Only the operator writes the parent delegation; host authority stops at the child zone.
resource "google_dns_managed_zone" "tunnel" {
  count      = local.ipv6 ? 0 : 1
  name       = "${var.name}-dns"
  dns_name   = "${var.domain}."
  visibility = "public"
  depends_on = [google_project_service.required]
  lifecycle {
    precondition {
      condition     = data.google_dns_managed_zone.parent[0].visibility == "public" && endswith("${var.domain}.", ".${data.google_dns_managed_zone.parent[0].dns_name}")
      error_message = "The tunnel domain must be a subdomain of the existing public parent zone. This pack creates and delegates a dedicated zone for it."
    }
  }
}
resource "google_dns_record_set" "delegation" {
  count        = local.ipv6 ? 0 : 1
  name         = google_dns_managed_zone.tunnel[0].dns_name
  managed_zone = data.google_dns_managed_zone.parent[0].name
  type         = "NS"
  ttl          = 300
  rrdatas      = google_dns_managed_zone.tunnel[0].name_servers
}

resource "terraform_data" "server_binary" {
  triggers_replace = [local.server_digest, fileexists(local.server_path)]
  provisioner "local-exec" {
    working_dir = local.specimen
    environment = { REAPER_BUILD = local.build, REAPER_BINARY = local.server_path }
    command     = "mkdir -p \"$REAPER_BUILD\" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o \"$REAPER_BINARY\" ./cmd/reaper-tunnel"
  }
}
resource "terraform_data" "caddy_binary" {
  triggers_replace = [local.caddy_digest, fileexists(local.caddy_path)]
  provisioner "local-exec" {
    working_dir = local.specimen
    environment = {
      REAPER_BUILD  = local.build, REAPER_BINARY = local.caddy_path,
      CADDY_VERSION = local.caddy_version, DNS_VERSION = local.dns_version, XCADDY_VERSION = local.xcaddy_version
    }
    command = "mkdir -p \"$REAPER_BUILD/tools\" && GOBIN=\"$REAPER_BUILD/tools\" go install github.com/caddyserver/xcaddy/cmd/xcaddy@\"$XCADDY_VERSION\" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \"$REAPER_BUILD/tools/xcaddy\" build \"$CADDY_VERSION\" --with github.com/caddy-dns/googleclouddns@\"$DNS_VERSION\" --output \"$REAPER_BINARY\""
  }
}
resource "random_id" "bucket" { byte_length = 4 }
resource "google_storage_bucket" "artifacts" {
  name                        = "${var.project_id}-${var.name}-${random_id.bucket.hex}"
  location                    = var.region
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  depends_on                  = [google_project_service.required]
}
resource "google_storage_bucket_object" "server" {
  name       = "server-${local.server_digest}"
  bucket     = google_storage_bucket.artifacts.name
  source     = local.server_path
  depends_on = [terraform_data.server_binary]
}
resource "google_storage_bucket_object" "caddy" {
  name       = "caddy-${local.caddy_digest}"
  bucket     = google_storage_bucket.artifacts.name
  source     = local.caddy_path
  depends_on = [terraform_data.caddy_binary]
}
resource "google_service_account" "host" {
  account_id = var.name
  depends_on = [google_project_service.required]
}
resource "google_storage_bucket_iam_member" "artifacts" {
  bucket = google_storage_bucket.artifacts.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.host.email}"
}
# Zone discovery is project-wide read-only; mutation authority is restricted to the chosen zone.
resource "google_project_iam_custom_role" "zone_discovery" {
  count       = local.ipv6 ? 0 : 1
  role_id     = "${replace(var.name, "-", "_")}_zone_discovery"
  title       = "Tunnel DNS zone discovery"
  permissions = ["dns.managedZones.list"]
}
resource "google_project_iam_member" "zone_discovery" {
  count   = local.ipv6 ? 0 : 1
  project = var.project_id
  role    = google_project_iam_custom_role.zone_discovery[0].name
  member  = "serviceAccount:${google_service_account.host.email}"
}
resource "google_dns_managed_zone_iam_member" "dns" {
  count        = local.ipv6 ? 0 : 1
  managed_zone = google_dns_managed_zone.tunnel[0].name
  role         = "roles/dns.admin"
  member       = "serviceAccount:${google_service_account.host.email}"
}
resource "random_password" "token" {
  for_each = toset(local.ipv6 ? ["admin", "read", "origin"] : ["admin", "read"])
  length   = 48
  special  = false
}
resource "google_secret_manager_secret" "token" {
  for_each  = random_password.token
  secret_id = "${var.name}-${each.key}"
  replication {
    auto {}
  }
  depends_on = [google_project_service.required]
}
resource "google_secret_manager_secret_version" "token" {
  for_each    = random_password.token
  secret      = google_secret_manager_secret.token[each.key].id
  secret_data = each.value.result
}
resource "google_secret_manager_secret_iam_member" "token" {
  for_each  = random_password.token
  secret_id = google_secret_manager_secret.token[each.key].id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.host.email}"
}
resource "google_compute_network" "tunnel" {
  name                    = var.name
  auto_create_subnetworks = false
  depends_on              = [google_project_service.required]
}
resource "google_compute_subnetwork" "tunnel" {
  name                     = var.name
  ip_cidr_range            = "10.79.0.0/24"
  region                   = var.region
  network                  = google_compute_network.tunnel.id
  stack_type               = local.ipv6 ? "IPV4_IPV6" : "IPV4_ONLY"
  ipv6_access_type         = local.ipv6 ? "EXTERNAL" : null
  private_ip_google_access = local.ipv6
}
resource "google_compute_firewall" "control" {
  name                    = "${var.name}-control"
  network                 = google_compute_network.tunnel.id
  source_ranges           = local.ipv6 ? local.cloudflare_ipv6_cidrs : var.control_cidrs
  target_service_accounts = [google_service_account.host.email]
  allow {
    protocol = "tcp"
    ports    = ["8443"]
  }
}
resource "google_compute_firewall" "edge" {
  name                    = "${var.name}-edge"
  network                 = google_compute_network.tunnel.id
  source_ranges           = local.ipv6 ? local.cloudflare_ipv6_cidrs : local.effective_edge_cidrs
  target_service_accounts = [google_service_account.host.email]
  allow {
    protocol = "tcp"
    ports    = local.ipv6 ? ["80", "443"] : ["443"]
  }
}
resource "google_compute_address" "tunnel" {
  count      = !local.ipv6 || var.retain_ipv4_reservation ? 1 : 0
  name       = var.name
  region     = var.region
  depends_on = [google_project_service.required]
}
resource "google_compute_disk" "state" {
  name = "${var.name}-state"
  zone = var.zone
  type = "pd-standard"
  size = var.state_disk_gib
  lifecycle {
    prevent_destroy = true
    ignore_changes  = [zone]
  }
  depends_on = [google_project_service.required]
}
resource "google_compute_instance" "tunnel" {
  name         = var.name
  zone         = var.zone
  machine_type = var.machine_type
  boot_disk {
    initialize_params {
      image = "debian-cloud/debian-12"
      size  = 10
      type  = "pd-standard"
    }
  }
  attached_disk {
    source      = google_compute_disk.state.id
    device_name = "reaper-state"
  }
  network_interface {
    subnetwork = google_compute_subnetwork.tunnel.id
    stack_type = local.ipv6 ? "IPV4_IPV6" : "IPV4_ONLY"
    dynamic "access_config" {
      for_each = local.ipv6 ? [] : [1]
      content { nat_ip = google_compute_address.tunnel[0].address }
    }
    dynamic "ipv6_access_config" {
      for_each = local.ipv6 ? [1] : []
      content {
        network_tier                = "PREMIUM"
        external_ipv6               = google_compute_address.ipv6[0].address
        external_ipv6_prefix_length = google_compute_address.ipv6[0].prefix_length
      }
    }
  }
  service_account {
    email  = google_service_account.host.email
    scopes = ["cloud-platform"]
  }
  metadata = { block-project-ssh-keys = "true", enable-oslogin = "TRUE" }
  metadata_startup_script = templatefile("${path.module}/startup.sh", {
    project       = var.project_id
    domain        = var.domain
    acme_email    = var.acme_email
    admin_actor   = var.admin_actor
    bucket        = google_storage_bucket.artifacts.name
    server_key    = google_storage_bucket_object.server.name
    caddy_key     = google_storage_bucket_object.caddy.name
    admin_secret  = google_secret_manager_secret.token["admin"].secret_id
    read_secret   = google_secret_manager_secret.token["read"].secret_id
    ipv6          = local.ipv6
    origin_secret = local.ipv6 ? google_secret_manager_secret.token["origin"].secret_id : ""
    caddy_config = local.ipv6 ? templatefile("${path.module}/Caddyfile-ipv6", {
      acme_email       = var.acme_email
      control_host     = local.control_host
      edge_hosts       = join(", ", [for name in sort(tolist(var.claim_names)) : "${name}.${var.domain}:443"])
      control_cidrs    = join(" ", var.control_cidrs)
      edge_cidrs       = join(" ", local.effective_edge_cidrs)
      cloudflare_cidrs = join(" ", local.cloudflare_ipv6_cidrs)
    }) : ""
  })
  lifecycle {
    ignore_changes = [boot_disk[0].initialize_params[0].image]
    precondition {
      condition     = !local.ipv6 || (length(var.claim_names) > 0 && !contains(var.claim_names, var.control_name))
      error_message = "IPv6 mode needs explicit claim_names distinct from control_name."
    }
    precondition {
      condition     = local.ipv6 || var.dns_zone != null
      error_message = "IPv4 mode requires the existing parent dns_zone."
    }
    precondition {
      condition     = var.zone == google_compute_disk.state.zone
      error_message = "The retained disk cannot cross zones. Migrate a snapshot explicitly."
    }

  }
  depends_on = [google_storage_bucket_iam_member.artifacts, google_secret_manager_secret_iam_member.token, google_secret_manager_secret_version.token, google_dns_managed_zone_iam_member.dns, google_project_iam_member.zone_discovery, google_dns_record_set.delegation]
}
resource "google_dns_record_set" "tunnel" {
  for_each     = local.ipv6 ? toset([]) : toset(["${var.domain}.", "*.${var.domain}."])
  name         = each.value
  managed_zone = google_dns_managed_zone.tunnel[0].name
  type         = "A"
  ttl          = 60
  rrdatas      = [google_compute_address.tunnel[0].address]
}

resource "google_compute_address" "ipv6" {
  count              = local.ipv6 ? 1 : 0
  name               = "${var.name}-ipv6"
  region             = var.region
  address_type       = "EXTERNAL"
  ip_version         = "IPV6"
  network_tier       = "PREMIUM"
  ipv6_endpoint_type = "VM"
  subnetwork         = google_compute_subnetwork.tunnel.id
}

moved {
  from = google_dns_managed_zone.tunnel
  to   = google_dns_managed_zone.tunnel[0]
}

moved {
  from = google_dns_record_set.delegation
  to   = google_dns_record_set.delegation[0]
}

moved {
  from = google_project_iam_custom_role.zone_discovery
  to   = google_project_iam_custom_role.zone_discovery[0]
}

moved {
  from = google_project_iam_member.zone_discovery
  to   = google_project_iam_member.zone_discovery[0]
}

moved {
  from = google_dns_managed_zone_iam_member.dns
  to   = google_dns_managed_zone_iam_member.dns[0]
}

moved {
  from = google_compute_address.tunnel
  to   = google_compute_address.tunnel[0]
}
