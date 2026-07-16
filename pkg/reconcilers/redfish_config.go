package reconcilers

import (
	"sync/atomic"
	"time"

	"github.com/user/firmware-updater/pkg/redfish"
)

var configuredRedfishHTTPTimeoutNanos atomic.Int64

func init() {
	configuredRedfishHTTPTimeoutNanos.Store(int64(redfish.DefaultHTTPTimeout))
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

func newRedfishClient(targetAddress, username, password string) *redfish.Client {
	return redfish.NewClientWithOptions(targetAddress, username, password, redfish.ClientOptions{
		Timeout: getRedfishHTTPTimeout(),
	})
}
