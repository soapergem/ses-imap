account_id := env("AWS_ACCOUNT_ID")
ecr_repo := env("ECR_REPO", "ses-imap")
region := env("AWS_REGION", "us-east-1")
s3_bucket := env("S3_BUCKET")
image_pull_secret := env("IMAGE_PULL_SECRET")

default:
    @just --list | grep -v "^    default$"

# build the Lambda zip (Linux ARM64 for provided.al2023)
build-lambda:
    GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/bootstrap ./cmd/ses-lambda
    cd bin && zip ../lambda.zip bootstrap

# run tests
test:
    go test ./...

# build multi-architecture container image
build:
    @podman manifest create {{account_id}}.dkr.ecr.{{region}}.amazonaws.com/{{ecr_repo}}:latest || true
    @podman build --platform linux/arm64 --manifest {{account_id}}.dkr.ecr.{{region}}.amazonaws.com/{{ecr_repo}}:latest .
    @podman build --platform linux/amd64 --manifest {{account_id}}.dkr.ecr.{{region}}.amazonaws.com/{{ecr_repo}}:latest .

# log into ECR
login:
    @aws ecr get-login-password --region {{region}} | podman login --username AWS --password-stdin {{account_id}}.dkr.ecr.{{region}}.amazonaws.com

# push container image to ECR
push:
    @podman manifest push {{account_id}}.dkr.ecr.{{region}}.amazonaws.com/{{ecr_repo}}:latest

# deploy helm chart
deploy:
    @helm upgrade --install ses-imap infra/deploy \
        --set image.repository={{account_id}}.dkr.ecr.{{region}}.amazonaws.com/{{ecr_repo}} \
        --set image.pullSecretName={{image_pull_secret}} \
        --set config.s3Bucket={{s3_bucket}}

# uninstall helm chart
undeploy:
    @helm uninstall ses-imap

# apply terraform
apply:
    terraform -chdir=infra/iac apply

# plan terraform
plan:
    terraform -chdir=infra/iac plan

# initialize terraform
init:
    terraform -chdir=infra/iac init

# add an IMAP user to Parameter Store (usage: just add-user user@example.com mypassword)
add-user email password:
    #!/usr/bin/env bash
    PARAM_NAME="/ses-imap/users/$(echo '{{email}}' | sed 's/@/\//')"
    HASH=$(htpasswd -bnBC 10 "" '{{password}}' | tr -d ':\n' | sed 's/$2y/$2a/')
    aws ssm put-parameter \
        --name "$PARAM_NAME" \
        --type SecureString \
        --value "$HASH" \
        --region {{region}} \
        --overwrite
    @echo "Created IMAP user {{email}}"

# clean up build artifacts
clean:
    rm -rf bin/ lambda.zip

# clean up container images
prune:
    @podman manifest rm {{account_id}}.dkr.ecr.{{region}}.amazonaws.com/{{ecr_repo}}:latest || true

# initialize QEMU for multi-architecture builds
setup:
    @sudo podman run --rm --privileged docker.io/multiarch/qemu-user-static --reset -p yes
