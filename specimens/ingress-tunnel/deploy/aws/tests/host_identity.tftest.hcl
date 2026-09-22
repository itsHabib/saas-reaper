mock_provider "aws" {
  mock_data "aws_caller_identity" {
    defaults = { account_id = "123456789012" }
  }
  mock_data "aws_subnet" {
    defaults = { availability_zone = "us-east-1a" }
  }
  mock_data "aws_ami" {
    defaults = { id = "ami-0123456789abcdef0" }
  }
  mock_resource "aws_ebs_volume" {
    defaults = { availability_zone = "us-east-1a" }
  }
}
mock_provider "random" {}

variables {
  region         = "us-east-1"
  domain         = "tunnel.example.com"
  hosted_zone_id = "Z0123456789"
  acme_email     = "ops@example.com"
  vpc_id         = "vpc-0123456789abcdef0"
  subnet_id      = "subnet-0123456789abcdef0"
  architecture   = "x86_64"
  instance_type  = "t3.nano"
}

# Seed only the mocked retained volume. Local binary-build provisioners are outside this graph.
run "retained_volume" {
  command = apply
  plan_options {
    target = [aws_ebs_volume.state]
  }
}

run "architecture_is_host_identity" {
  command = plan
  assert {
    condition     = terraform_data.host_config.input.architecture == "x86_64"
    error_message = "Architecture must participate in the host replacement trigger."
  }
}

run "reject_cross_zone_volume" {
  command = plan
  override_data {
    target = data.aws_subnet.chosen
    values = { availability_zone = "us-east-1b" }
  }
  expect_failures = [aws_instance.tunnel]
}

run "custom_vpc_requires_subnet" {
  command = plan
  variables {
    subnet_id = null
  }
  override_data {
    target = data.aws_subnets.chosen[0]
    values = { ids = ["subnet-0123456789abcdef0"] }
  }
  expect_failures = [aws_instance.tunnel]
}
