package fetcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	// tokenCacheKey is the Redis key used to share the client-credentials
	// token across multiple server instances.
	tokenCacheKey = "osu:fetcher:token"

	// tokenSafetyMargin is subtracted from the server-reported expiry so we
	// refresh slightly before the token actually expires.
	tokenSafetyMargin = 60 * time.Second

	// httpTimeout caps each individual osu! API request.
	httpTimeout = 15 * time.Second
)

// APIClientConfig holds the credentials needed to talk to the osu! API v2.
type APIClientConfig struct {
	ClientID     string
	ClientSecret string
	APIBase      string
}

// APIClient makes authenticated requests to the osu! API v2 using the
// client-credentials grant. The token is cached both in-memory (with a
// mutex to prevent thundering-herd refreshes) and in Redis (so multiple
// instances share a single token when possible).
type APIClient struct {
	cfg    APIClientConfig
	rdb    *redis.Client
	http   *http.Client
	logger *zap.Logger

	// In-memory token cache — guarded by mu.
	mu      sync.Mutex
	token   string
	expires time.Time
}

// NewAPIClient creates a new osu! API v2 client.
func NewAPIClient(cfg APIClientConfig, rdb *redis.Client, logger *zap.Logger) *APIClient {
	return &APIClient{
		cfg:    cfg,
		rdb:    rdb,
		http:   &http.Client{Timeout: httpTimeout},
		logger: logger,
	}
}

// --- osu! API response DTOs ---

// OsuUserResponse mirrors the relevant fields from GET /api/v2/users/{id}.
type OsuUserResponse struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url"`
	Country   struct {
		Code string `json:"code"`
	} `json:"country"`
	Statistics struct {
		GlobalRank int64   `json:"global_rank"`
		PP         float32 `json:"pp"`
	} `json:"statistics"`
}

// OsuBeatmapResponse mirrors the relevant fields from GET /api/v2/beatmaps/{id}.
type OsuBeatmapResponse struct {
	ID               int64   `json:"id"`
	BeatmapsetID     int64   `json:"beatmapset_id"`
	Status           string  `json:"status"`
	ModeInt          int     `json:"mode_int"`
	DifficultyRating float64 `json:"difficulty_rating"`
	Version          string  `json:"version"`
	TotalLength      int     `json:"total_length"`
	UserID           int64   `json:"user_id"`
	BPM              float64 `json:"bpm"`
	CS               float64 `json:"cs"`
	AR               float64 `json:"ar"`
	Drain            float64 `json:"drain"`
	Accuracy         float64 `json:"accuracy"`
	Beatmapset       struct {
		ID     int64  `json:"id"`
		Title  string `json:"title"`
		Artist string `json:"artist"`
		Covers struct {
			Cover   string `json:"cover"`
			Cover2x string `json:"cover@2x"`
		} `json:"covers"`
	} `json:"beatmapset"`
}

// --- Token management ---

// tokenResponse mirrors the response from POST /oauth/token (client-credentials).
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// getToken returns a valid access token, refreshing it if necessary.
func (c *APIClient) getToken(ctx context.Context) (string, error) {
	// Fast path: in-memory cache is still valid.
	c.mu.Lock()
	if c.token != "" && time.Now().Before(c.expires) {
		// Use a local variable to store token before releasing the lock.
		tok := c.token
		c.mu.Unlock()
		return tok, nil
	}
	c.mu.Unlock()

	// Try Redis (shared across instances).
	if tok, err := c.rdb.Get(ctx, tokenCacheKey).Result(); err == nil && tok != "" {
		// Hydrate in-memory cache. We don't know the exact expiry from Redis
		// alone, but the key has a TTL so it will eventually be evicted.
		// Set a conservative in-memory expiry.
		ttl, _ := c.rdb.TTL(ctx, tokenCacheKey).Result()
		if ttl > tokenSafetyMargin {
			c.mu.Lock()
			c.token = tok
			c.expires = time.Now().Add(ttl - tokenSafetyMargin)
			c.mu.Unlock()
			c.logger.Debug("token cache hit (redis)", zap.Duration("ttl", ttl))
			return tok, nil
		}
	}

	// Slow path: request a new token from osu!.
	c.logger.Info("token cache miss, fetching new token from osu! API")
	return c.refreshToken(ctx)
}

