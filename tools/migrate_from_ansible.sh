#!/bin/bash
set -euo pipefail

if [[ ${#MASTER_KEY} -ne 64 ]]; then
    echo "Error: MASTER_KEY environment variable must be a 64-character hex string."
    exit 1
fi

ansible-vault view vaulted_secrets.yml | yq e '.bmc_credentials[] | .id + " " + .username + " " + .password' - | \
while read -r id username password; do
    go run ./cmd/secret-cli \
        --secret-id "$id" \
        --username "$username" \
        --password "$password" \
        --store-path "./secrets.json"
done