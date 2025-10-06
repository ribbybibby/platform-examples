# `image-copy-ecr-cronjob`

Runs a regular `CronJob` in an AWS EKS cluster that copies recently updated
images from a Chainguard organization to AWS ECR.

## Requirements

These steps assume you have an existing AWS EKS cluster.

## Usage

1. Login to Chainguard, AWS, AWS ECR and configure your Kubernetes context.

```
chainctl auth login
aws sso login
aws ecr get-login-password --region <region> | docker login --username AWS --password-stdin <account-id>.dkr.ecr.<region>.amazonaws.com
aws eks update-kubeconfig --region=<region> --name=<cluster-name>
```

2. Apply the Terraform code.

```
cd iac/

cat <<EOF > terraform.tfvars
# Required. The name of your Chainguard organization.
org_name = "your.org"

# Required. The name of your AWS EKS cluster. The cluster must already exist.
cluster_name = "your-cluster-name"

# Required. The name of the AWS ECR repository to copy images to
repo_name = "chainguard"

# Optional. The namespace for the CronJob. Terraform will create this namespace.
# Defaults to 'chainguard'.
namespace = "chainguard"

# Optional. Whether to ignore tags for signatures and attestations.
ignore_referrers = false

# Optional. The job will only copy images that have been updated within this
# period of time. Defaults to 72h.
updated_within = "72h"
EOF

terraform init

terraform apply -var-file terraform.tfvars
```