// refreshToken requests a new client-credentials token and caches it.
func (c *APIClient) refreshToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring the lock — another goroutine may have
	// refreshed while we were waiting.
	if c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}

	body := map[string]string{
		"client_id":     c.cfg.ClientID,
		"client_secret": c.cfg.ClientSecret,
		"grant_type":    "client_credentials",
		"scope":         "public",
	}
	payload, _ := json.Marshal(body)

	url := fmt.Sprintf("%s/oauth/token", c.cfg.APIBase)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		c.logger.Error("failed to request osu! token", zap.Error(err))
		return "", fmt.Errorf("request token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		c.logger.Error("osu! token endpoint returned non-OK status",
			zap.Int("status", resp.StatusCode),
			zap.String("body", string(raw)),
		)
		return "", fmt.Errorf("osu! token endpoint returned %d: %s", resp.StatusCode, string(raw))
	}

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	if tr.AccessToken == "" {
		return "", fmt.Errorf("osu! token response missing access_token")
	}

	// Cache in-memory.
	ttl := time.Duration(tr.ExpiresIn) * time.Second
	c.token = tr.AccessToken
	c.expires = time.Now().Add(ttl - tokenSafetyMargin)

	// Cache in Redis for other instances.
	redisTTL := ttl - tokenSafetyMargin
	if redisTTL > 0 {
		if err := c.rdb.Set(ctx, tokenCacheKey, tr.AccessToken, redisTTL).Err(); err != nil {
			c.logger.Warn("failed to cache osu! token in redis", zap.Error(err))
		}
	}

	c.logger.Debug("refreshed osu! api token", zap.Duration("ttl", ttl))
	return c.token, nil
}

// --- API methods ---

// GetUser fetches a user from GET /api/v2/users/{id}.
func (c *APIClient) GetUser(ctx context.Context, osuID int64) (*OsuUserResponse, error) {
	var user OsuUserResponse
	url := fmt.Sprintf("%s/api/v2/users/%s/osu", c.cfg.APIBase, strconv.FormatInt(osuID, 10))
	if err := c.do(ctx, http.MethodGet, url, nil, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// GetBeatmap fetches a beatmap from GET /api/v2/beatmaps/{id}.
func (c *APIClient) GetBeatmap(ctx context.Context, osuID int64) (*OsuBeatmapResponse, error) {
	var bm OsuBeatmapResponse
	url := fmt.Sprintf("%s/api/v2/beatmaps/%s", c.cfg.APIBase, strconv.FormatInt(osuID, 10))
	if err := c.do(ctx, http.MethodGet, url, nil, &bm); err != nil {
		return nil, err
	}
	return &bm, nil
}

// do executes an authenticated request against the osu! API.
func (c *APIClient) do(ctx context.Context, method, url string, body []byte, out any) error {
	tok, err := c.getToken(ctx)
	if err != nil {
		return fmt.Errorf("get token: %w", err)
	}

	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	c.logger.Debug("osu! API call", zap.String("method", method), zap.String("url", url))

	resp, err := c.http.Do(req)
	if err != nil {
		c.logger.Error("osu! API request failed", zap.String("url", url), zap.Error(err))
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		// Token may have been revoked — invalidate and retry once.
		c.logger.Warn("osu! API returned 401, refreshing token and retrying", zap.String("url", url))
		c.mu.Lock()
		c.token = ""
		c.expires = time.Time{}
		c.mu.Unlock()
		_ = c.rdb.Del(ctx, tokenCacheKey).Err()

		// Single retry.
		tok, err = c.getToken(ctx)
		if err != nil {
			return fmt.Errorf("refresh token after 401: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err = c.http.Do(req)
		if err != nil {
			c.logger.Error("osu! API retry request failed", zap.String("url", url), zap.Error(err))
			return fmt.Errorf("retry request: %w", err)
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode == http.StatusNotFound {
		c.logger.Debug("osu! API resource not found", zap.String("url", url))
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		c.logger.Error("osu! API returned non-OK status",
			zap.String("url", url),
			zap.Int("status", resp.StatusCode),
			zap.String("body", string(raw)),
		)
		return fmt.Errorf("osu! api returned %d: %s", resp.StatusCode, string(raw))
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		c.logger.Error("failed to decode osu! API response", zap.String("url", url), zap.Error(err))
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
