package containerrelease

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ghcrTagAvailable requests pull-only registry access without logging Docker in
// before image tests. Credentials stay in HTTP headers, never command arguments,
// Docker configuration, build arguments, or diagnostics.
func ghcrTagAvailable(image, tag, actor, credential string, transport http.RoundTripper) error {
	if actor == "" || credential == "" {
		return errors.New("GHCR tag check requires GITHUB_ACTOR and GH_TOKEN")
	}
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	repository := strings.TrimPrefix(image, "ghcr.io/")
	query := url.Values{"service": {"ghcr.io"}, "scope": {"repository:" + repository + ":pull"}}
	request, err := http.NewRequest(http.MethodGet, "https://ghcr.io/token?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	request.SetBasicAuth(actor, credential)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request GHCR pull token: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("request GHCR pull token: HTTP %d", response.StatusCode)
	}
	var auth struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&auth); err != nil {
		// Do not log response bodies or parse errors containing token material.
		return errors.New("invalid GHCR pull token response")
	}
	if auth.Token == "" {
		auth.Token = auth.AccessToken
	}
	if auth.Token == "" {
		return errors.New("GHCR pull token response has no token")
	}
	request, err = http.NewRequest(http.MethodHead, "https://ghcr.io/v2/"+repository+"/manifests/"+url.PathEscape(tag), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+auth.Token)
	request.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json")
	manifest, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("check GHCR image tag: %w", err)
	}
	defer manifest.Body.Close()
	switch manifest.StatusCode {
	case http.StatusNotFound:
		return nil
	case http.StatusOK:
		return fmt.Errorf("refusing to overwrite existing image %s:%s", image, tag)
	default:
		// Authentication and registry failures never imply an available tag.
		return fmt.Errorf("cannot check whether image tag exists: GHCR HTTP %d", manifest.StatusCode)
	}
}
