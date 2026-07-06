package reconcilers
// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package reconcilers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/user/firmware-updater/internal/smd"
	"github.com/user/firmware-updater/pkg/firmwareproxy"
)

// fakeResolver implements smdResolver for tests.
type fakeResolver struct {
	members     []string
	membersErr  error
	endpoints   map[string]string // xname -> BMC FQDN ("" or missing => not found)
	endpointErr map[string]error  // xname -> hard error to return
}

func (f *fakeResolver) GroupMembers(_ context.Context, _ string) ([]string, error) {
	if f.membersErr != nil {
		return nil, f.membersErr
	}
	return f.members, nil
}

func (f *fakeResolver) ResolveMemberBMC(_ context.Context, xname string) (string, string, error) {
	if err, ok := f.endpointErr[xname]; ok {
		return "", "", err
	}
	fqdn, ok := f.endpoints[xname]
	if !ok || fqdn == "" {
		return "", "", smd.ErrEndpointNotFound
	}
	return fqdn, "bmc-" + xname, nil
}

func TestResolveGroupTargets_HappyPath(t *testing.T) {
	r := &fakeResolver{
		members: []string{"x0c0s0b0n0", "x0c0s1b0n0", "x0c0s2b0n0"},
		endpoints: map[string]string{
			"x0c0s0b0n0": "bmc0.example.com",
			"x0c0s1b0n0": "bmc1.example.com",
			"x0c0s2b0n0": "bmc2.example.com",
		},
	}
	res, err := resolveGroupTargets(context.Background(), r, "grp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Targets) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(res.Targets))
	}
	if res.TotalMembers != 3 || len(res.Unresolvable) != 0 {
		t.Fatalf("unexpected resolution: %+v", res)
	}
}

func TestResolveGroupTargets_DeduplicatesByBMC(t *testing.T) {
	// Two node members share the same controlling BMC FQDN.
	r := &fakeResolver{
		members: []string{"x0c0s0b0n0", "x0c0s0b0n1"},
		endpoints: map[string]string{
			"x0c0s0b0n0": "bmc0.example.com",
			"x0c0s0b0n1": "bmc0.example.com",
		},
	}
	res, err := resolveGroupTargets(context.Background(), r, "grp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Targets) != 1 {
		t.Fatalf("expected 1 deduped BMC target, got %d", len(res.Targets))
	}
}

func TestResolveGroupTargets_UnresolvableRecorded(t *testing.T) {
	r := &fakeResolver{
		members: []string{"x0c0s0b0n0", "x0c0s1b0n0"},
		endpoints: map[string]string{
			"x0c0s0b0n0": "bmc0.example.com",
			// x0c0s1b0n0 missing -> ErrEndpointNotFound
		},
	}
	res, err := resolveGroupTargets(context.Background(), r, "grp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Targets) != 1 || len(res.Unresolvable) != 1 {
		t.Fatalf("expected 1 resolved + 1 unresolvable, got %+v", res)
	}
	if res.Unresolvable[0] != "x0c0s1b0n0" {
		t.Fatalf("unexpected unresolvable member: %v", res.Unresolvable)
	}
}

func TestResolveGroupTargets_GroupFetchError(t *testing.T) {
	wantErr := &firmwareproxy.HTTPStatusError{StatusCode: 404, Message: "No such group"}
	r := &fakeResolver{membersErr: wantErr}
	_, err := resolveGroupTargets(context.Background(), r, "grp")
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected group fetch error to propagate, got %v", err)
	}
}

func TestResolveGroupTargets_HardEndpointErrorPropagates(t *testing.T) {
	hard := &firmwareproxy.HTTPStatusError{StatusCode: 503, Message: "smd down"}
	r := &fakeResolver{
		members:     []string{"x0c0s0b0n0"},
		endpointErr: map[string]error{"x0c0s0b0n0": hard},
	}
	_, err := resolveGroupTargets(context.Background(), r, "grp")
	if !errors.Is(err, hard) {
		t.Fatalf("expected hard endpoint error to propagate, got %v", err)
	}
}

func TestFanOutDispatch_AllSucceed(t *testing.T) {
	members := makeMembers(6)
	result := fanOutDispatch(context.Background(), members, 3, func(_ context.Context, _ memberTarget) error {
		return nil
	})
	if result.Completed != 6 || len(result.Failed) != 0 {
		t.Fatalf("expected 6 completed, 0 failed, got %+v", result)
	}
}

func TestFanOutDispatch_SomeFail(t *testing.T) {
	members := makeMembers(4)
	result := fanOutDispatch(context.Background(), members, 2, func(_ context.Context, m memberTarget) error {
		if m.BMCFQDN == "bmc1" || m.BMCFQDN == "bmc3" {
			return fmt.Errorf("boom")
		}
		return nil
	})
	if result.Completed != 2 {
		t.Fatalf("expected 2 completed, got %d", result.Completed)
	}
	if len(result.Failed) != 2 {
		t.Fatalf("expected 2 failed, got %v", result.Failed)
	}
	if result.FirstErr == nil {
		t.Fatal("expected FirstErr to be set when a member fails")
	}
}

func TestFanOutDispatch_HonorsParallelismBound(t *testing.T) {
	const maxParallel = 3
	members := makeMembers(12)

	var (
		current int32
		peak    int32
		mu      sync.Mutex
	)
	result := fanOutDispatch(context.Background(), members, maxParallel, func(_ context.Context, _ memberTarget) error {
		n := atomic.AddInt32(&current, 1)
		mu.Lock()
		if n > peak {
			peak = n
		}
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		atomic.AddInt32(&current, -1)
		return nil
	})

	if result.Completed != 12 {
		t.Fatalf("expected 12 completed, got %d", result.Completed)
	}
	if peak > maxParallel {
		t.Fatalf("parallelism bound violated: peak concurrency %d > %d", peak, maxParallel)
	}
	if peak < 2 {
		t.Fatalf("expected some concurrency (peak >= 2), got %d", peak)
	}
}

func TestFanOutDispatch_ZeroMaxParallelIsSerial(t *testing.T) {
	members := makeMembers(4)
	var current, peak int32
	result := fanOutDispatch(context.Background(), members, 0, func(_ context.Context, _ memberTarget) error {
		n := atomic.AddInt32(&current, 1)
		if n > atomic.LoadInt32(&peak) {
			atomic.StoreInt32(&peak, n)
		}
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt32(&current, -1)
		return nil
	})
	if result.Completed != 4 {
		t.Fatalf("expected 4 completed, got %d", result.Completed)
	}
	if peak != 1 {
		t.Fatalf("expected serial execution (peak 1), got %d", peak)
	}
}

func makeMembers(n int) []memberTarget {
	members := make([]memberTarget, 0, n)
	for i := 0; i < n; i++ {
		members = append(members, memberTarget{
			Xname:    fmt.Sprintf("node%d", i),
			BMCXname: fmt.Sprintf("bmcx%d", i),
			BMCFQDN:  fmt.Sprintf("bmc%d", i),
		})
	}
	return members
}
