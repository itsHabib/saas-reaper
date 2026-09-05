data "aws_caller_identity" "current" {}

data "aws_vpc" "default" {
  count   = var.vpc_id == null ? 1 : 0
  default = true
}

locals {
  vpc_id   = var.vpc_id == null ? data.aws_vpc.default[0].id : var.vpc_id
  go_arch  = var.architecture == "x86_64" ? "amd64" : "arm64"
  specimen = "${path.module}/../.."
  build    = "${path.module}/.build"

  # Only shipped source moves the server binary: tests never reach the instance.
  server_sources = [
    for file in sort(fileset(local.specimen, "{cmd,internal}/**/*.go")) : file
    if !endswith(file, "_test.go")
  ]
  server_digest = sha256(join("", concat(
    [for file in local.server_sources : filesha256("${local.specimen}/${file}")],
    [filesha256("${local.specimen}/go.mod"), filesha256("${local.specimen}/go.sum"), local.go_arch],
  )))
  caddy_digest = sha256("${var.caddy_version}|${var.caddy_route53_version}|${var.xcaddy_version}|${local.go_arch}")

  server_key    = "reaper-tunnel-${local.go_arch}"
  caddy_key     = "caddy-${local.go_arch}"
  server_binary = "${local.build}/${local.server_key}"
  caddy_binary  = "${local.build}/${local.caddy_key}"
  xcaddy_tool   = "${local.build}/tools/xcaddy"

  # An empty allowlist means anywhere; a supplied one becomes one rule per CIDR and nothing else.
  control_sources = length(var.control_cidrs) > 0 ? var.control_cidrs : ["0.0.0.0/0"]
  edge_sources    = length(var.edge_cidrs) > 0 ? var.edge_cidrs : ["0.0.0.0/0"]
}

data "aws_subnets" "chosen" {
  count = var.subnet_id == null ? 1 : 0
  filter {
    name   = "vpc-id"
    values = [local.vpc_id]
  }
}

locals {
  subnet_id = var.subnet_id == null ? sort(data.aws_subnets.chosen[0].ids)[0] : var.subnet_id
}

data "aws_subnet" "chosen" {
  id = local.subnet_id
}

data "aws_ami" "linux" {
  most_recent = true
  owners      = ["amazon"]
  filter {
    name   = "name"
    values = ["al2023-ami-2023.*-${var.architecture}"]
  }
}

# Both binaries are built here, on the machine running terraform, and shipped through a
# private bucket. The instance never compiles anything and never fetches from a registry or a
# vendor's download service; every byte it runs was pinned and built by the apply. A missing
# artifact is itself a trigger, so a fresh checkout or a second operator rebuilds rather than
# failing on an absent file.
resource "terraform_data" "server_binary" {
  triggers_replace = [local.server_digest, fileexists(local.server_binary)]
  provisioner "local-exec" {
    working_dir = local.specimen
    command     = "mkdir -p ${abspath(local.build)} && CGO_ENABLED=0 GOOS=linux GOARCH=${local.go_arch} go build -trimpath -o ${abspath(local.server_binary)} ./cmd/reaper-tunnel"
  }
}

# xcaddy is installed natively first and only then asked to cross-compile Caddy; putting the
# target platform on the tool's own build would produce a Linux xcaddy on a macOS operator.
resource "terraform_data" "caddy_binary" {
  triggers_replace = [local.caddy_digest, fileexists(local.caddy_binary)]
  provisioner "local-exec" {
    working_dir = local.specimen
    command     = "mkdir -p ${abspath(local.build)}/tools && GOBIN=${abspath(local.build)}/tools go install github.com/caddyserver/xcaddy/cmd/xcaddy@${var.xcaddy_version} && CGO_ENABLED=0 GOOS=linux GOARCH=${local.go_arch} ${abspath(local.xcaddy_tool)} build ${var.caddy_version} --with github.com/caddy-dns/route53@${var.caddy_route53_version} --output ${abspath(local.caddy_binary)}"
  }
}

# The host's identity-shaping inputs. Changing one of these deliberately replaces the instance
# (the state volume survives); nothing else about the boot script does.
resource "terraform_data" "host_config" {
  input = {
    domain      = var.domain
    acme_email  = var.acme_email
    admin_actor = var.admin_actor
  }
}

resource "aws_s3_bucket" "artifacts" {
  bucket        = "${var.name}-${data.aws_caller_identity.current.account_id}"
  force_destroy = true
}

resource "aws_s3_bucket_public_access_block" "artifacts" {
  bucket                  = aws_s3_bucket.artifacts.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_object" "server" {
  bucket      = aws_s3_bucket.artifacts.id
  key         = local.server_key
  source      = local.server_binary
  source_hash = local.server_digest
  depends_on  = [terraform_data.server_binary]
}

resource "aws_s3_object" "caddy" {
  bucket      = aws_s3_bucket.artifacts.id
  key         = local.caddy_key
  source      = local.caddy_binary
  source_hash = local.caddy_digest
  depends_on  = [terraform_data.caddy_binary]
}

# The two API tokens are minted by the apply and kept in Secrets Manager. The service reads
# them from Secrets Manager at every start, so rotate one with
# `terraform apply -replace=random_password.admin_token` and restart the service on the host.
resource "random_password" "admin_token" {
  length  = 48
  special = false
}

resource "random_password" "read_token" {
  length  = 48
  special = false
}

