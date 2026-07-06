package reconcilers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
	"github.com/user/firmware-updater/internal/secretsruntime"
)

var installTestSecretStore sync.Once

func TestReconcileFirmwareUpdateJobCompletesFromRedfishTask(t *testing.T) {
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
	ensureTestSecretStore(t)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertBasicAuth(t, r)
		switch r.URL.Path {
		case "/redfish/v1/TaskService/Tasks/mock-task":
			http.NotFound(w, r)
		case "/redfish/v1/UpdateService/FirmwareInventory/BMC":
			respondTestJSON(t, w, map[string]interface{}{
				"Version": "nc.1.10.2-build1",
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
