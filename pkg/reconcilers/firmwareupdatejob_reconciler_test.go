package reconcilers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openchami/fabrica/pkg/events"
	"github.com/openchami/fabrica/pkg/fabrica"
	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
	"github.com/user/firmware-updater/internal/secretsruntime"
)

var installTestSecretStore sync.Once

func TestReconcileFirmwareUpdateJobCompletesFromRedfishTask(t *testing.T) {
	configureFastPollingForTests(t)
	ensureTestSecretStore(t)

	for _, scenario := range []struct {
		name     string
		versions []string
		expected string
		state    string
		detail   string
	}{
		{name: "expected version", versions: []string{"1.2.0"}, expected: "1.2.0", state: "Completed"},
		{name: "wait for change after completed task", versions: []string{"1.0.0", "1.0.0", "1.2.0"}, expected: "1.2.0", state: "Completed"},
		{name: "unexpected change", versions: []string{"1.1.0"}, expected: "1.2.0", state: "Failed", detail: "unexpected version"},
		{name: "unexpected build", versions: []string{"1.2.0 build 8"}, expected: "1.2.0 build 7", state: "Failed", detail: "unexpected version"},
		{name: "unchanged timeout", versions: []string{"1.0.0"}, expected: "1.2.0", state: "Failed", detail: "timed out"},
		{name: "already expected but unchanged", versions: []string{"1.0.0"}, expected: "1.0.0", state: "Failed", detail: "timed out"},
		{name: "missing version timeout", versions: []string{""}, expected: "1.2.0", state: "Failed", detail: "timed out"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var inventoryReads int
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assertBasicAuth(t, r)
				switch r.URL.Path {
				case "/redfish/v1/UpdateService/FirmwareInventory/BMC":
					version := scenario.versions[inventoryReads]
					if inventoryReads < len(scenario.versions)-1 {
						inventoryReads++
					}
					respondTestJSON(t, w, map[string]interface{}{"Version": version})
					return
				case "/redfish/v1/TaskService/Tasks/mock-task":
				default:
					t.Fatalf("unexpected path %q", r.URL.Path)
				}
				respondTestJSON(t, w, map[string]interface{}{
					"TaskState":  "Completed",
					"TaskStatus": "OK",
				})
			}))
			defer server.Close()

			job := v1.FirmwareUpdateJob{
				Spec: v1.FirmwareUpdateJobSpec{
					TargetAddress: strings.TrimPrefix(server.URL, "https://"),
					SecretID:      "test-secret",
					Targets:       []string{"/redfish/v1/UpdateService/FirmwareInventory/BMC"},
				},
				Status: v1.FirmwareUpdateJobStatus{
					JobState:       "InProgress",
					TaskID:         "/redfish/v1/TaskService/Tasks/mock-task",
					CurrentVersion: "1.0.0",
					UpdateVersion:  scenario.expected,
				},
			}

			reconciler := &FirmwareUpdateJobReconciler{}
			if err := reconciler.reconcileFirmwareUpdateJob(context.Background(), &job); err != nil {
				t.Fatalf("reconcileFirmwareUpdateJob returned error: %v", err)
			}

			if job.Status.JobState != scenario.state {
				t.Fatalf("expected %s, got %+v", scenario.state, job.Status)
			}
			if !strings.Contains(job.Status.ErrorDetail, scenario.detail) {
				t.Fatalf("expected detail %q, got %q", scenario.detail, job.Status.ErrorDetail)
			}
		})
	}
}

func TestReconcileFirmwareUpdateJobFailsFromRedfishTask(t *testing.T) {
	configureFastPollingForTests(t)
	ensureTestSecretStore(t)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertBasicAuth(t, r)
		respondTestJSON(t, w, map[string]interface{}{
			"TaskState": "Exception",
			"Messages":  []map[string]string{{"Message": "flash failed"}},
		})
	}))
	defer server.Close()

	job := v1.FirmwareUpdateJob{
		Spec: v1.FirmwareUpdateJobSpec{
			TargetAddress: strings.TrimPrefix(server.URL, "https://"),
			SecretID:      "test-secret",
		},
		Status: v1.FirmwareUpdateJobStatus{
			JobState: "InProgress",
			TaskID:   "/redfish/v1/TaskService/Tasks/mock-task",
		},
	}

	reconciler := &FirmwareUpdateJobReconciler{}
	if err := reconciler.reconcileFirmwareUpdateJob(context.Background(), &job); err != nil {
		t.Fatalf("reconcileFirmwareUpdateJob returned error: %v", err)
	}

	if job.Status.JobState != "Failed" {
		t.Fatalf("expected Failed, got %q", job.Status.JobState)
	}
	if !strings.Contains(job.Status.ErrorDetail, "flash failed") {
		t.Fatalf("expected task failure detail, got %q", job.Status.ErrorDetail)
	}
}

