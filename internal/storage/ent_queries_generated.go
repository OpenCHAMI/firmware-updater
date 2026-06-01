package storage

import (
	"context"
	"fmt"

	"firmware-manager/internal/storage/ent"
	"firmware-manager/internal/storage/ent/label"
	entresource "firmware-manager/internal/storage/ent/resource"

	v1 "firmware-manager/apis/hardware.fabrica.dev/v1"
)

// ensureEntClient verifies the ent client has been initialized
func ensureEntClient() {
	if entClient == nil {
		panic("ent client not initialized: call SetEntClient in main.go before using storage")
	}
}

// QueryResources returns a generic query builder for a given kind
func QueryResources(ctx context.Context, kind string) *ent.ResourceQuery {
	ensureEntClient()
	return entClient.Resource.Query().
		Where(entresource.KindEQ(kind))
}

// QueryResourcesByLabels queries resources by kind and exact-match labels
func QueryResourcesByLabels(ctx context.Context, kind string, labels map[string]string) (*ent.ResourceQuery, error) {
	ensureEntClient()
	q := entClient.Resource.Query().Where(entresource.KindEQ(kind))
	for k, v := range labels {
		q = q.Where(entresource.HasLabelsWith(
			label.KeyEQ(k),
			label.ValueEQ(v),
		))
	}
	return q, nil
}

// Queryfirmwarebundles returns a query builder for firmwarebundles
func Queryfirmwarebundles(ctx context.Context) *ent.ResourceQuery {
	return QueryResources(ctx, "FirmwareBundle")
}

// GetFirmwareBundleByUID loads a single FirmwareBundle by UID
func GetFirmwareBundleByUID(ctx context.Context, uid string) (*v1.FirmwareBundle, error) {
	ensureEntClient()
	r, err := entClient.Resource.Query().
		Where(entresource.UIDEQ(uid), entresource.KindEQ("FirmwareBundle")).
		WithLabels().
		WithAnnotations().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load FirmwareBundle %s: %w", uid, err)
	}
	v, err := FromEntResource(ctx, r)
	if err != nil {
		return nil, err
	}
	return v.(*v1.FirmwareBundle), nil
}

// ListfirmwarebundlesByLabels returns firmwarebundles matching all provided labels
func ListfirmwarebundlesByLabels(ctx context.Context, labels map[string]string) ([]*v1.FirmwareBundle, error) {
	q, err := QueryResourcesByLabels(ctx, "FirmwareBundle", labels)
	if err != nil {
		return nil, err
	}
	rs, err := q.WithLabels().WithAnnotations().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*v1.FirmwareBundle, 0, len(rs))
	for _, r := range rs {
		v, err := FromEntResource(ctx, r)
		if err != nil {
			continue
		}
		out = append(out, v.(*v1.FirmwareBundle))
	}
	return out, nil
}

// Queryfirmwareupdatejobs returns a query builder for firmwareupdatejobs
func Queryfirmwareupdatejobs(ctx context.Context) *ent.ResourceQuery {
	return QueryResources(ctx, "FirmwareUpdateJob")
}

// GetFirmwareUpdateJobByUID loads a single FirmwareUpdateJob by UID
func GetFirmwareUpdateJobByUID(ctx context.Context, uid string) (*v1.FirmwareUpdateJob, error) {
	ensureEntClient()
	r, err := entClient.Resource.Query().
		Where(entresource.UIDEQ(uid), entresource.KindEQ("FirmwareUpdateJob")).
		WithLabels().
		WithAnnotations().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load FirmwareUpdateJob %s: %w", uid, err)
	}
	v, err := FromEntResource(ctx, r)
	if err != nil {
		return nil, err
	}
	return v.(*v1.FirmwareUpdateJob), nil
}

// ListfirmwareupdatejobsByLabels returns firmwareupdatejobs matching all provided labels
func ListfirmwareupdatejobsByLabels(ctx context.Context, labels map[string]string) ([]*v1.FirmwareUpdateJob, error) {
	q, err := QueryResourcesByLabels(ctx, "FirmwareUpdateJob", labels)
	if err != nil {
		return nil, err
	}
	rs, err := q.WithLabels().WithAnnotations().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*v1.FirmwareUpdateJob, 0, len(rs))
	for _, r := range rs {
		v, err := FromEntResource(ctx, r)
		if err != nil {
			continue
		}
		out = append(out, v.(*v1.FirmwareUpdateJob))
	}
	return out, nil
}
