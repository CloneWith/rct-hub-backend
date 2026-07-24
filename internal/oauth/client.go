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
func NewClient(cfg Config, rdb *redis.Client) *Client {
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
	}
}

// AuthURL returns the osu! authorization URL and stores a CSRF state token in Redis.
func (c *Client) AuthURL(ctx context.Context) (string, error) {
	state, err := randomState()
	if err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	if err := c.rdb.Set(ctx, stateKey(state), "1", 5*time.Minute).Err(); err != nil {
		return "", fmt.Errorf("store state: %w", err)
	}
	return c.oauth2.AuthCodeURL(state, oauth2.AccessTypeOnline), nil
}

// Exchange verifies the state token and exchanges the authorization code for an access token.
func (c *Client) Exchange(ctx context.Context, code, state string) (*oauth2.Token, error) {
	if err := c.verifyState(ctx, state); err != nil {
		return nil, err
	}
	return c.oauth2.Exchange(ctx, code)
}

// Me fetches the authenticated osu! user's profile.
func (c *Client) Me(ctx context.Context, token *oauth2.Token) (*OsuUser, error) {
	client := c.oauth2.Client(ctx, token)
	url := fmt.Sprintf("%s/api/v2/me", c.cfg.APIBase)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch me: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("osu! api returned %d: %s", resp.StatusCode, string(body))
	}

	var user OsuUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decode user: %w", err)
	}
	return &user, nil
}

func (c *Client) verifyState(ctx context.Context, state string) error {
	if state == "" {
		return fmt.Errorf("missing state")
	}
	exists, err := c.rdb.Exists(ctx, stateKey(state)).Result()
	if err != nil {
		return fmt.Errorf("verify state: %w", err)
	}
	if exists == 0 {
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
