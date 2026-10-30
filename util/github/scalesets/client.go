// Copyright 2024 Cloudbase Solutions SRL
//
//    Licensed under the Apache License, Version 2.0 (the "License"); you may
//    not use this file except in compliance with the License. You may obtain
//    a copy of the License at
//
//         http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
//    WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
//    License for the specific language governing permissions and limitations
//    under the License.

package scalesets

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/go-github/v84/github"

	runnerErrors "github.com/cloudbase/garm-provider-common/errors"
	"github.com/cloudbase/garm/metrics"
	"github.com/cloudbase/garm/params"
	"github.com/cloudbase/garm/runner/common"
)

// Scale-set mutations and ordinary reads must not wait forever. The message
// broker can legitimately hold a long poll open, so it has a separate bound.
const requestTimeout = 60 * time.Second
const longPollRequestTimeout = 100 * time.Second

// scaleSetTransport is unauthenticated: each scale-set request supplies its own
// Authorization. Generic GitHub clients retain their existing transport.
func scaleSetTransport(creds params.ForgeCredentials) (*http.Transport, error) {
	var roots *x509.CertPool
	if creds.CABundle != nil {
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(creds.CABundle) {
			return nil, fmt.Errorf("failed to parse CA cert")
		}
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:       http.ProxyFromEnvironment,
		DialContext: dialer.DialContext,
		TLSClientConfig: &tls.Config{
			RootCAs:    roots,
			MinVersion: tls.VersionTLS12,
		},
		ForceAttemptHTTP2: true,
		HTTP2: &http.HTTP2Config{
			SendPingTimeout: 30 * time.Second,
			PingTimeout:     15 * time.Second,
		},
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}, nil
}

func NewClient(cli common.GithubClient) (*ScaleSetClient, error) {
	transport, err := scaleSetTransport(cli.GetEntity().Credentials)
	if err != nil {
		return nil, fmt.Errorf("building scale-set transport: %w", err)
	}
	return &ScaleSetClient{
		ghCli:          cli,
		httpClient:     &http.Client{Timeout: requestTimeout, Transport: transport},
		longPollClient: &http.Client{Timeout: longPollRequestTimeout, Transport: transport},
	}, nil
}

type ScaleSetClient struct {
	ghCli          common.GithubClient
	httpClient     *http.Client
	longPollClient *http.Client

	// scale sets are aparently available through the same security
	// contex that a normal runner would use. We connect to the same
	// API endpoint a runner would connect to, in order to fetch jobs.
	// To do this, we use a runner registration token.
	runnerRegistrationToken *github.RegistrationToken
	// actionsServiceInfo holds the pipeline URL and the JWT token to
	// access it. The pipeline URL is the base URL where we can access
	// the scale set endpoints.
	actionsServiceInfo *params.ActionsServiceAdminInfoResponse

	mux sync.Mutex
}

func (s *ScaleSetClient) recordOperation(operation string) {
	metrics.GithubOperationCount.WithLabelValues(
		operation,
		s.ghCli.GetEntity().LabelScope(),
	).Inc()
}

func (s *ScaleSetClient) recordFailedOperation(operation string) {
	metrics.GithubOperationFailedCount.WithLabelValues(
		operation,
		s.ghCli.GetEntity().LabelScope(),
	).Inc()
}

func (s *ScaleSetClient) SetGithubClient(cli common.GithubClient) {
	s.mux.Lock()
	defer s.mux.Unlock()
	s.ghCli = cli
}

func (s *ScaleSetClient) GetGithubClient() (common.GithubClient, error) {
	s.mux.Lock()
	defer s.mux.Unlock()
	if s.ghCli == nil {
		return nil, fmt.Errorf("github client is not set in scaleset client")
	}
	return s.ghCli, nil
}

func (s *ScaleSetClient) Do(req *http.Request) (*http.Response, error) {
	return s.doWithClient(s.httpClient, req)
}

func (s *ScaleSetClient) DoLongPoll(req *http.Request) (*http.Response, error) {
	return s.doWithClient(s.longPollClient, req)
}

func (s *ScaleSetClient) doWithClient(client *http.Client, req *http.Request) (*http.Response, error) {
	if client == nil {
		return nil, fmt.Errorf("http client is not initialized")
	}

	resp, err := client.Do(req) //nolint:gosec // G704 - URL is constructed from GitHub API endpoints
	if err != nil {
		return nil, fmt.Errorf("failed to dispatch HTTP request: %w", err)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}

	var body []byte
	if resp != nil {
		defer resp.Body.Close()
		body, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read body: %w", err)
		}
	}

	switch resp.StatusCode {
	case 404:
		return nil, runnerErrors.NewNotFoundError("resource %s not found: %q", req.URL.String(), string(body))
	case 400:
		return nil, runnerErrors.NewBadRequestError("bad request while calling %s: %q", req.URL.String(), string(body))
	case 409:
		return nil, runnerErrors.NewConflictError("conflict while calling %s: %q", req.URL.String(), string(body))
	case 401, 403:
		return nil, runnerErrors.ErrUnauthorized
	default:
		return nil, fmt.Errorf("request to %s failed with status code %d: %q", req.URL.String(), resp.StatusCode, string(body))
	}
}
