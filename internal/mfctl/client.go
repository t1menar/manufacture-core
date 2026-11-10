package mfctl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/t1menar/manufacture-core/internal/spec"
)

// Environment variables of mfctl.
const (
	EnvURL   = "MF_URL"
	EnvToken = "MF_ADMIN_TOKEN" //nolint:gosec // name of a variable, not a credential.
)

// DefaultURL is the gateway of a local core.
const DefaultURL = "http://127.0.0.1:8080"

type client struct {
	base  string
	token string
	http  *http.Client
}

// APIError is a problem+json response.
type APIError struct {
	Status int              `json:"status"`
	Code   string           `json:"code"`
	Title  string           `json:"title"`
	Detail string           `json:"detail"`
	Errors spec.Diagnostics `json:"errors"`
}

func (e *APIError) Error() string {
	msg := e.Detail
	if msg == "" {
		msg = e.Title
	}
	return fmt.Sprintf("%s (HTTP %d %s)", msg, e.Status, e.Code)
}

func (c *client) do(ctx context.Context, method, path, contentType string, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.base, "/")+path, rd)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach the core at %s (set %s): %w", c.base, EnvURL, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		e := &APIError{Status: resp.StatusCode}
		if json.Unmarshal(data, e) != nil || e.Code == "" {
			e.Code, e.Detail = "http_error", strings.TrimSpace(string(data))
		}
		if resp.StatusCode == http.StatusUnauthorized && c.token == "" {
			e.Detail += "; set " + EnvToken
		}
		return e
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if raw, ok := out.(*json.RawMessage); ok {
		*raw = data
		return nil
	}
	return json.Unmarshal(data, out)
}

func newClient(getenv func(string) string) *client {
	base := getenv(EnvURL)
	if base == "" {
		base = DefaultURL
	}
	return &client{base: base, token: getenv(EnvToken), http: &http.Client{Timeout: 60 * time.Second}}
}
