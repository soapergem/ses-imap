account_id := env("AWS_ACCOUNT_ID")
ecr_repo := env("ECR_REPO", "ses-imap")
region := env("AWS_REGION", "us-east-1")
s3_bucket := env("S3_BUCKET")

default:
    @just --list | grep -v "^    default$"

# build the IMAP server binary
build-server:
    go build -ldflags="-s -w" -o bin/imap-server ./cmd/imap-server

# build the Lambda binary (Linux ARM64 for provided.al2023)
build-lambda:
    GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/bootstrap ./cmd/ses-lambda

# package lambda into a zip for deployment
package-lambda: build-lambda
    cd bin && zip ../lambda.zip bootstrap

# build both binaries
build: build-server build-lambda

# run tests
test:
    go test ./...

# build multi-architecture container image
build-image:
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
        --set config.s3Bucket={{s3_bucket}}

# uninstall helm chart
undeploy:
    @helm uninstall ses-imap

# apply terraform
apply:
    cd infra/iac && terraform apply

# plan terraform
plan:
    cd infra/iac && terraform plan

# initialize terraform
init:
    cd infra/iac && terraform init

# clean up build artifacts
clean:
    rm -rf bin/ lambda.zip

# clean up container images
prune:
    @podman manifest rm {{account_id}}.dkr.ecr.{{region}}.amazonaws.com/{{ecr_repo}}:latest || true

# initialize QEMU for multi-architecture builds
setup:
    @sudo podman run --rm --privileged docker.io/multiarch/qemu-user-static --reset -p yes
