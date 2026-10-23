package reconcilers

import (
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/user/firmware-updater/pkg/redfish"
)

const redfishHTTPTimeoutEnvVar = "FIRMWARE_UPDATER_REDFISH_HTTP_TIMEOUT"
const firmwareUpdateTimeoutEnvVar = "FIRMWARE_UPDATER_TIMEOUT"

var configuredRedfishHTTPTimeoutNanos atomic.Int64

func init() {
	timeout := redfish.DefaultHTTPTimeout
	if raw := strings.TrimSpace(os.Getenv(redfishHTTPTimeoutEnvVar)); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			timeout = time.Duration(seconds) * time.Second
		}
	}
	configuredRedfishHTTPTimeoutNanos.Store(int64(timeout))
}

func SetRedfishHTTPTimeout(timeout time.Duration) {
	if timeout <= 0 {
		timeout = redfish.DefaultHTTPTimeout
	}
	configuredRedfishHTTPTimeoutNanos.Store(int64(timeout))
}

func getRedfishHTTPTimeout() time.Duration {
	timeout := time.Duration(configuredRedfishHTTPTimeoutNanos.Load())
	if timeout <= 0 {
		return redfish.DefaultHTTPTimeout
	}
	return timeout
}

func getFirmwareUpdateTimeout() time.Duration {
	seconds, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(firmwareUpdateTimeoutEnvVar)), 10, 64)
	if err != nil || seconds <= 0 || seconds > int64(time.Duration(1<<63-1)/time.Second) {
		return redfishLongPollMaxDuration
	}
	return time.Duration(seconds) * time.Second
}

func newRedfishClient(targetAddress, username, password string) *redfish.Client {
	return redfish.NewClientWithOptions(targetAddress, username, password, redfish.ClientOptions{
		Timeout: getRedfishHTTPTimeout(),
	})
}
