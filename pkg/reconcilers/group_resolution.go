// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT
// This file contains user-customizable group-selection logic for
// FirmwareUpdateJob. It is safe to edit; it is NOT overwritten by code
// generation.
package reconcilers

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/user/firmware-updater/internal/smd"
)

// memberTarget is a single resolved BMC target within a group job.
type memberTarget struct {
	// Xname is the (first) member component xname that resolved to this BMC.
	Xname string
	// BMCXname is the controlling BMC xname (for logging/dedup).
	BMCXname string
	// BMCFQDN is the Redfish dispatch address for this BMC.
	BMCFQDN string
}

// smdResolver is the subset of the SMD client used for group resolution. It is
// an interface so the resolution logic can be unit-tested with a fake.
type smdResolver interface {
	GroupMembers(ctx context.Context, groupRef string) ([]string, error)
	ResolveMemberBMC(ctx context.Context, xname string) (fqdn string, bmcXname string, err error)
}

// groupResolution captures the outcome of resolving a group into BMC targets.
type groupResolution struct {
	// Targets are the de-duplicated BMC dispatch targets.
	Targets []memberTarget
	// Unresolvable are member xnames with no usable ComponentEndpoint.
	Unresolvable []string
	// TotalMembers is the raw member count reported by SMD.
	TotalMembers int
}

// Detail returns a human-readable resolution summary for the job status.
func (g groupResolution) Detail() string {
	return fmt.Sprintf("resolved %d BMC(s) from %d member(s); %d unresolvable",
		len(g.Targets), g.TotalMembers, len(g.Unresolvable))
}

// resolveGroupTargets fetches the group's members and resolves each to its
// controlling BMC. Members without a ComponentEndpoint are recorded as
// unresolvable (not an error). Any transport/terminal error from SMD is
// returned so the caller can classify and retry or fail.
//
// Resolution is type-agnostic: every member is resolved via ComponentEndpoint
// regardless of its component type; members that lack a Redfish endpoint are
// treated as unresolvable. Results are de-duplicated by BMC FQDN so a BMC that
// backs multiple member nodes is dispatched to only once.
func resolveGroupTargets(ctx context.Context, resolver smdResolver, groupRef string) (groupResolution, error) {
	members, err := resolver.GroupMembers(ctx, groupRef)
	if err != nil {
		return groupResolution{}, err
	}

	res := groupResolution{TotalMembers: len(members)}
	seen := make(map[string]struct{})

	for _, xname := range members {
		fqdn, bmcXname, err := resolver.ResolveMemberBMC(ctx, xname)
		if errors.Is(err, smd.ErrEndpointNotFound) {
			res.Unresolvable = append(res.Unresolvable, xname)
			continue
		}
		if err != nil {
			return groupResolution{}, err
		}
		if _, dup := seen[fqdn]; dup {
			continue
		}
		seen[fqdn] = struct{}{}
		res.Targets = append(res.Targets, memberTarget{Xname: xname, BMCXname: bmcXname, BMCFQDN: fqdn})
	}

	return res, nil
}

// fanOutResult aggregates fan-out dispatch outcomes.
type fanOutResult struct {
	// Completed is the number of members dispatched successfully.
	Completed int
	// Failed lists the BMC FQDNs that failed to dispatch, along with the error.
	Failed []string
	// FirstErr is a representative error from a failed member (nil if all ok).
	FirstErr error
}

// fanOutDispatch invokes dispatch for each member, bounding concurrency to
// maxParallel (values < 1 are treated as 1 = serial). It returns the aggregate
// result. Ordering of Failed is non-deterministic across concurrent members.
func fanOutDispatch(ctx context.Context, members []memberTarget, maxParallel int, dispatch func(ctx context.Context, m memberTarget) error) fanOutResult {
	if maxParallel < 1 {
		maxParallel = 1
	}

	var (
		mu     sync.Mutex
		result fanOutResult
		wg     sync.WaitGroup
	)
	sem := make(chan struct{}, maxParallel)

	for _, m := range members {
		wg.Add(1)
		go func(m memberTarget) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			err := dispatch(ctx, m)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.Failed = append(result.Failed, m.BMCFQDN)
				if result.FirstErr == nil {
					result.FirstErr = fmt.Errorf("member %s (%s): %w", m.Xname, m.BMCFQDN, err)
				}
				return
			}
			result.Completed++
		}(m)
	}

	wg.Wait()
	return result
}
