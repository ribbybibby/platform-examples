# `image-copy-azure`

This example sets up an Azure Function that listens for `registry.push` events
to a private Chainguard Registry group and mirrors those new images to a
repository in Azure Container Registry.

## Setup

Build the Go binary for `linux/amd64`. Save it to the function work directory.

```
GOOS=linux GOARCH=amd64 go build -o iac/function/image-copy-azure .
```

Login to Azure.

```
az login
```

Run the terraform.

It expects three variables:
- `name`: The name of the resources to create. This must be unique across
  Azure, so make it distinctive.
- `subscription_id`: The id of the Azure subscription to create resources under.
- `group_name`: The name of the Chainguard group to receive events from. For
  instance, `your.org.com`.

```
cd iac/
terraform init
terraform apply \
    -var name=yourorgimagecopy \
    -var subscription_id=$AZURE_SUBSCRIPTION_ID \
    -var group_name=your.org.com
```

To tear down resources, run `terraform destroy`.
