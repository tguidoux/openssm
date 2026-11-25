#!/usr/bin/env bash
#
# OpenSSM — register this machine as a managed instance, run commands on it
# and open an interactive Session Manager shell.
#
# Prerequisites:
#   - An OpenSSM server on localhost:9330 (examples/docker/config.with-creds.yaml)
#   - openssm-agent built:   bazel build //apps/go/agent:openssm-agent
#   - session-manager-plugin installed (for the session step)
#
# Usage:
#   ./examples/commands/run-command-and-sessions.sh

set -euo pipefail

export AWS_ENDPOINT_URL="${AWS_ENDPOINT_URL:-http://localhost:9330}"
export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-us-east-1}"
export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-AKIAIOSFODNN7EXAMPLE}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY}"
export AWS_PAGER=""
AGENT="${AGENT:-$(bazel info bazel-bin 2>/dev/null)/apps/go/agent/openssm-agent_/openssm-agent}"
STATE="${STATE:-$(mktemp -d)/agent.json}"

echo "▶ Create a hybrid activation"
read -r ACT_ID ACT_CODE < <(aws ssm create-activation --iam-role demo --default-instance-name "$(hostname)" \
  --query '[ActivationId,ActivationCode]' --output text)

echo "▶ Start openssm-agent ($AGENT)"
"$AGENT" -server "$AWS_ENDPOINT_URL" -activation-id "$ACT_ID" -activation-code "$ACT_CODE" -state-file "$STATE" &
AGENT_PID=$!
trap 'kill $AGENT_PID 2>/dev/null' EXIT
sleep 2

IID=$(aws ssm describe-instance-information --filters Key=ActivationIds,Values="$ACT_ID" \
  --query 'InstanceInformationList[0].InstanceId' --output text)
echo "▶ Managed instance: $IID"
aws ssm add-tags-to-resource --resource-type ManagedInstance --resource-id "$IID" --tags Key=role,Value=demo

echo "▶ Run a command by tag"
CMD=$(aws ssm send-command --document-name AWS-RunShellScript --targets Key=tag:role,Values=demo \
  --parameters 'commands=["uname -a","whoami","uptime"]' --query Command.CommandId --output text)
aws ssm wait command-executed --command-id "$CMD" --instance-id "$IID"
aws ssm get-command-invocation --command-id "$CMD" --instance-id "$IID" \
  --query '[Status,StandardOutputContent]' --output text

echo "▶ Open an interactive shell (type 'exit' to leave)"
aws ssm start-session --target "$IID"

echo "▶ Session history"
aws ssm describe-sessions --state History --filters key=Target,value="$IID" \
  --query 'Sessions[].[SessionId,Status]' --output table