func TestReconcileFirmwareUpdateJobFallsBackToInventoryWhenTaskMissing(t *testing.T) {
	configureFastPollingForTests(t)
	ensureTestSecretStore(t)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertBasicAuth(t, r)
		switch r.URL.Path {
		case "/redfish/v1/TaskService/Tasks/mock-task":
			http.NotFound(w, r)
		case "/redfish/v1/UpdateService/FirmwareInventory/BMC":
			respondTestJSON(t, w, map[string]interface{}{
				"Version": "1.10.2",
			})
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	job := v1.FirmwareUpdateJob{
		Spec: v1.FirmwareUpdateJobSpec{
			TargetAddress: strings.TrimPrefix(server.URL, "https://"),
			SecretID:      "test-secret",
			Targets:       []string{"/redfish/v1/UpdateService/FirmwareInventory/BMC"},
		},
		Status: v1.FirmwareUpdateJobStatus{
			JobState:        "InProgress",
			TaskID:          "/redfish/v1/TaskService/Tasks/mock-task",
			ResolvedVersion: "1.10.2",
			CurrentVersion:  "1.10.1",
		},
	}

	reconciler := &FirmwareUpdateJobReconciler{}
	if err := reconciler.reconcileFirmwareUpdateJob(context.Background(), &job); err != nil {
		t.Fatalf("reconcileFirmwareUpdateJob returned error: %v", err)
	}

	if job.Status.JobState != "Completed" {
		t.Fatalf("expected Completed, got %q", job.Status.JobState)
	}
}

func TestReconcileFirmwareUpdateJobIgnoresInventoryWarning(t *testing.T) {
	configureFastPollingForTests(t)
	ensureTestSecretStore(t)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertBasicAuth(t, r)
		if r.URL.Path != "/redfish/v1/UpdateService/FirmwareInventory/BMC" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		respondTestJSON(t, w, map[string]interface{}{
			"Version": "1.10.3",
			"Status": map[string]interface{}{
				"Health": "Warning",
				"Conditions": []map[string]interface{}{{
					"Message":  "Required 'version' file was missing from firmware archive.",
					"Severity": "Warning",
				}},
			},
		})
	}))
	defer server.Close()

	job := v1.FirmwareUpdateJob{
		Spec: v1.FirmwareUpdateJobSpec{
			TargetAddress: strings.TrimPrefix(server.URL, "https://"),
			SecretID:      "test-secret",
			Targets:       []string{"/redfish/v1/UpdateService/FirmwareInventory/BMC"},
		},
		Status: v1.FirmwareUpdateJobStatus{
			JobState:        "InProgress",
			ResolvedVersion: "1.10.3",
			CurrentVersion:  "1.10.2",
		},
	}

	reconciler := &FirmwareUpdateJobReconciler{}
	if err := reconciler.reconcileFirmwareUpdateJob(context.Background(), &job); err != nil {
		t.Fatalf("reconcileFirmwareUpdateJob returned error: %v", err)
	}

	if job.Status.JobState != "Completed" {
		t.Fatalf("expected Completed, got %q", job.Status.JobState)
	}
	if job.Status.ErrorDetail != "" {
		t.Fatalf("expected no error detail, got %q", job.Status.ErrorDetail)
	}
}

