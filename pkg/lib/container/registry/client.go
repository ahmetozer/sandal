package registry

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Client talks to a single OCI-compatible container registry.
type Client struct {
	registry   string
	httpClient *http.Client
	auth       *authHandler
}

// NewClient creates a registry client. It automatically loads credentials
// from ~/.docker/config.json for the given registry.
func NewClient(registry string) *Client {
	user, pass := loadDockerCredentials(registry)
	return &Client{
		registry:   registry,
		httpClient: &http.Client{},
		auth: &authHandler{
			username: user,
			password: pass,
		},
	}
}

// baseURL returns the registry API base URL.
func (c *Client) baseURL() string {
	return "https://" + c.registry
}

// do executes an HTTP request with automatic auth retry on 401.
func (c *Client) do(ctx context.Context, req *http.Request, scope string) (*http.Response, error) {
	req = req.WithContext(ctx)
	c.auth.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		if err := c.auth.handleChallenge(resp, scope); err != nil {
			return nil, fmt.Errorf("auth: %w", err)
		}
		// Retry with new token.
		req2, _ := http.NewRequestWithContext(ctx, req.Method, req.URL.String(), req.Body)
		for k, v := range req.Header {
			req2.Header[k] = v
		}
		c.auth.applyAuth(req2)
		resp, err = c.httpClient.Do(req2)
		if err != nil {
			return nil, err
		}
	}

	return resp, nil
}

// GetManifest fetches a manifest or index by reference (tag or digest).
// Returns raw JSON bytes and the Content-Type.
func (c *Client) GetManifest(ctx context.Context, repo, ref string) ([]byte, string, error) {
	u := fmt.Sprintf("%s/v2/%s/manifests/%s", c.baseURL(), repo, ref)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, "", err
	}

	// Accept both manifest list/index and single manifest types.
	req.Header.Set("Accept", strings.Join([]string{
		MediaTypeDockerManifestList,
		MediaTypeOCIIndex,
		MediaTypeDockerManifest,
		MediaTypeOCIManifest,
	}, ", "))

	scope := "repository:" + repo + ":pull"
	resp, err := c.do(ctx, req, scope)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("GET manifest %s: %d %s", ref, resp.StatusCode, body)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	return data, resp.Header.Get("Content-Type"), nil
}

// GetBlob downloads a blob by digest. The caller must close the returned reader.
func (c *Client) GetBlob(ctx context.Context, repo, digest string) (io.ReadCloser, error) {
	u := fmt.Sprintf("%s/v2/%s/blobs/%s", c.baseURL(), repo, digest)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}

	scope := "repository:" + repo + ":pull"
	resp, err := c.do(ctx, req, scope)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("GET blob %s: %d %s", digest, resp.StatusCode, body)
	}

	return resp.Body, nil
}

// UploadBlob pushes a blob using monolithic upload.
func (c *Client) UploadBlob(ctx context.Context, repo string, data []byte, digest string) error {
	// Check if the blob already exists.
	headURL := fmt.Sprintf("%s/v2/%s/blobs/%s", c.baseURL(), repo, digest)
	headReq, _ := http.NewRequest("HEAD", headURL, nil)
	scope := "repository:" + repo + ":pull,push"
	headResp, err := c.do(ctx, headReq, scope)
	if err == nil {
		headResp.Body.Close()
		if headResp.StatusCode == http.StatusOK {
			return nil // already exists
		}
	}

	// Start upload session.
	postURL := fmt.Sprintf("%s/v2/%s/blobs/uploads/", c.baseURL(), repo)
	postReq, _ := http.NewRequest("POST", postURL, nil)
	postReq.Header.Set("Content-Type", "application/octet-stream")

	postResp, err := c.do(ctx, postReq, scope)
	if err != nil {
		return fmt.Errorf("start upload: %w", err)
	}
	defer postResp.Body.Close()

	if postResp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(postResp.Body)
		return fmt.Errorf("start upload: %d %s", postResp.StatusCode, body)
	}

	// Get the upload location.
	location := postResp.Header.Get("Location")
	if location == "" {
		return fmt.Errorf("no Location header in upload response")
	}

	// Resolve relative URLs.
	uploadURL, err := resolveURL(c.baseURL(), location)
	if err != nil {
		return fmt.Errorf("resolve upload URL: %w", err)
	}

	// Append digest query parameter.
	sep := "?"
	if strings.Contains(uploadURL, "?") {
		sep = "&"
	}
	uploadURL += sep + "digest=" + url.QueryEscape(digest)

	// PUT the blob data.
	putReq, _ := http.NewRequest("PUT", uploadURL, bytes.NewReader(data))
	putReq.Header.Set("Content-Type", "application/octet-stream")
	putReq.ContentLength = int64(len(data))
	c.auth.applyAuth(putReq)

	putResp, err := c.httpClient.Do(putReq.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("upload blob: %w", err)
	}
	defer putResp.Body.Close()

	if putResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(putResp.Body)
		return fmt.Errorf("upload blob PUT: %d %s", putResp.StatusCode, body)
	}

	return nil
}

// PutManifest pushes a manifest by reference.
func (c *Client) PutManifest(ctx context.Context, repo, ref string, manifest []byte, mediaType string) error {
	u := fmt.Sprintf("%s/v2/%s/manifests/%s", c.baseURL(), repo, ref)
	req, _ := http.NewRequest("PUT", u, bytes.NewReader(manifest))
	req.Header.Set("Content-Type", mediaType)

	scope := "repository:" + repo + ":pull,push"
	resp, err := c.do(ctx, req, scope)
	if err != nil {
		return fmt.Errorf("push manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("push manifest: %d %s", resp.StatusCode, body)
	}

	return nil
}

// resolveURL resolves a possibly-relative URL against a base.
func resolveURL(base, ref string) (string, error) {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref, nil
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	refURL, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	return baseURL.ResolveReference(refURL).String(), nil
}
