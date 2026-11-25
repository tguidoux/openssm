#!/usr/bin/env bash
#
# OpenSSM — Parameter Store and Documents quick start with the AWS CLI.
#
# Prerequisites: an OpenSSM server on localhost:9330 using
# examples/docker/config.with-creds.yaml (or any config with auth disabled).
#
# Usage:
#   ./examples/commands/quick-start.sh

set -euo pipefail

export AWS_ENDPOINT_URL="${AWS_ENDPOINT_URL:-http://localhost:9330}"
export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-us-east-1}"
export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-AKIAIOSFODNN7EXAMPLE}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY}"
export AWS_PAGER=""

step() { echo; echo "▶ $*"; }

step "Create parameters (String, StringList, SecureString)"
aws ssm put-parameter --name /quickstart/db/host --value db.internal --type String --overwrite
aws ssm put-parameter --name /quickstart/db/replicas --value "r1,r2,r3" --type StringList --overwrite
aws ssm put-parameter --name /quickstart/db/password --value 's3cr3t!' --type SecureString --overwrite

step "Read them back (SecureString decrypted)"
aws ssm get-parameters-by-path --path /quickstart --recursive --with-decryption \
  --query 'Parameters[].[Name,Type,Value]' --output table

step "Version, label and history"
aws ssm put-parameter --name /quickstart/db/host --value db2.internal --overwrite --query Version
aws ssm label-parameter-version --name /quickstart/db/host --parameter-version 1 --labels stable
aws ssm get-parameter --name /quickstart/db/host:stable --query Parameter.Value
aws ssm get-parameter-history --name /quickstart/db/host --query 'Parameters[].[Version,Value,join(`,`, Labels)]' --output table

step "Tags and filters"
aws ssm add-tags-to-resource --resource-type Parameter --resource-id /quickstart/db/password --tags Key=team,Value=data
aws ssm describe-parameters --parameter-filters Key=tag:team,Values=data --query 'Parameters[].Name'

step "Create a Command document"
cat > /tmp/openssm-hello.yaml <<'DOC'
schemaVersion: "2.2"
description: Print a greeting
parameters:
  name: {type: String, default: world}
mainSteps:
  - action: aws:runShellScript
    name: greet
    inputs:
      runCommand: ["echo Hello, {{ name }}!"]
DOC
aws ssm delete-document --name QuickStart-Hello 2>/dev/null || true
aws ssm create-document --name QuickStart-Hello --document-type Command --document-format YAML \
  --content file:///tmp/openssm-hello.yaml --query 'DocumentDescription.[Name,DocumentVersion,Status]'
aws ssm list-documents --filters Key=Owner,Values=Self --query 'DocumentIdentifiers[].Name'

step "Clean up"
aws ssm delete-parameters --names /quickstart/db/host /quickstart/db/replicas /quickstart/db/password
aws ssm delete-document --name QuickStart-Hello
echo; echo "✅ Done"