func TestReconcileFirmwareUpdateJobFailsOnInventoryWarningDownloadFailed(t *testing.T) {
	configureFastPollingForTests(t)
	ensureTestSecretStore(t)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertBasicAuth(t, r)
		if r.URL.Path != "/redfish/v1/UpdateService/FirmwareInventory/BMC" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		respondTestJSON(t, w, map[string]interface{}{
			"Version": "nc.1.10.3-24-shasta-release.arm",
			"Status": map[string]interface{}{
				"Health": "Warning",
				"Conditions": []map[string]interface{}{{
					"Message":   "Firmware package download failed.",
					"MessageId": "HPEFirmwareUpdate.1.0.DownloadFailed",
					"Severity":  "Warning",
				}},
			},
		})
	}))
	defer server.Close()

	job := v1.FirmwareUpdateJob{
		Spec: v1.FirmwareUpdateJobSpec{
			TargetAddress: strings.TrimPrefix(server.URL, "https://"),
			SecretID:      "test-secret",
			Targets:       []string{"/redfish/v1/UpdateService/FirmwareInventory/BMC"},
		},
		Status: v1.FirmwareUpdateJobStatus{
			JobState: "InProgress",
		},
	}

	reconciler := &FirmwareUpdateJobReconciler{}
	if err := reconciler.reconcileFirmwareUpdateJob(context.Background(), &job); err != nil {
		t.Fatalf("reconcileFirmwareUpdateJob returned error: %v", err)
	}

	if job.Status.JobState != "Failed" {
		t.Fatalf("expected Failed, got %q", job.Status.JobState)
	}
	detailLower := strings.ToLower(job.Status.ErrorDetail)
	if !strings.Contains(detailLower, "downloadfailed") && !strings.Contains(detailLower, "download failed") {
		t.Fatalf("expected error detail to include download failure context, got %q", job.Status.ErrorDetail)
	}
}

