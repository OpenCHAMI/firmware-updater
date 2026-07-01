#!/bin/bash
set -e

echo "1. Generating MASTER_KEY..."
export MASTER_KEY="$(openssl rand -hex 32)"
echo "MASTER_KEY set."

echo "2. Generating out-of-band secrets.json..."
TEST_DIR=$(mktemp -d)
SECRETS_FILE="${TEST_DIR}/secrets.json"
SERVER_LOG="${TEST_DIR}/server.log"

go run ./cmd/secret-cli \
  --secret-id demo-bmc \
  --username admin \
  --password password \
  --store-path "$SECRETS_FILE"

echo "3. Starting the Fabrica server in the background..."
MASTER_KEY="$MASTER_KEY" go run ./cmd/server serve --port 18090 --database-url="file:hpc_test.db?cache=shared&_fk=1"  --secrets-file "$SECRETS_FILE" > "$SERVER_LOG" 2>&1 &
SERVER_PID=$!

sleep 3

echo "4. Executing API request using the secretID..."

# Temporarily disable exit-on-error to handle the curl failure gracefully
set +e
HTTP_STATUS=$(curl -s -o "${TEST_DIR}/response.json" -w "%{http_code}" -X POST http://127.0.0.1:18090/firmwareupdatejobs/ \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata":{"name":"secretid-demo"},
    "spec":{
      "targetAddress":"192.0.2.10",
      "secretID":"demo-bmc",
      "serverProxyAddress":"127.0.0.1",
      "ociReference":"127.0.0.1:5000/firmware/test-bmc:1.0.0",
      "targets":["/redfish/v1/UpdateService/FirmwareInventory/BMC"]
    }
  }')
CURL_EXIT_CODE=$?
set -e

if [ $CURL_EXIT_CODE -ne 0 ] || [ -z "$HTTP_STATUS" ] || [ "$HTTP_STATUS" = "000" ]; then
    echo "ERROR: API request failed. The server likely crashed."
    echo "--- SERVER LOG OUTPUT ---"
    cat "$SERVER_LOG"
    echo "-------------------------"
else
    echo "API Response HTTP Status: $HTTP_STATUS"
    cat "${TEST_DIR}/response.json"
    echo ""
fi

echo "5. Cleaning up..."
# Ensure the process exists before trying to kill it
if kill -0 $SERVER_PID 2>/dev/null; then
    kill $SERVER_PID
fi
rm -rf "$TEST_DIR"