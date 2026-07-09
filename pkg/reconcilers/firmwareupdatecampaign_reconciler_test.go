package reconcilers

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/mattn/go-sqlite3"
	"github.com/openchami/fabrica/pkg/fabrica"
	"github.com/openchami/fabrica/pkg/resource"
	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
	"github.com/user/firmware-updater/internal/storage"
	"github.com/user/firmware-updater/internal/storage/ent"
	"github.com/user/firmware-updater/internal/storage/ent/migrate"
)

func TestSummarizeCampaignJobsCountsChildJobs(t *testing.T) {
	jobs := map[string]*v1.FirmwareUpdateJob{
		"10.0.0.1|bmc": {
			Metadata: v1.FirmwareUpdateJob{}.Metadata,
			Spec:     v1.FirmwareUpdateJobSpec{TargetAddress: "10.0.0.1"},
			Status:   v1.FirmwareUpdateJobStatus{JobState: v1.CampaignStateCompleted},
		},
		"10.0.0.1|bios": {
			Metadata: v1.FirmwareUpdateJob{}.Metadata,
			Spec:     v1.FirmwareUpdateJobSpec{TargetAddress: "10.0.0.1"},
			Status:   v1.FirmwareUpdateJobStatus{JobState: v1.CampaignStateFailed},
		},
		"10.0.0.2|bmc": {
			Metadata: v1.FirmwareUpdateJob{}.Metadata,
			Spec:     v1.FirmwareUpdateJobSpec{TargetAddress: "10.0.0.2"},
			Status:   v1.FirmwareUpdateJobStatus{JobState: "Resolving"},
		},
	}

	summary, childJobs := summarizeCampaignJobs(jobs)
	if summary.Total != 3 {
		t.Fatalf("expected total child jobs to be 3, got %d", summary.Total)
	}
	if summary.Completed != 1 || summary.Failed != 1 || summary.Pending != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if len(childJobs) != 3 {
		t.Fatalf("expected 3 child jobs, got %d", len(childJobs))
	}
}

func TestDeriveCampaignStateCompletedWithErrors(t *testing.T) {
	state := deriveCampaignState(v1.CampaignSummary{Total: 2, Completed: 1, Failed: 1})
	if state != v1.CampaignStateCompletedWithErrors {
		t.Fatalf("expected CompletedWithErrors, got %s", state)
	}
}

func TestTokenizeHardwareHintExtractsModelPrefix(t *testing.T) {
	tokens := tokenizeHardwareHint("/redfish/v1/Systems/x9000c3s7b1")
	found := false
	for _, token := range tokens {
		if token == "x9000" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected tokenized hardware hints to include model prefix x9000, got %v", tokens)
	}
}

func TestBuildUniversalDiscoveryRepositories(t *testing.T) {
	repositories := buildUniversalDiscoveryRepositories("127.0.0.1:5000/firmware", "Cabinet Controller")
	if len(repositories) != 3 {
		t.Fatalf("expected 3 repository candidates, got %d: %v", len(repositories), repositories)
	}
	if repositories[0] != "127.0.0.1:5000/firmware/cabinet-controller" {
		t.Fatalf("unexpected first repository candidate: %s", repositories[0])
	}
	if repositories[1] != "127.0.0.1:5000/firmware/cabinetcontroller" {
		t.Fatalf("unexpected compact repository candidate: %s", repositories[1])
	}
	if repositories[2] != "127.0.0.1:5000/firmware" {
		t.Fatalf("unexpected base repository fallback: %s", repositories[2])
	}
}

func TestCampaignJobIsActive(t *testing.T) {
	tests := []struct {
		name     string
		job      *v1.FirmwareUpdateJob
		expected bool
	}{
		{
			name:     "nil job is not active",
			job:      nil,
			expected: false,
		},
		{
			name: "empty state is active",
			job: &v1.FirmwareUpdateJob{
				Status: v1.FirmwareUpdateJobStatus{JobState: ""},
			},
			expected: true,
		},
		{
			name: "in-progress state is active",
			job: &v1.FirmwareUpdateJob{
				Status: v1.FirmwareUpdateJobStatus{JobState: v1.CampaignStateInProgress},
			},
			expected: true,
		},
		{
			name: "completed state is not active",
			job: &v1.FirmwareUpdateJob{
				Status: v1.FirmwareUpdateJobStatus{JobState: v1.CampaignStateCompleted},
			},
			expected: false,
		},
		{
			name: "failed state is not active",
			job: &v1.FirmwareUpdateJob{
				Status: v1.FirmwareUpdateJobStatus{JobState: v1.CampaignStateFailed},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if actual := campaignJobIsActive(tt.job); actual != tt.expected {
				t.Fatalf("expected %t, got %t", tt.expected, actual)
			}
		})
	}
}

func TestBuildActiveCampaignTargets(t *testing.T) {
	jobs := map[string]*v1.FirmwareUpdateJob{
		"10.0.0.1|bmc": {
			Metadata: fabrica.Metadata{Annotations: map[string]string{v1.CampaignTargetAnnotation: "10.0.0.1"}},
			Status:   v1.FirmwareUpdateJobStatus{JobState: v1.CampaignStateInProgress},
		},
		"10.0.0.1|bios": {
			Metadata: fabrica.Metadata{Annotations: map[string]string{v1.CampaignTargetAnnotation: "10.0.0.1"}},
			Status:   v1.FirmwareUpdateJobStatus{JobState: v1.CampaignStateCompleted},
		},
		"10.0.0.2|bmc": {
			Metadata: fabrica.Metadata{Annotations: map[string]string{v1.CampaignTargetAnnotation: "10.0.0.2"}},
			Status:   v1.FirmwareUpdateJobStatus{JobState: v1.CampaignStateFailed},
		},
		"10.0.0.3|bmc": {
			Metadata: fabrica.Metadata{Annotations: map[string]string{v1.CampaignTargetAnnotation: "10.0.0.3"}},
			Status:   v1.FirmwareUpdateJobStatus{JobState: ""},
		},
	}

	activeTargets := buildActiveCampaignTargets(jobs)
	if !activeTargets["10.0.0.1"] {
		t.Fatalf("expected target 10.0.0.1 to be active")
	}
	if !activeTargets["10.0.0.3"] {
		t.Fatalf("expected target 10.0.0.3 to be active")
	}
	if activeTargets["10.0.0.2"] {
		t.Fatalf("expected target 10.0.0.2 to be inactive")
	}
	if len(activeTargets) != 2 {
		t.Fatalf("expected exactly 2 active targets, got %d", len(activeTargets))
	}
}

