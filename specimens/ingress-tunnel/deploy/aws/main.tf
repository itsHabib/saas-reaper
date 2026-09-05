data "aws_caller_identity" "current" {}

data "aws_vpc" "default" {
  count   = var.vpc_id == null ? 1 : 0
  default = true
}

locals {
  vpc_id     = var.vpc_id == null ? data.aws_vpc.default[0].id : var.vpc_id
  go_arch    = var.architecture == "x86_64" ? "amd64" : "arm64"
  specimen   = "${path.module}/../.."
  binary     = "${path.module}/.build/reaper-tunnel-${local.go_arch}"
  object_key = "reaper-tunnel-${local.go_arch}"
  source_digest = sha256(join("", concat(
    [for file in sort(fileset(local.specimen, "{cmd,internal}/**/*.go")) : filesha256("${local.specimen}/${file}")],
    [filesha256("${local.specimen}/go.mod"), filesha256("${local.specimen}/go.sum")],
  )))
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

data "aws_ami" "linux" {
  most_recent = true
  owners      = ["amazon"]
  filter {
    name   = "name"
    values = ["al2023-ami-2023.*-${var.architecture}"]
  }
}

# The server is built here, on the machine running terraform, and shipped through a private
# bucket. The instance never compiles anything and never fetches from a registry.
resource "terraform_data" "server_binary" {
  triggers_replace = [local.source_digest, local.go_arch]
  provisioner "local-exec" {
    working_dir = local.specimen
    command     = "CGO_ENABLED=0 GOOS=linux GOARCH=${local.go_arch} go build -trimpath -o ${abspath(local.binary)} ./cmd/reaper-tunnel"
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
  key         = local.object_key
  source      = local.binary
  source_hash = local.source_digest
  depends_on  = [terraform_data.server_binary]
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
        Resource = [var.admin_token_secret_arn, var.read_token_secret_arn]
      },
      {
        Sid      = "FetchServer"
        Action   = ["s3:GetObject"]
        Effect   = "Allow"
        Resource = "${aws_s3_bucket.artifacts.arn}/${local.object_key}"
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

resource "aws_security_group" "edge" {
  name        = var.name
  description = "Public HTTPS edge for the reaper tunnel host"
  vpc_id      = local.vpc_id
  ingress {
    description = "ACME HTTP challenge fallback and redirect to HTTPS"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = var.allowed_cidrs
  }
  ingress {
    description = "Agents and visitors"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = var.allowed_cidrs
  }
  egress {
    description = "Certificate authority, Route 53, Secrets Manager, S3"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_instance" "tunnel" {
  ami                         = data.aws_ami.linux.id
  instance_type               = var.instance_type
  subnet_id                   = local.subnet_id
  vpc_security_group_ids      = [aws_security_group.edge.id]
  iam_instance_profile        = aws_iam_instance_profile.instance.name
  user_data_replace_on_change = true
  user_data = templatefile("${path.module}/user-data.sh", {
    region                 = var.region
    architecture           = var.architecture
    domain                 = var.domain
    acme_email             = var.acme_email
    admin_actor_base64     = base64encode(var.admin_actor)
    admin_token_secret_arn = var.admin_token_secret_arn
    read_token_secret_arn  = var.read_token_secret_arn
    artifact_bucket        = aws_s3_bucket.artifacts.id
    artifact_key           = local.object_key
    artifact_digest        = local.source_digest
  })
  metadata_options {
    http_tokens = "required"
  }
  root_block_device {
    encrypted   = true
    volume_size = 8
  }
  depends_on = [aws_s3_object.server]
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
