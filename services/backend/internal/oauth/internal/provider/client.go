// Package provider is the network adapter for the oauth module: it implements
// service.ProviderClient by talking to a provider's OAuth token endpoint and
// account-info endpoint over HTTP. It is the only piece of the module that makes
// outbound calls, which is why the service depends on it through an interface
// and tests substitute a fake.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/getnyx/influaudit/backend/internal/oauth/internal/service"
)

// maxResponseBytes bounds how much of a provider response is read, so a
// misbehaving or hostile endpoint cannot exhaust memory.
const maxResponseBytes = 1 << 20 // 1 MiB

// defaultTimeout bounds a single provider HTTP call when the caller does not
// supply its own client.
const defaultTimeout = 15 * time.Second

// Client exchanges authorization codes and resolves account ids against live
// provider endpoints.
type Client struct {
	http *http.Client
}

var _ service.ProviderClient = (*Client)(nil)

// New builds a Client. A nil http client is replaced with one carrying a
// conservative timeout so a hung provider cannot block a request indefinitely.
func New(hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{http: hc}
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

// Exchange swaps the authorization code for tokens under PKCE, then resolves the
// provider's stable account identifier for the issued access token.
func (c *Client) Exchange(ctx context.Context, req service.ExchangeRequest) (service.ExchangeResult, error) {
	tok, err := c.postToken(ctx, req)
	if err != nil {
		return service.ExchangeResult{}, err
	}
	if tok.AccessToken == "" {
		return service.ExchangeResult{}, fmt.Errorf("token endpoint returned no access token")
	}

	accountID, err := c.resolveAccountID(ctx, req.Provider, req.AccountInfoURL, tok.AccessToken)
	if err != nil {
		return service.ExchangeResult{}, err
	}

	var expiry time.Time
	if tok.ExpiresIn > 0 {
		expiry = time.Now().UTC().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}

	return service.ExchangeResult{
		AccessToken:       tok.AccessToken,
		RefreshToken:      tok.RefreshToken,
		Expiry:            expiry,
		Scopes:            strings.Fields(tok.Scope),
		ProviderAccountID: accountID,
	}, nil
}

func (c *Client) postToken(ctx context.Context, req service.ExchangeRequest) (tokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", req.Code)
	form.Set("redirect_uri", req.RedirectURI)
	form.Set("client_id", req.ClientID)
	form.Set("client_secret", req.ClientSecret)
	form.Set("code_verifier", req.CodeVerifier)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, fmt.Errorf("build token request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body := io.LimitReader(resp.Body, maxResponseBytes)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain a bounded amount so the connection can be reused, but do not
		// surface the provider's error body.
		_, _ = io.Copy(io.Discard, body)
		return tokenResponse{}, fmt.Errorf("token endpoint returned status %d", resp.StatusCode)
	}

	var tok tokenResponse
	if err := json.NewDecoder(body).Decode(&tok); err != nil {
		return tokenResponse{}, fmt.Errorf("decode token response: %w", err)
	}
	return tok, nil
}

func (c *Client) resolveAccountID(ctx context.Context, provider, accountInfoURL, accessToken string) (string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, accountInfoURL, nil)
	if err != nil {
		return "", fmt.Errorf("build account request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+accessToken)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("account request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body := io.LimitReader(resp.Body, maxResponseBytes)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, body)
		return "", fmt.Errorf("account endpoint returned status %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(body)
	if err != nil {
		return "", fmt.Errorf("read account response: %w", err)
	}
	return parseAccountID(provider, raw)
}

// parseAccountID extracts the stable account identifier from a provider's
// account-info response. The response shape differs per provider, so parsing is
// selected by provider name.
func parseAccountID(provider string, body []byte) (string, error) {
	switch provider {
	case service.ProviderGoogle:
		var yt struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		if err := json.Unmarshal(body, &yt); err != nil {
			return "", fmt.Errorf("decode youtube account: %w", err)
		}
		if len(yt.Items) == 0 || yt.Items[0].ID == "" {
			return "", fmt.Errorf("youtube account response contained no channel id")
		}
		return yt.Items[0].ID, nil
	case service.ProviderMeta:
		var me struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &me); err != nil {
			return "", fmt.Errorf("decode meta account: %w", err)
		}
		if me.ID == "" {
			return "", fmt.Errorf("meta account response contained no id")
		}
		return me.ID, nil
	default:
		return "", fmt.Errorf("no account resolver for provider %q", provider)
	}
}
