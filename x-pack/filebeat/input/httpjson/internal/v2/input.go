// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License;
// you may not use this file except in compliance with the Elastic License.

package v2

import (
	"context"
	"fmt"
	"github.com/goccy/go-json"
	"net"
	"net/http"
	"net/url"
	"time"

	retryablehttp "github.com/hashicorp/go-retryablehttp"
	"go.uber.org/zap"

	v2 "github.com/elastic/beats/v7/filebeat/input/v2"
	inputcursor "github.com/elastic/beats/v7/filebeat/input/v2/input-cursor"
	"github.com/elastic/beats/v7/libbeat/beat"
	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/libbeat/common/transport/httpcommon"
	"github.com/elastic/beats/v7/libbeat/common/useragent"
	"github.com/elastic/beats/v7/libbeat/logp"
	"github.com/elastic/go-concert/ctxtool"
	"github.com/elastic/go-concert/timed"
)

const (
	inputName = "httpjson"
)

var (
	userAgent = useragent.UserAgent("Filebeat", true)

	// for testing
	timeNow = time.Now
)

type retryLogger struct {
	log *logp.Logger
}

func newRetryLogger(log *logp.Logger) *retryLogger {
	return &retryLogger{
		log: log.Named("retryablehttp").WithOptions(zap.AddCallerSkip(1)),
	}
}

func (log *retryLogger) Error(format string, args ...interface{}) {
	log.log.Errorf(format, args...)
}

func (log *retryLogger) Info(format string, args ...interface{}) {
	log.log.Infof(format, args...)
}

func (log *retryLogger) Debug(format string, args ...interface{}) {
	log.log.Debugf(format, args...)
}

func (log *retryLogger) Warn(format string, args ...interface{}) {
	log.log.Warnf(format, args...)
}

func test(url *url.URL) error {
	port := func() string {
		if url.Port() != "" {
			return url.Port()
		}
		switch url.Scheme {
		case "https":
			return "443"
		}
		return "80"
	}()

	_, err := net.DialTimeout("tcp", net.JoinHostPort(url.Hostname(), port), time.Second)
	if err != nil {
		return fmt.Errorf("url %q is unreachable", url)
	}

	return nil
}

func run(
	ctx v2.Context,
	config config,
	publisher inputcursor.Publisher,
	cursor *inputcursor.Cursor,
) error {
	log := ctx.Logger.With("input_url", config.Request.URL)

	stdCtx := ctxtool.FromCanceller(ctx.Cancelation)

	httpClient, err := newHTTPClient(stdCtx, config, log)
	if err != nil {
		return err
	}

	requestFactory := newRequestFactory(config.Request, config.Auth, log)
	pagination := newPagination(config, httpClient, log)
	responseProcessor := newResponseProcessor(config.Response, pagination, log)
	requester := newRequester(httpClient, requestFactory, responseProcessor, log)

	trCtx := emptyTransformContext()
	trCtx.cursor = newCursor(config.Cursor, log)
	trCtx.cursor.load(cursor)

	doFunc := func() error {
		log.Info("Process another repeated request.")

		if err := requester.doRequest(stdCtx, trCtx, publisher); err != nil {
			log.Errorf("Error while processing http request: %v", err)
		}

		if stdCtx.Err() != nil {
			return err
		}

		return nil
	}

	// we trigger the first call immediately,
	// then we schedule it on the given interval using timed.Periodic
	if err = doFunc(); err == nil {
		err = timed.Periodic(stdCtx, config.Interval, doFunc)
	}

	log.Infof("Input stopped because context was cancelled with: %v", err)

	return nil
}

func newHTTPClient(ctx context.Context, config config, log *logp.Logger) (*httpClient, error) {
	// Make retryable HTTP client
	netHTTPClient, err := config.Request.Transport.Client(clientOptions(config.Request.URL.URL, config.Request.KeepAlive.Settings())...)
	if err != nil {
		return nil, err
	}

	netHTTPClient.CheckRedirect = checkRedirect(config.Request, log)

	client := &retryablehttp.Client{
		HTTPClient:   netHTTPClient,
		Logger:       newRetryLogger(log),
		RetryWaitMin: config.Request.Retry.getWaitMin(),
		RetryWaitMax: config.Request.Retry.getWaitMax(),
		RetryMax:     config.Request.Retry.getMaxAttempts(),
		CheckRetry:   retryablehttp.DefaultRetryPolicy,
		Backoff:      retryablehttp.DefaultBackoff,
	}

	limiter := newRateLimiterFromConfig(config.Request.RateLimit, log)

	if config.Auth.OAuth2.isEnabled() {
		authClient, err := config.Auth.OAuth2.client(ctx, client.StandardClient())
		if err != nil {
			return nil, err
		}
		return &httpClient{client: authClient, limiter: limiter}, nil
	}

	return &httpClient{client: client.StandardClient(), limiter: limiter}, nil
}

// clientOptions returns constructed client configuration options.
func clientOptions(u *url.URL, keepalive httpcommon.WithKeepaliveSettings) []httpcommon.TransportOption {
	return []httpcommon.TransportOption{
		httpcommon.WithAPMHTTPInstrumentation(),
		keepalive,
	}
}

func checkRedirect(config *requestConfig, log *logp.Logger) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		log.Debug("http client: checking redirect")
		if len(via) >= config.RedirectMaxRedirects {
			log.Debug("http client: max redirects exceeded")
			return fmt.Errorf("stopped after %d redirects", config.RedirectMaxRedirects)
		}

		if !config.RedirectForwardHeaders || len(via) == 0 {
			log.Debugf("http client: nothing to do while checking redirects - forward_headers: %v, via: %#v", config.RedirectForwardHeaders, via)
			return nil
		}

		prev := via[len(via)-1] // previous request to get headers from

		log.Debugf("http client: forwarding headers from previous request: %#v", prev.Header)
		req.Header = prev.Header.Clone()

		for _, k := range config.RedirectHeadersBanList {
			log.Debugf("http client: ban header %v", k)
			req.Header.Del(k)
		}

		return nil
	}
}

func makeEvent(body common.MapStr) (beat.Event, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return beat.Event{}, err
	}
	now := timeNow()
	fields := common.MapStr{
		"event": common.MapStr{
			"created": now,
		},
		"message": string(bodyBytes),
	}

	return beat.Event{
		Timestamp: now,
		Fields:    fields,
	}, nil
}
