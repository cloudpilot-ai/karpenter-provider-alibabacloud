#!/bin/bash
set -eu -o pipefail

for i in $(
  find ./cmd ./pkg ./hack -name "*.go"
); do
  if ! grep -q "CloudPilot AI" $i; then
    cat hack/boilerplate.go.txt $i >$i.new && mv $i.new $i
  fi
done