func TestReconcileFirmwareUpdateJobIncludesStructuredRedfishErrorDetail(t *testing.T) {
	configureFastPollingForTests(t)
	ensureTestSecretStore(t)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertBasicAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{
			"@odata.error": {
				"code": "Base.1.0.GeneralError",
				"message": "A general error has occurred.",
				"@Message.ExtendedInfo": [
					{
						"MessageId": "UpdateService.1.0.ImageTransferProtocolNotSupported",
						"Message": "The requested transfer protocol is not supported.",
						"Resolution": "Use HTTPS for this BMC."
					}
				]
			}
		}`))
	}))
	defer server.Close()

	job := v1.FirmwareUpdateJob{
		Spec: v1.FirmwareUpdateJobSpec{
			TargetAddress: strings.TrimPrefix(server.URL, "https://"),
			SecretID:      "test-secret",
		},
		Status: v1.FirmwareUpdateJobStatus{
			JobState: "InProgress",
			TaskID:   "/redfish/v1/TaskService/Tasks/mock-task",
		},
	}

	reconciler := &FirmwareUpdateJobReconciler{}
	if err := reconciler.reconcileFirmwareUpdateJob(context.Background(), &job); err != nil {
		t.Fatalf("reconcileFirmwareUpdateJob returned error: %v", err)
	}

	if job.Status.JobState != "Failed" {
		t.Fatalf("expected Failed, got %q", job.Status.JobState)
	}
	if !strings.Contains(job.Status.ErrorDetail, "ImageTransferProtocolNotSupported") {
		t.Fatalf("expected MessageId in error detail, got %q", job.Status.ErrorDetail)
	}
	if !strings.Contains(job.Status.ErrorDetail, "Use HTTPS for this BMC") {
		t.Fatalf("expected Resolution in error detail, got %q", job.Status.ErrorDetail)
	}
}

func TestRedfishDispatchURI(t *testing.T) {
	tests := []struct {
		name          string
		targetAddress string
		actionURI     string
		expected      string
	}{
		{
			name:          "relative uri",
			targetAddress: "10.0.0.1",
			actionURI:     "/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate",
			expected:      "https://10.0.0.1/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate",
		},
		{
			name:          "relative uri without leading slash",
			targetAddress: "10.0.0.1",
			actionURI:     "redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate",
			expected:      "https://10.0.0.1/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate",
		},
		{
			name:          "absolute uri",
			targetAddress: "10.0.0.1",
			actionURI:     "https://example.invalid/update",
			expected:      "https://example.invalid/update",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redfishDispatchURI(tt.targetAddress, tt.actionURI); got != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestBuildDryRunSuccessMessageIncludesURIAndPayload(t *testing.T) {
	uri := "https://10.0.0.1/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate"
	payload := `{"ImageURI":"http://127.0.0.1/firmware-proxy/layer/sha256:abc","Targets":["/redfish/v1/UpdateService/FirmwareInventory/BMC"]}`

	msg := buildDryRunSuccessMessage(uri, payload)
	if !strings.Contains(msg, uri) {
		t.Fatalf("expected message to include uri %q, got %q", uri, msg)
	}
	if !strings.Contains(msg, payload) {
		t.Fatalf("expected message to include payload %q, got %q", payload, msg)
	}
}

func TestBuildRedfishUpdatePayloadIncludesExpectedFields(t *testing.T) {
	res := &v1.FirmwareUpdateJob{
		Spec: v1.FirmwareUpdateJobSpec{
			Targets:   []string{"/redfish/v1/UpdateService/FirmwareInventory/BMC"},
			Component: "BMC",
		},
	}
	profile := v1.DeviceProfile{
		Spec: v1.DeviceProfileSpec{
			ProfileID:             "test-profile",
			UpdatePayloadTemplate: `{"ImageURI":"%imageURI%","Targets":["%target%"],"Component":"%component%"}`,
		},
	}

	payload, payloadJSON, err := buildRedfishUpdatePayload(res, "http://127.0.0.1/firmware-proxy/layer/sha256:abc", profile)
	if err != nil {
		t.Fatalf("buildRedfishUpdatePayload returned error: %v", err)
	}

	if got, _ := payload["ImageURI"].(string); got != "http://127.0.0.1/firmware-proxy/layer/sha256:abc" {
		t.Fatalf("unexpected image uri in payload: %q", got)
	}
	targetsRaw, ok := payload["Targets"].([]interface{})
	if !ok || len(targetsRaw) != 1 || targetsRaw[0] != "/redfish/v1/UpdateService/FirmwareInventory/BMC" {
		t.Fatalf("unexpected targets in payload: %#v", payload["Targets"])
	}
	if got, _ := payload["Component"].(string); got != "BMC" {
		t.Fatalf("unexpected component in payload: %q", got)
	}
	if !strings.Contains(payloadJSON, "\"ImageURI\":\"http://127.0.0.1/firmware-proxy/layer/sha256:abc\"") {
		t.Fatalf("expected compact payload json to include image uri, got %q", payloadJSON)
	}
}

func TestVersionsSemanticallyEqualStrictComparison(t *testing.T) {
	if !versionsSemanticallyEqual("1.2.0", "1.2.0") {
		t.Fatal("expected versions to match")
	}
	if versionsSemanticallyEqual("1.20.0", "1.2.0") {
		t.Fatal("expected versions to differ")
	}
}

func TestVersionsSemanticallyEqualTwoComponentVersions(t *testing.T) {
	if !versionsSemanticallyEqual("1.2", "1.2.0") {
		t.Fatal("expected two-component and three-component versions to match")
	}
	if versionsSemanticallyEqual("1.2", "1.3") {
		t.Fatal("expected two-component versions to differ")
	}
	if !versionsSemanticallyEqual("nc.1.2", "1.2.0") {
		t.Fatal("expected vendor-prefixed two-component version to normalize")
	}
}

func TestFirmwareUpdateJobRejectsMultipleEndpoints(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redfish/v1/UpdateService/FirmwareInventory":
			respondTestJSON(t, w, map[string]interface{}{
				"Members": []map[string]string{{"@odata.id": "/A"}, {"@odata.id": "/B"}},
			})
		default:
			respondTestJSON(t, w, map[string]interface{}{"Name": "BMC", "Version": "1.0.0"})
		}
	}))
	defer server.Close()
	job := &v1.FirmwareUpdateJob{
		Spec: v1.FirmwareUpdateJobSpec{
			TargetAddress: strings.TrimPrefix(server.URL, "https://"),
			Targets:       []string{"/A", "/B"},
		},
		Status: v1.FirmwareUpdateJobStatus{UpdateVersion: "1.2.0"},
	}
	if err := captureFirmwareVersions(context.Background(), job, bmcCredentials{}); err == nil || !strings.Contains(err.Error(), "exactly one firmware endpoint") {
		t.Fatalf("expected capture to reject multiple endpoints, got %v", err)
	}
	for _, discover := range []bool{false, true} {
		if discover {
			job.Spec.Targets = nil
			job.Spec.Component = "BMC"
		}
		verification, err := verifyFirmwareTargetsUpdatedOnce(context.Background(), job, bmcCredentials{})
		if err != nil || !verification.Failed || !strings.Contains(verification.Detail, "exactly one firmware endpoint") {
			t.Fatalf("expected verification to reject multiple endpoints (discover=%t), got %+v, err %v", discover, verification, err)
		}
	}
}

func TestCaptureFirmwareVersionsAndVerifyAfterStorageRoundTrip(t *testing.T) {
	configureFastPollingForTests(t)
	installed := "1.0.0"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respondTestJSON(t, w, map[string]interface{}{"Version": installed})
	}))
	defer server.Close()

	deadline := time.Now().Add(time.Minute)
	job := v1.FirmwareUpdateJob{
		Spec: v1.FirmwareUpdateJobSpec{
			TargetAddress: strings.TrimPrefix(server.URL, "https://"),
			Targets:       []string{"/A"},
		},
		Status: v1.FirmwareUpdateJobStatus{
			UpdateVersion: "1.2.0",
			Timeout:       &deadline,
		},
	}
	if err := captureFirmwareVersions(context.Background(), &job, bmcCredentials{}); err != nil {
		t.Fatal(err)
	}
	if job.Status.CurrentVersion != "1.0.0" {
		t.Fatalf("expected current version baseline, got %+v", job.Status)
	}
	raw, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	var restored v1.FirmwareUpdateJob
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Status.CurrentVersion != "1.0.0" || strings.Contains(string(raw), "initialVersions") {
		t.Fatalf("expected only currentVersion baseline in storage, got %s", raw)
	}
	if restored.Status.Timeout == nil || !restored.Status.Timeout.Equal(deadline) {
		t.Fatal("verification deadline did not survive storage")
	}
	for _, scenario := range []struct {
		name    string
		version string
		updated bool
		failed  bool
	}{
		{name: "unchanged", version: "1.0.0"},
		{name: "unexpected change", version: "1.1.0", failed: true},
		{name: "updated", version: "1.2.0", updated: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			installed = scenario.version
			verification, err := verifyFirmwareTargetsUpdatedOnce(context.Background(), &restored, bmcCredentials{})
			if err != nil || verification.Updated != scenario.updated || verification.Failed != scenario.failed {
				t.Fatalf("unexpected verification %+v, err %v", verification, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := verifyFirmwareTargetsUpdatedWithBackoff(ctx, &restored, bmcCredentials{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected parent cancellation, got %v", err)
	}
	if !restored.Status.Timeout.Equal(deadline) {
		t.Fatal("verification restarted its timeout")
	}
	expired := time.Now().Add(-time.Second)
	restored.Status.Timeout = &expired
	verification, err := verifyFirmwareTargetsUpdatedWithBackoff(context.Background(), &restored, bmcCredentials{})
	if err != nil || !verification.Failed || !strings.Contains(verification.Detail, "timed out") {
		t.Fatalf("expected expired verification to fail, got %+v, err %v", verification, err)
	}
}

func TestReconcileFirmwareUpdateJobFailsWhenInventoryUnavailableUntilTimeout(t *testing.T) {
	configureFastPollingForTests(t)
	ensureTestSecretStore(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	job := v1.FirmwareUpdateJob{
		Spec: v1.FirmwareUpdateJobSpec{
			TargetAddress: strings.TrimPrefix(server.URL, "https://"),
			SecretID:      "test-secret",
			Targets:       []string{"/A"},
		},
		Status: v1.FirmwareUpdateJobStatus{
			JobState: "InProgress", CurrentVersion: "1.0.0", UpdateVersion: "1.2.0",
		},
	}
	reconciler := &FirmwareUpdateJobReconciler{}
	if err := reconciler.reconcileFirmwareUpdateJob(context.Background(), &job); err != nil {
		t.Fatal(err)
	}
	if job.Status.JobState != "Failed" || !strings.Contains(job.Status.ErrorDetail, "timed out") {
		t.Fatalf("expected terminal timeout failure, got %+v", job.Status)
	}
}

func TestVerifyFirmwareTargetsUpdatedRejectsUnexpectedVersionChange(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respondTestJSON(t, w, map[string]interface{}{"Version": "1.1.0"})
	}))
	defer server.Close()

	job := &v1.FirmwareUpdateJob{
		Spec: v1.FirmwareUpdateJobSpec{
			TargetAddress: strings.TrimPrefix(server.URL, "https://"),
			Targets:       []string{"/redfish/v1/UpdateService/FirmwareInventory/BMC"},
		},
		Status: v1.FirmwareUpdateJobStatus{
			CurrentVersion:  "1.0.0",
			ResolvedVersion: "1.2.0",
		},
	}
	verification, err := verifyFirmwareTargetsUpdatedOnce(context.Background(), job, bmcCredentials{})
	if err != nil {
		t.Fatal(err)
	}
	if !verification.Failed || !strings.Contains(verification.Detail, "1.1.0") || !strings.Contains(verification.Detail, "1.2.0") {
		t.Fatalf("expected unexpected version failure with actual and expected versions, got %+v", verification)
	}
}

func TestVerifyFirmwareTargetsUpdatedWithBackoffRespectsParentContextTimeout(t *testing.T) {
	ensureTestSecretStore(t)
	configureFastPollingForTests(t)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertBasicAuth(t, r)
		if strings.HasSuffix(r.URL.Path, "/A") {
			respondTestJSON(t, w, map[string]interface{}{
				"Version": "1.0.0",
			})
			return
		}
		respondTestJSON(t, w, map[string]interface{}{})
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()

	res := &v1.FirmwareUpdateJob{
		Spec: v1.FirmwareUpdateJobSpec{
			TargetAddress: strings.TrimPrefix(server.URL, "https://"),
			Targets:       []string{"/redfish/v1/UpdateService/FirmwareInventory/A"},
		},
		Status: v1.FirmwareUpdateJobStatus{
			ResolvedVersion: "1.2.0",
			CurrentVersion:  "1.0.0",
		},
	}

	verification, err := verifyFirmwareTargetsUpdatedWithBackoff(ctx, res, bmcCredentials{Username: "root", Password: "initial0"})
	if err == nil {
		t.Fatalf("expected timeout error, got verification: %+v", verification)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
}

func TestUpdateJobStatusNotifiesOwningCampaignWhenJobStatusChanges(t *testing.T) {
	ctx := context.Background()
	client, cleanup := setupReconcilerTestStorageClient(t)
	defer cleanup()

	bus := events.NewInMemoryEventBus(16, 1)
	bus.Start()
	defer bus.Close()

	previousBus := events.GetGlobalEventBus()
	events.SetGlobalEventBus(bus)
	defer events.SetGlobalEventBus(previousBus)

	previousConfig := events.GetEventConfig()
	events.SetEventConfig(&events.EventConfig{
		Enabled:                true,
		LifecycleEventsEnabled: true,
		ConditionEventsEnabled: previousConfig.ConditionEventsEnabled,
		EventTypePrefix:        previousConfig.EventTypePrefix,
		ConditionEventPrefix:   previousConfig.ConditionEventPrefix,
		Source:                 previousConfig.Source,
	})
	defer events.SetEventConfig(previousConfig)

	campaign := &v1.FirmwareUpdateCampaign{
		APIVersion: "hardware.fabrica.dev/v1",
		Kind:       "FirmwareUpdateCampaign",
		Metadata: fabrica.Metadata{
			Name: "campaign",
			UID:  "firmwareupdatecampaign-test",
		},
	}
	if err := client.Create(ctx, campaign); err != nil {
		t.Fatalf("create campaign: %v", err)
	}

	job := &v1.FirmwareUpdateJob{
		APIVersion: "hardware.fabrica.dev/v1",
		Kind:       "FirmwareUpdateJob",
		Metadata: fabrica.Metadata{
			Name: "job",
			UID:  "firmwareupdatejob-test",
			Annotations: map[string]string{
				v1.CampaignUIDAnnotation: campaign.Metadata.UID,
			},
		},
		Status: v1.FirmwareUpdateJobStatus{
			JobState: "Pending",
		},
	}
	if err := client.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	eventsCh := make(chan events.Event, 1)
	if _, err := bus.Subscribe("**", func(_ context.Context, event events.Event) error {
		if event.ResourceKind() == "FirmwareUpdateCampaign" && event.ResourceUID() == campaign.Metadata.UID {
			select {
			case eventsCh <- event:
			default:
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("subscribe events: %v", err)
	}

	job.Status.JobState = "Resolving"
	reconciler := NewDefaultFirmwareUpdateJobReconciler(client, bus)
	if err := reconciler.updateJobStatus(ctx, job); err != nil {
		t.Fatalf("updateJobStatus returned error: %v", err)
	}

	select {
	case event := <-eventsCh:
		if event.ResourceKind() != "FirmwareUpdateCampaign" {
			t.Fatalf("expected FirmwareUpdateCampaign event, got %q", event.ResourceKind())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for owning campaign notification")
	}
}

func ensureTestSecretStore(t *testing.T) {
	t.Helper()
	installTestSecretStore.Do(func() {
		if err := secretsruntime.SetStore(fakeSecretStore{
			secrets: map[string]string{
				"test-secret":    `{"username":"root","password":"initial0"}`,
				"binding-secret": `{"username":"root","password":"initial0"}`,
				"10.0.0.77":      `{"username":"root","password":"initial0"}`,
				"fleet-default":  `{"username":"root","password":"initial0"}`,
				"blank-secret":   `{"username":"","password":""}`,
			},
		}); err != nil {
			t.Fatalf("SetStore failed: %v", err)
		}
	})
}

func TestFirmwareUpdateTimeoutFromEnvironment(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "empty uses default", want: 30 * time.Minute},
		{name: "configured seconds", value: "120", want: 2 * time.Minute},
		{name: "trim whitespace", value: " 60 ", want: time.Minute},
		{name: "invalid uses default", value: "invalid", want: 30 * time.Minute},
		{name: "zero uses default", value: "0", want: 30 * time.Minute},
		{name: "negative uses default", value: "-1", want: 30 * time.Minute},
		{name: "overflow uses default", value: "9223372036854775807", want: 30 * time.Minute},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Setenv(firmwareUpdateTimeoutEnvVar, scenario.value)
			if got := getFirmwareUpdateTimeout(); got != scenario.want {
				t.Fatalf("expected timeout %s, got %s", scenario.want, got)
			}
			before := time.Now()
			job := &v1.FirmwareUpdateJob{}
			if _, err := verifyFirmwareTargetsUpdatedWithBackoff(context.Background(), job, bmcCredentials{}); err != nil {
				t.Fatal(err)
			}
			if job.Status.Timeout == nil || job.Status.Timeout.Before(before.Add(scenario.want)) || job.Status.Timeout.After(time.Now().Add(scenario.want)) {
				t.Fatalf("expected saved expiration using %s, got %v", scenario.want, job.Status.Timeout)
			}
			original := *job.Status.Timeout
			t.Setenv(firmwareUpdateTimeoutEnvVar, "300")
			if _, err := verifyFirmwareTargetsUpdatedWithBackoff(context.Background(), job, bmcCredentials{}); err != nil {
				t.Fatal(err)
			}
			if !job.Status.Timeout.Equal(original) {
				t.Fatal("existing timeout changed when environment changed")
			}
		})
	}
}

func configureFastPollingForTests(t *testing.T) {
	t.Helper()
	t.Setenv(firmwareUpdateTimeoutEnvVar, "")

	originalMaxDuration := redfishLongPollMaxDuration
	originalMinInterval := redfishLongPollMinInterval
	originalMaxInterval := redfishLongPollMaxInterval

	redfishLongPollMaxDuration = 200 * time.Millisecond
	redfishLongPollMinInterval = 5 * time.Millisecond
	redfishLongPollMaxInterval = 10 * time.Millisecond

	t.Cleanup(func() {
		redfishLongPollMaxDuration = originalMaxDuration
		redfishLongPollMinInterval = originalMinInterval
		redfishLongPollMaxInterval = originalMaxInterval
	})
}

func assertBasicAuth(t *testing.T, r *http.Request) {
	t.Helper()
	username, password, ok := r.BasicAuth()
	if !ok {
		t.Fatal("expected basic auth credentials")
	}
	if username != "root" || password != "initial0" {
		t.Fatalf("unexpected basic auth credentials %q/%q", username, password)
	}
}

func respondTestJSON(t *testing.T, w http.ResponseWriter, payload interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("encode json: %v", err)
	}
}

type fakeSecretStore struct {
	secrets map[string]string
}

func (f fakeSecretStore) GetSecretByID(secretID string) (string, error) {
	return f.secrets[secretID], nil
}

func (f fakeSecretStore) StoreSecretByID(secretID, secret string) error {
	if f.secrets == nil {
		f.secrets = map[string]string{}
	}
	f.secrets[secretID] = secret
	return nil
}

func (f fakeSecretStore) ListSecrets() (map[string]string, error) {
	cloned := make(map[string]string, len(f.secrets))
	for key, value := range f.secrets {
		cloned[key] = value
	}
	return cloned, nil
}

func (f fakeSecretStore) RemoveSecretByID(secretID string) error {
	delete(f.secrets, secretID)
	return nil
}