resource "aws_secretsmanager_secret" "admin_token" {
  name                    = "${var.name}-admin-token"
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "admin_token" {
  secret_id     = aws_secretsmanager_secret.admin_token.id
  secret_string = random_password.admin_token.result
}

resource "aws_secretsmanager_secret" "read_token" {
  name                    = "${var.name}-read-token"
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "read_token" {
  secret_id     = aws_secretsmanager_secret.read_token.id
  secret_string = random_password.read_token.result
}

resource "aws_iam_role" "instance" {
  name = "${var.name}-instance"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
    }]
  })
}

resource "aws_iam_role_policy" "instance" {
  role = aws_iam_role.instance.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "ReadTokens"
        Action   = ["secretsmanager:GetSecretValue"]
        Effect   = "Allow"
        Resource = [aws_secretsmanager_secret.admin_token.arn, aws_secretsmanager_secret.read_token.arn]
      },
      {
        Sid      = "FetchBinaries"
        Action   = ["s3:GetObject"]
        Effect   = "Allow"
        Resource = ["${aws_s3_bucket.artifacts.arn}/${local.server_key}", "${aws_s3_bucket.artifacts.arn}/${local.caddy_key}"]
      },
      {
        Sid      = "AnswerDnsChallenges"
        Action   = ["route53:ChangeResourceRecordSets", "route53:ListResourceRecordSets"]
        Effect   = "Allow"
        Resource = "arn:aws:route53:::hostedzone/${var.hosted_zone_id}"
      },
      {
        Sid      = "FindZone"
        Action   = ["route53:ListHostedZonesByName", "route53:ListHostedZones", "route53:GetChange"]
        Effect   = "Allow"
        Resource = "*"
      },
    ]
  })
}

resource "aws_iam_instance_profile" "instance" {
  name = var.name
  role = aws_iam_role.instance.name
}

resource "aws_security_group" "host" {
  name        = var.name
  description = "Reaper tunnel host: control on 8443 for your machines, edge on 443 for visitors"
  vpc_id      = local.vpc_id
}

resource "aws_vpc_security_group_ingress_rule" "control" {
  for_each          = toset(local.control_sources)
  security_group_id = aws_security_group.host.id
  description       = "Agents and the management API"
  cidr_ipv4         = each.value
  from_port         = 8443
  to_port           = 8443
  ip_protocol       = "tcp"
}

resource "aws_vpc_security_group_ingress_rule" "edge" {
  for_each          = toset(local.edge_sources)
  security_group_id = aws_security_group.host.id
  description       = "Visitors reaching a claimed subdomain"
  cidr_ipv4         = each.value
  from_port         = 443
  to_port           = 443
  ip_protocol       = "tcp"
}

resource "aws_vpc_security_group_egress_rule" "all" {
  security_group_id = aws_security_group.host.id
  description       = "Certificate authority, Route 53, Secrets Manager, S3"
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}

# The claims database lives on its own volume so the instance is disposable and the state is
# not. Replacing the instance re-attaches the same volume.
resource "aws_ebs_volume" "state" {
  availability_zone = data.aws_subnet.chosen.availability_zone
  size              = var.state_volume_gib
  type              = "gp3"
  encrypted         = true
  tags = {
    Name = "${var.name}-state"
  }
  lifecycle {
    # The volume is the only durable state; a subnet or zone drift must never replace it.
    ignore_changes = [availability_zone]
  }
}

resource "aws_instance" "tunnel" {
  ami                    = data.aws_ami.linux.id
  instance_type          = var.instance_type
  subnet_id              = local.subnet_id
  vpc_security_group_ids = [aws_security_group.host.id]
  iam_instance_profile   = aws_iam_instance_profile.instance.name
  user_data = templatefile("${path.module}/user-data.sh", {
    region                = var.region
    domain                = var.domain
    acme_email            = var.acme_email
    admin_actor_base64    = base64encode(var.admin_actor)
    admin_token_secret_id = aws_secretsmanager_secret.admin_token.id
    read_token_secret_id  = aws_secretsmanager_secret.read_token.id
    artifact_bucket       = aws_s3_bucket.artifacts.id
    server_key            = local.server_key
    caddy_key             = local.caddy_key
    state_volume_id       = replace(aws_ebs_volume.state.id, "-", "")
  })
  metadata_options {
    http_tokens = "required"
  }
  root_block_device {
    encrypted   = true
    volume_size = 8
  }
  lifecycle {
    # A newer AMI or a changed boot script must never replace the host underneath live tunnels
    # by itself; a changed domain, ACME contact, or actor does, and a deliberate
    # `terraform apply -replace=aws_instance.tunnel` always can. The state volume survives.
    ignore_changes       = [ami, user_data]
    replace_triggered_by = [terraform_data.host_config]
  }
  depends_on = [aws_s3_object.server, aws_s3_object.caddy]
}

resource "aws_volume_attachment" "state" {
  device_name = "/dev/sdf"
  volume_id   = aws_ebs_volume.state.id
  instance_id = aws_instance.tunnel.id
}

resource "aws_eip" "tunnel" {
  domain = "vpc"
}

resource "aws_eip_association" "tunnel" {
  instance_id   = aws_instance.tunnel.id
  allocation_id = aws_eip.tunnel.id
}

resource "aws_route53_record" "apex" {
  zone_id = var.hosted_zone_id
  name    = var.domain
  type    = "A"
  ttl     = 60
  records = [aws_eip.tunnel.public_ip]
}

resource "aws_route53_record" "wildcard" {
  zone_id = var.hosted_zone_id
  name    = "*.${var.domain}"
  type    = "A"
  ttl     = 60
  records = [aws_eip.tunnel.public_ip]
}
