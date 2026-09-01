package handler

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"rctHubBackend/internal/fetcher"
	"rctHubBackend/internal/middleware"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/response"
)

// maxBulkIDs caps the number of osu! IDs accepted per bulk request. osu! API
// lookups are single-request per ID, so an unbounded batch would hammer the
// upstream API and trigger its rate limits.
const maxBulkIDs = 50

// BulkResult is the per-ID outcome of a bulk add operation. Successful rows
// carry the stored document id and a human-readable label; failed rows carry
// an error message.
type BulkResult struct {
	OsuID  int64  `json:"osu_id"`
	OK     bool   `json:"ok"`
	ID     string `json:"id,omitempty"`
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

// BulkReport is the aggregate response body for a bulk add request.
type BulkReport struct {
	Total     int          `json:"total"`
	Succeeded int          `json:"succeeded"`
	Failed    int          `json:"failed"`
	Results   []BulkResult `json:"results"`
}

// BulkHandler performs bulk fetch-and-store operations for users and beatmaps.
type BulkHandler struct {
	fetcher fetcher.Fetcher
	log     *zap.Logger
}

// NewBulkHandler creates a BulkHandler.
func NewBulkHandler(f fetcher.Fetcher, log *zap.Logger) *BulkHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &BulkHandler{fetcher: f, log: log}
}

// parseOsuIDs decodes and validates the {osu_ids: [...]} request body. It
// rejects empty payloads and batches larger than maxBulkIDs, and de-duplicates
// the ids while preserving order.
func parseOsuIDs(c *gin.Context) ([]int64, bool) {
	var req struct {
		OsuIDs []int64 `json:"osu_ids" binding:"required,min=1"`
	}
	if err := bindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return nil, false
	}
	if len(req.OsuIDs) > maxBulkIDs {
		response.BadRequest(c, "too many ids: maximum "+strconv.Itoa(maxBulkIDs)+" per request")
		return nil, false
	}
	seen := make(map[int64]struct{}, len(req.OsuIDs))
	ids := make([]int64, 0, len(req.OsuIDs))
	for _, id := range req.OsuIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, true
}

// BulkCreateUsers fetches and stores each osu! user id, returning a per-id report.
func (h *BulkHandler) BulkCreateUsers(c *gin.Context) {
	ids, ok := parseOsuIDs(c)
	if !ok {
		return
	}

	results := make([]BulkResult, 0, len(ids))
	for _, osuID := range ids {
		user, err := h.fetcher.GetUser(c.Request.Context(), osuID)
		if err != nil {
			results = append(results, BulkResult{OsuID: osuID, OK: false, Error: bulkError(err)})
			continue
		}
		results = append(results, BulkResult{
			OsuID:  osuID,
			OK:     true,
			ID:     user.ID.Hex(),
			Detail: user.Username,
		})
	}

	claims, _ := middleware.ClaimsFromContext(c)
	h.log.Info("audit: bulk users added", zap.Int64("caller_osu_id", claims.OsuID), zap.Int("count", len(ids)))
	response.JSON(c, summarizeBulk(results))
}

// BulkCreateBeatmaps fetches and stores each osu! beatmap id, returning a per-id report.
func (h *BulkHandler) BulkCreateBeatmaps(c *gin.Context) {
	ids, ok := parseOsuIDs(c)
	if !ok {
		return
	}

	results := make([]BulkResult, 0, len(ids))
	for _, osuID := range ids {
		bm, err := h.fetcher.GetBeatmap(c.Request.Context(), osuID)
		if err != nil {
			results = append(results, BulkResult{OsuID: osuID, OK: false, Error: bulkError(err)})
			continue
		}
		results = append(results, BulkResult{
			OsuID:  osuID,
			OK:     true,
			ID:     bm.ID.Hex(),
			Detail: bm.Title,
		})
	}

	claims, _ := middleware.ClaimsFromContext(c)
	h.log.Info("audit: bulk beatmaps added", zap.Int64("caller_osu_id", claims.OsuID), zap.Int("count", len(ids)))
	response.JSON(c, summarizeBulk(results))
}

// bulkError maps a fetch error onto a concise public message. An upstream 404
// (either fetcher.ErrNotFound or the wrapped errs.ErrNotFound) becomes a
// friendly "not found"; everything else keeps its underlying message.
func bulkError(err error) string {
	if errors.Is(err, errs.ErrNotFound) || errors.Is(err, fetcher.ErrNotFound) {
		return "not found"
	}
	return err.Error()
}

// summarizeBulk folds per-id results into an aggregate report.
func summarizeBulk(results []BulkResult) BulkReport {
	report := BulkReport{Total: len(results), Results: results}
	for _, r := range results {
		if r.OK {
			report.Succeeded++
		} else {
			report.Failed++
		}
	}
	return report
}
