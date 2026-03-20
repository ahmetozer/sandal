package registry

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// authHandler manages bearer token and basic auth for a registry.
type authHandler struct {
	username string
	password string
	token    string // cached bearer token
}

// applyAuth sets the Authorization header on a request.
func (a *authHandler) applyAuth(req *http.Request) {
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	} else if a.username != "" {
		req.SetBasicAuth(a.username, a.password)
	}
}

// handleChallenge parses a 401 response's WWW-Authenticate header,
// fetches a bearer token, and caches it.
func (a *authHandler) handleChallenge(resp *http.Response, scope string) error {
	wwwAuth := resp.Header.Get("WWW-Authenticate")
	if wwwAuth == "" {
		return fmt.Errorf("401 with no WWW-Authenticate header")
	}

	scheme, params := parseWWWAuthenticate(wwwAuth)

	if strings.EqualFold(scheme, "basic") {
		// Basic auth — nothing more to do if creds are already set.
		if a.username == "" {
			return fmt.Errorf("registry requires basic auth but no credentials provided")
		}
		return nil
	}

	if !strings.EqualFold(scheme, "bearer") {
		return fmt.Errorf("unsupported auth scheme: %s", scheme)
	}

	realm := params["realm"]
	if realm == "" {
		return fmt.Errorf("bearer challenge missing realm")
	}

	// Build token URL.
	tokenURL := realm + "?"
	if svc, ok := params["service"]; ok {
		tokenURL += "service=" + svc + "&"
	}
	if scope != "" {
		tokenURL += "scope=" + scope
	}

	tokenReq, err := http.NewRequest("GET", tokenURL, nil)
	if err != nil {
		return fmt.Errorf("build token request: %w", err)
	}
	if a.username != "" {
		tokenReq.SetBasicAuth(a.username, a.password)
	}

	tokenResp, err := http.DefaultClient.Do(tokenReq)
	if err != nil {
		return fmt.Errorf("fetch token: %w", err)
	}
	defer tokenResp.Body.Close()

	if tokenResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(tokenResp.Body)
		return fmt.Errorf("token endpoint returned %d: %s", tokenResp.StatusCode, body)
	}

	var tr struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&tr); err != nil {
		return fmt.Errorf("decode token response: %w", err)
	}

	a.token = tr.Token
	if a.token == "" {
		a.token = tr.AccessToken
	}
	if a.token == "" {
		return fmt.Errorf("empty token from %s", realm)
	}

	return nil
}

// parseWWWAuthenticate parses "Bearer realm=...,service=...,scope=..." into
// the scheme and a key-value map.
func parseWWWAuthenticate(header string) (string, map[string]string) {
	params := make(map[string]string)

	// Split scheme from the rest.
	scheme, rest, _ := strings.Cut(header, " ")
	rest = strings.TrimSpace(rest)

	// Parse key="value" pairs separated by commas.
	for rest != "" {
		rest = strings.TrimSpace(rest)
		key, after, found := strings.Cut(rest, "=")
		if !found {
			break
		}
		key = strings.TrimSpace(key)
		after = strings.TrimSpace(after)

		var val string
		if strings.HasPrefix(after, "\"") {
			// Quoted value.
			after = after[1:]
			end := strings.IndexByte(after, '"')
			if end == -1 {
				val = after
				rest = ""
			} else {
				val = after[:end]
				rest = strings.TrimLeft(after[end+1:], ", ")
			}
		} else {
			// Unquoted value.
			end := strings.IndexByte(after, ',')
			if end == -1 {
				val = after
				rest = ""
			} else {
				val = after[:end]
				rest = after[end+1:]
			}
		}
		params[strings.ToLower(key)] = val
	}

	return scheme, params
}

// dockerConfig represents ~/.docker/config.json.
type dockerConfig struct {
	Auths map[string]struct {
		Auth string `json:"auth"`
	} `json:"auths"`
}

// loadDockerCredentials reads ~/.docker/config.json and returns
// username/password for the given registry.
func loadDockerCredentials(registry string) (string, string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", ""
	}

	data, err := os.ReadFile(filepath.Join(home, ".docker", "config.json"))
	if err != nil {
		return "", ""
	}

	var cfg dockerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", ""
	}

	// Try exact match, then try with https:// prefix.
	lookups := []string{registry}
	if registry == "registry-1.docker.io" {
		lookups = append(lookups, "https://index.docker.io/v1/", "index.docker.io", "docker.io")
	}

	for _, key := range lookups {
		if entry, ok := cfg.Auths[key]; ok && entry.Auth != "" {
			decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
			if err != nil {
				continue
			}
			user, pass, _ := strings.Cut(string(decoded), ":")
			return user, pass
		}
	}

	return "", ""
}
