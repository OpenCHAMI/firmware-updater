package reconcilers

import (
	"context"
	"encoding/json"
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

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertBasicAuth(t, r)
		if r.URL.Path != "/redfish/v1/TaskService/Tasks/mock-task" {
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

	if job.Status.JobState != "Completed" {
		t.Fatalf("expected Completed, got %q", job.Status.JobState)
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

func TestVersionsSemanticallyEqualStrictComparison(t *testing.T) {
	if !versionsSemanticallyEqual("1.2.0", "1.2.0") {
		t.Fatal("expected versions to match")
	}
	if versionsSemanticallyEqual("1.20.0", "1.2.0") {
		t.Fatal("expected versions to differ")
	}
}

func TestUpdateJobStatusNotifiesOwningCampaignWhenJobFails(t *testing.T) {
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
			JobState: "InProgress",
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

	job.Status.JobState = "Failed"
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
				"test-secret": `{"username":"root","password":"initial0"}`,
			},
		}); err != nil {
			t.Fatalf("SetStore failed: %v", err)
		}
	})
}

func configureFastPollingForTests(t *testing.T) {
	t.Helper()

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
