package reconcilers

import (
	"testing"

	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
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