func TestReconcileDesiredCampaignJobs_SequencesPerTargetAcrossReconciles(t *testing.T) {
	ctx := context.Background()
	client, cleanup := setupReconcilerTestStorageClient(t)
	defer cleanup()

	resource.RegisterResourcePrefix("FirmwareUpdateJob", "firmwareupdatejob")
	reconciler := NewDefaultFirmwareUpdateCampaignReconciler(client, nil)

	keyA := "10.0.0.10|/redfish/v1/UpdateService/FirmwareInventory/A"
	keyB := "10.0.0.10|/redfish/v1/UpdateService/FirmwareInventory/B"
	desiredJobs := []desiredCampaignJob{
		buildTestDesiredCampaignJob(keyB, "10.0.0.10", "job-b"),
		buildTestDesiredCampaignJob(keyA, "10.0.0.10", "job-a"),
	}

	jobsByKey := make(map[string]*v1.FirmwareUpdateJob)
	createdAny, err := reconciler.reconcileDesiredCampaignJobs(ctx, desiredJobs, jobsByKey)
	if err != nil {
		t.Fatalf("first reconcileDesiredCampaignJobs failed: %v", err)
	}
	if !createdAny {
		t.Fatalf("expected first reconcile pass to create a child job")
	}
	if len(jobsByKey) != 1 {
		t.Fatalf("expected only one child job to be created in first pass, got %d", len(jobsByKey))
	}
	firstJob, exists := jobsByKey[keyA]
	if !exists {
		t.Fatalf("expected deterministic ordering to create key %q first", keyA)
	}
	if firstJob.Metadata.UID == "" {
		t.Fatalf("expected created job to have generated UID")
	}
	if _, exists := jobsByKey[keyB]; exists {
		t.Fatalf("did not expect second child job for the same target to be created in first pass")
	}

	storedFirstPass, err := client.List(ctx, "FirmwareUpdateJob")
	if err != nil {
		t.Fatalf("list FirmwareUpdateJob after first pass failed: %v", err)
	}
	if len(storedFirstPass) != 1 {
		t.Fatalf("expected 1 stored child job after first pass, got %d", len(storedFirstPass))
	}

	firstJob.Status.JobState = v1.CampaignStateCompleted
	if err := client.Update(ctx, firstJob); err != nil {
		t.Fatalf("update first child job status failed: %v", err)
	}

	createdAny, err = reconciler.reconcileDesiredCampaignJobs(ctx, desiredJobs, jobsByKey)
	if err != nil {
		t.Fatalf("second reconcileDesiredCampaignJobs failed: %v", err)
	}
	if !createdAny {
		t.Fatalf("expected second reconcile pass to create next child job")
	}
	if len(jobsByKey) != 2 {
		t.Fatalf("expected both child jobs to exist after second pass, got %d", len(jobsByKey))
	}
	secondJob, exists := jobsByKey[keyB]
	if !exists {
		t.Fatalf("expected second child key %q to be created after first job completed", keyB)
	}
	if secondJob.Metadata.UID == "" {
		t.Fatalf("expected second created job to have generated UID")
	}

	storedSecondPass, err := client.List(ctx, "FirmwareUpdateJob")
	if err != nil {
		t.Fatalf("list FirmwareUpdateJob after second pass failed: %v", err)
	}
	if len(storedSecondPass) != 2 {
		t.Fatalf("expected 2 stored child jobs after second pass, got %d", len(storedSecondPass))
	}
}

func buildTestDesiredCampaignJob(key, targetAddress, name string) desiredCampaignJob {
	job := &v1.FirmwareUpdateJob{
		APIVersion: "hardware.fabrica.dev/v1",
		Kind:       "FirmwareUpdateJob",
		Metadata: fabrica.Metadata{
			Name: name,
			Annotations: map[string]string{
				v1.CampaignTargetAnnotation: targetAddress,
				v1.CampaignChildKeyAnnotation: key,
			},
		},
		Spec: v1.FirmwareUpdateJobSpec{
			TargetAddress:      targetAddress,
			SecretID:           "test-secret",
			ServerProxyAddress: "127.0.0.1",
		},
	}

	return desiredCampaignJob{key: key, job: job}
}

func setupReconcilerTestStorageClient(t *testing.T) (*storage.StorageClient, func()) {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name())
	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite3 test database: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	entClient := ent.NewClient(ent.Driver(entsql.OpenDB("sqlite3", sqlDB)))
	if err := entClient.Schema.Create(context.Background(), migrate.WithDropIndex(true), migrate.WithDropColumn(true)); err != nil {
		t.Fatalf("create test schema: %v", err)
	}

	storage.SetEntClient(entClient)
	client := storage.NewStorageClient()

	cleanup := func() {
		_ = entClient.Close()
		_ = sqlDB.Close()
	}

	return client, cleanup
}
