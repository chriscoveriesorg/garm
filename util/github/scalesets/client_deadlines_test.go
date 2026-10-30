// Copyright 2026 Cloudbase Solutions SRL
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package scalesets

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/cloudbase/garm/params"
	"github.com/cloudbase/garm/runner/common"
)

// The source URL comes from the retained v0.2.1 README. No request leaves the
// process: the only transport used below injects failure, never a response.
type failingTransport struct {
	calls int
}

func (f *failingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.calls++
	<-req.Context().Done()
	return nil, req.Context().Err()
}

type clientForTransportTest struct {
	common.GithubClient
}

func (clientForTransportTest) GetEntity() params.ForgeEntity {
	return params.ForgeEntity{}
}

func TestScaleSetClientsHaveIndependentDeadlinesAndSharedTransport(t *testing.T) {
	client, err := NewClient(clientForTransportTest{})
	if err != nil {
		t.Fatal(err)
	}
	if client.httpClient.Timeout != 60*time.Second || client.longPollClient.Timeout != 100*time.Second {
		t.Fatalf("unexpected client timeouts: %v / %v", client.httpClient.Timeout, client.longPollClient.Timeout)
	}
	transport, ok := client.httpClient.Transport.(*http.Transport)
	if !ok || transport != client.longPollClient.Transport {
		t.Fatal("scale-set clients do not share one base transport")
	}
	if !transport.ForceAttemptHTTP2 || transport.HTTP2 == nil || transport.HTTP2.SendPingTimeout != 30*time.Second || transport.HTTP2.PingTimeout != 15*time.Second {
		t.Fatal("HTTP/2 health check configuration differs")
	}
	if transport.TLSClientConfig == nil || transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatal("TLS minimum version differs")
	}
}

func TestRejectedCABundleUsesRetainedSourceBytes(t *testing.T) {
	readme, err := os.ReadFile("../../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scaleSetTransport(params.ForgeCredentials{CABundle: readme}); err == nil {
		t.Fatal("non-certificate source bytes accepted as a CA bundle")
	}
}

func TestInjectedFailureIsNotRetried(t *testing.T) {
	readme, err := os.ReadFile("../../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	sourceURL := regexp.MustCompile(`https://github[.]com/cloudbase/garm/[^)[:space:]]+`).Find(readme)
	if len(sourceURL) == 0 {
		t.Fatal("retained source URL missing")
	}
	client, err := NewClient(clientForTransportTest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, dispatch := range []struct {
		method string
		call   func(*http.Request) (*http.Response, error)
		target *http.Client
	}{
		{http.MethodPost, client.Do, client.httpClient},
		{http.MethodGet, client.DoLongPoll, client.longPollClient},
	} {
		transport := &failingTransport{}
		dispatch.target.Transport = transport
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		req, err := http.NewRequestWithContext(ctx, dispatch.method, string(sourceURL), nil)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		_, err = dispatch.call(req)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || transport.calls != 1 {
			t.Fatalf("failure %v, dispatch count %d", err, transport.calls)
		}
	}
}

// GetMessage uses the public README URL only as a request-construction input;
// the injected transport fails before any network or successful response.
func TestGetMessageUsesLongPollClientOnInjectedFailure(t *testing.T) {
	readme, err := os.ReadFile("../../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	sourceURL := regexp.MustCompile(`https://github[.]com/cloudbase/garm/[^)[:space:]]+`).Find(readme)
	if len(sourceURL) == 0 {
		t.Fatal("retained source URL missing")
	}
	client, err := NewClient(clientForTransportTest{})
	if err != nil {
		t.Fatal(err)
	}
	ordinary := &failingTransport{}
	poll := &failingTransport{}
	client.httpClient.Transport = ordinary
	client.longPollClient.Transport = poll
	session := &MessageSession{ssCli: client, session: &params.RunnerScaleSetSession{MessageQueueURL: string(sourceURL)}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = session.GetMessage(ctx, 0, 2)
	if !errors.Is(err, context.DeadlineExceeded) || poll.calls != 1 || ordinary.calls != 0 {
		t.Fatalf("failure %v, long-poll calls %d, ordinary calls %d", err, poll.calls, ordinary.calls)
	}
}
