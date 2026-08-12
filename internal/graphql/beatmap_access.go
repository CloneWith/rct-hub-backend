package graphql

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"rctHubBackend/pkg/errs"
)

func (r *Resolver) beatmapByID(ctx context.Context, beatmapID *string) (*Beatmap, error) {
	if beatmapID == nil {
		return nil, nil
	}
	id, err := parsePositiveInt64ID(*beatmapID)
	if err != nil {
		return nil, err
	}
	if loader := BeatmapLoaderFromCtx(ctx); loader != nil {
		beatmap, err := loader.Load(ctx, id)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) || errors.Is(err, errs.ErrNotFound) {
				return nil, nil
			}
			return nil, err
		}
		return mapBeatmap(beatmap), nil
	}
	if r == nil || r.beatmaps == nil {
		return nil, nil
	}
	beatmap, err := r.beatmaps.GetByOsuID(ctx, id)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) || errors.Is(err, errs.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return mapBeatmap(beatmap), nil
}

func (r *Resolver) persistedBeatmapMetadata(ctx context.Context, beatmapID *string) (*Beatmap, error) {
	if beatmapID == nil || r == nil || r.metadata == nil {
		return nil, nil
	}
	id, err := parsePositiveInt64ID(*beatmapID)
	if err != nil {
		return nil, err
	}
	beatmap, err := r.metadata.Beatmap(ctx, id)
	if err != nil {
		return nil, err
	}
	return mapBeatmap(beatmap), nil
}
