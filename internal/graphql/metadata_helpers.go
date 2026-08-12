package graphql

import (
	"context"
	"sync"

	"rctHubBackend/internal/beatmapmetadata"
)

type metadataRequestCache struct {
	mu      sync.Mutex
	records map[int64]metadataCacheEntry
}

type metadataCacheEntry struct {
	record beatmapmetadata.Record
	err    error
}

type metadataCacheKey struct{}

func withMetadataRequestCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, metadataCacheKey{}, &metadataRequestCache{records: make(map[int64]metadataCacheEntry)})
}

func (r *matchPoolSlotMetadataResolver) metadataRecord(ctx context.Context, obj *MatchPoolSlotMetadata) (*beatmapmetadata.Record, error) {
	if obj == nil || obj.BeatmapID == nil || *obj.BeatmapID == "" || r.metadata == nil {
		return nil, nil
	}
	id, err := parsePositiveInt64ID(*obj.BeatmapID)
	if err != nil {
		return nil, err
	}
	if cache, ok := ctx.Value(metadataCacheKey{}).(*metadataRequestCache); ok && cache != nil {
		cache.mu.Lock()
		entry, exists := cache.records[id]
		if !exists {
			entry.record, entry.err = r.metadata.State(ctx, id)
			cache.records[id] = entry
		}
		cache.mu.Unlock()
		if entry.err != nil {
			return nil, entry.err
		}
		return &entry.record, nil
	}
	record, err := r.metadata.State(ctx, id)
	if err != nil {
		return nil, err
	}
	return &record, nil
}
