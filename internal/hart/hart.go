// Package hart publishes loop artifacts to a hart instance.
//
// The wire format is the one the fleets already use by hand:
//
//	POST {HART_URL}/v1/publish?owner=<owner>&artifact=<id>&visibility=<v>
//	     content-type: text/html
//	     X-Hart-Owner-Key: <key>
//	     <the html as the raw body>
//
// Note the auth header: hart's older /api/artifacts endpoint takes a bearer
// token instead, and one fleet still calls it that way. /v1/publish with
// X-Hart-Owner-Key is the form that is actually in service.
package hart

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DefaultURL is used when HART_URL is unset.
const DefaultURL = "https://hart.intrane.fr"

// Result is what a successful publish returns.
type Result struct {
	URL   string `json:"url"`
	Bytes int    `json:"bytes"`
	// Gated is set when a private artifact was verified to refuse anonymous reads.
	Gated bool `json:"gated,omitempty"`
}

// Config is resolved from the environment.
type Config struct {
	BaseURL  string
	OwnerKey string
	// ReadKey is sent alongside visibility=private so the artifact can be read
	// with a shared link. It is a credential, so it comes from the environment
	// rather than fleet.yml.
	ReadKey string
}

// FromEnv reads the hart settings. fleet-cli is a compiled binary, so it never
// picks up ~/.hart/config the way the hart CLI does — the values have to come
// from the environment, which for the units means /etc/default/fleet-cli.
func FromEnv() Config {
	base := strings.TrimSuffix(os.Getenv("HART_URL"), "/")
	if base == "" {
		base = DefaultURL
	}
	return Config{
		BaseURL:  base,
		OwnerKey: os.Getenv("HART_OWNER_KEY"),
		ReadKey:  os.Getenv("HART_READ_KEY"),
	}
}

// Publish uploads html as owner/artifact and returns the artifact URL.
func Publish(ctx context.Context, cfg Config, owner, artifact, visibility string, html []byte) (Result, error) {
	if cfg.OwnerKey == "" {
		return Result{}, fmt.Errorf("HART_OWNER_KEY is not set")
	}
	if owner == "" || artifact == "" {
		return Result{}, fmt.Errorf("owner and artifact are both required (got %q/%q)", owner, artifact)
	}
	if len(bytes.TrimSpace(html)) == 0 {
		return Result{}, fmt.Errorf("refusing to publish an empty artifact")
	}

	q := url.Values{}
	q.Set("owner", owner)
	q.Set("artifact", artifact)
	if visibility != "" {
		q.Set("visibility", visibility)
	}
	if visibility == "private" {
		if cfg.ReadKey == "" {
			return Result{}, fmt.Errorf("visibility private needs HART_READ_KEY")
		}
		q.Set("read_key", cfg.ReadKey)
	}
	endpoint := cfg.BaseURL + "/v1/publish?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(html))
	if err != nil {
		return Result{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "text/html")
	req.Header.Set("X-Hart-Owner-Key", cfg.OwnerKey)

	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("post to hart: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("hart returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	out := Result{Bytes: len(html)}
	// A 2xx with an unparseable body still means it was published; fall back to
	// the canonical artifact path rather than failing the loop over it.
	var parsed struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.URL != "" {
		out.URL = parsed.URL
	} else {
		out.URL = fmt.Sprintf("%s/a/%s/%s", cfg.BaseURL, owner, artifact)
	}

	// A private artifact that is readable without credentials is worse than a
	// failed publish, because it looks like a success. Prove the gate rather
	// than trusting it.
	if visibility == "private" {
		if err := verifyGated(ctx, out.URL); err != nil {
			return out, err
		}
		out.Gated = true
	}
	return out, nil
}

// verifyGated fetches the artifact with no credentials and requires a refusal.
func verifyGated(ctx context.Context, artifactURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifactURL, nil)
	if err != nil {
		return fmt.Errorf("build gating check: %w", err)
	}
	client := &http.Client{
		Timeout: 20 * time.Second,
		// Follow no redirects: a redirect to a login page is still a refusal,
		// and following it could turn a 302 into a 200.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("gating check for %s: %w", artifactURL, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden ||
		(resp.StatusCode >= 300 && resp.StatusCode < 400) {
		return nil
	}
	return fmt.Errorf("published %s as private but an anonymous fetch returned %d — it is not gated",
		artifactURL, resp.StatusCode)
}

// OwnerFromRepo takes the owner half of an "owner/name" repo string.
func OwnerFromRepo(repo string) string {
	if i := strings.IndexByte(repo, '/'); i > 0 {
		return repo[:i]
	}
	return repo
}
