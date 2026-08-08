package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
)

// Config wraps the osu! OAuth2 endpoints and client credentials.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	APIBase      string
}

// Client handles osu! OAuth2 authentication.
type Client struct {
	cfg    Config
	oauth2 *oauth2.Config
	rdb    *redis.Client
	log    *zap.Logger
}

// OAuthClient abstracts the osu! OAuth operations used by the auth service.
type OAuthClient interface {
	AuthURL(ctx context.Context) (string, error)
	Exchange(ctx context.Context, code, state string) (*oauth2.Token, error)
	Me(ctx context.Context, token *oauth2.Token) (*OsuUser, error)
}

// Ensure Client implements OAuthClient.
var _ OAuthClient = (*Client)(nil)

// OsuUser represents the response from /api/v2/me.
type OsuUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url"`
	Country   struct {
		Code string `json:"code"`
	} `json:"country"`
}

// NewClient creates a new osu! OAuth client.
func NewClient(cfg Config, rdb *redis.Client, log *zap.Logger) *Client {
	if log == nil {
		log = zap.NewNop()
	}
	endpoint := oauth2.Endpoint{
		AuthURL:  fmt.Sprintf("%s/oauth/authorize", cfg.APIBase),
		TokenURL: fmt.Sprintf("%s/oauth/token", cfg.APIBase),
	}
	return &Client{
		cfg: cfg,
		oauth2: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURI,
			Endpoint:     endpoint,
			Scopes:       []string{"identify", "public", "chat.read", "chat.write", "chat.write_manage"},
		},
		rdb: rdb,
		log: log,
	}
}

// AuthURL returns the osu! authorization URL and stores a CSRF state token in Redis.
func (c *Client) AuthURL(ctx context.Context) (string, error) {
	state, err := randomState()
	if err != nil {
		c.log.Error("failed to generate OAuth state", zap.Error(err))
		return "", fmt.Errorf("generate state: %w", err)
	}
	if err := c.rdb.Set(ctx, stateKey(state), "1", 5*time.Minute).Err(); err != nil {
		c.log.Error("failed to store OAuth state in Redis", zap.Error(err))
		return "", fmt.Errorf("store state: %w", err)
	}
	c.log.Debug("generated OAuth authorization URL", zap.String("state_prefix", state[:8]))
	return c.oauth2.AuthCodeURL(state, oauth2.AccessTypeOnline), nil
}

// Exchange verifies the state token and exchanges the authorization code for an access token.
func (c *Client) Exchange(ctx context.Context, code, state string) (*oauth2.Token, error) {
	if err := c.verifyState(ctx, state); err != nil {
		c.log.Warn("OAuth state verification failed", zap.Error(err))
		return nil, err
	}
	token, err := c.oauth2.Exchange(ctx, code)
	if err != nil {
		c.log.Error("OAuth code exchange failed", zap.Error(err))
		return nil, err
	}
	c.log.Info("OAuth token exchanged successfully",
		zap.String("token_type", token.TokenType),
		zap.Time("expiry", token.Expiry),
	)
	return token, nil
}

// Me fetches the authenticated osu! user's profile.
func (c *Client) Me(ctx context.Context, token *oauth2.Token) (*OsuUser, error) {
	client := c.oauth2.Client(ctx, token)
	url := fmt.Sprintf("%s/api/v2/me", c.cfg.APIBase)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		c.log.Error("failed to create /me request", zap.Error(err))
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		c.log.Error("failed to fetch osu! user profile", zap.Error(err))
		return nil, fmt.Errorf("fetch me: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		c.log.Error("osu! /me API returned non-OK status",
			zap.Int("status", resp.StatusCode),
			zap.String("body", string(body)),
		)
		return nil, fmt.Errorf("osu! api returned %d: %s", resp.StatusCode, string(body))
	}

	var user OsuUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		c.log.Error("failed to decode osu! user profile", zap.Error(err))
		return nil, fmt.Errorf("decode user: %w", err)
	}
	c.log.Info("fetched osu! user profile",
		zap.Int64("osu_id", user.ID),
		zap.String("username", user.Username),
	)
	return &user, nil
}

func (c *Client) verifyState(ctx context.Context, state string) error {
	if state == "" {
		return fmt.Errorf("missing state")
	}
	exists, err := c.rdb.Exists(ctx, stateKey(state)).Result()
	if err != nil {
		c.log.Error("failed to verify OAuth state in Redis", zap.Error(err))
		return fmt.Errorf("verify state: %w", err)
	}
	if exists == 0 {
		c.log.Warn("OAuth state not found or expired", zap.String("state_prefix", state[:min(8, len(state))]))
		return fmt.Errorf("invalid or expired state")
	}
	_ = c.rdb.Del(ctx, stateKey(state))
	return nil
}

func stateKey(state string) string {
	return fmt.Sprintf("osu:oauth:state:%s", state)
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
