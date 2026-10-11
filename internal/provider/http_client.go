// LADR - the programming layer for inference-time reasoning.
// Copyright (C) 2026  Redeo Labs Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ladr/ladr/pkg/errors"
)

// Default timeouts and limits — can be overridden via env vars.
var (
	defaultTimeoutMs       = envInt("PROVIDER_TIMEOUT_MS", 60000)
	defaultStreamTimeoutMs = envInt("PROVIDER_STREAM_TIMEOUT_MS", 30*60*1000)
	defaultIdleTimeoutMs   = envInt("PROVIDER_IDLE_TIMEOUT_MS", 60000)
	defaultMaxRespBytes    = envInt("PROVIDER_MAX_RESPONSE_BYTES", 10*1024*1024)
)

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// GetProviderTimeoutMs returns the override or the default timeout.
func GetProviderTimeoutMs(override int) int {
	if override != 0 {
		return override
	}
	return defaultTimeoutMs
}

// IsRetryableHttpStatus returns true for 429, 502, 503, 504.
func IsRetryableHttpStatus(status int) bool {
	return status == 429 || status == 502 || status == 503 || status == 504
}

// trimTrailingSlash removes a single trailing "/" from the URL.
func trimTrailingSlash(s string) string {
	if strings.HasSuffix(s, "/") {
		return s[:len(s)-1]
	}
	return s
}

// ProviderFetch performs an HTTP request with a context-based timeout.
// Returns a ProviderError on network failure or timeout.
func ProviderFetch(
	ctx context.Context,
	providerName string,
	url string,
	method string,
	headers map[string]string,
	body []byte,
	timeoutMs int,
) (*http.Response, error) {
	ms := GetProviderTimeoutMs(timeoutMs)
	if ms <= 0 {
		ms = defaultTimeoutMs
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(ms)*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, errors.NewProviderError(fmt.Sprintf("Network error: %s", err.Error()), errors.ProviderErrorOptions{
			Provider:  providerName,
			Retryable: true,
		})
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, errors.NewProviderError("Request timed out", errors.ProviderErrorOptions{
				Provider:  providerName,
				IsTimeout: true,
				Retryable: true,
			})
		}
		return nil, errors.NewProviderError(fmt.Sprintf("Network error: %s", err.Error()), errors.ProviderErrorOptions{
			Provider:  providerName,
			Retryable: true,
		})
	}
	return resp, nil
}


// ProviderFetchBytes performs the request and reads the FULL body inside the
// timeout scope, then returns status + bytes. Use this for non-stream calls:
// ProviderFetch's defer-cancel fires when it returns, and a body read after
// return races a canceled context (large bodies reliably fail).
func ProviderFetchBytes(
	ctx context.Context,
	providerName string,
	url string,
	method string,
	headers map[string]string,
	body []byte,
	timeoutMs int,
) (int, []byte, error) {
	ms := GetProviderTimeoutMs(timeoutMs)
	if ms <= 0 {
		ms = defaultTimeoutMs
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(ms)*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, errors.NewProviderError(fmt.Sprintf("Network error: %s", err.Error()), errors.ProviderErrorOptions{
			Provider:  providerName,
			Retryable: true,
		})
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return 0, nil, errors.NewProviderError("Request timed out", errors.ProviderErrorOptions{
				Provider:  providerName,
				IsTimeout: true,
				Retryable: true,
			})
		}
		return 0, nil, errors.NewProviderError(fmt.Sprintf("Network error: %s", err.Error()), errors.ProviderErrorOptions{
			Provider:  providerName,
			Retryable: true,
		})
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(defaultMaxRespBytes)))
	if err != nil {
		return resp.StatusCode, nil, errors.NewProviderError(fmt.Sprintf("Body read: %s", err.Error()), errors.ProviderErrorOptions{
			Provider:  providerName,
			Retryable: true,
		})
	}
	return resp.StatusCode, data, nil
}

// ProviderFetchStream performs an HTTP request for a streaming response.
// The caller is responsible for closing the response body.
func ProviderFetchStream(
	ctx context.Context,
	providerName string,
	url string,
	method string,
	headers map[string]string,
	body []byte,
	timeoutMs int,
) (*http.Response, error) {
	ms := timeoutMs
	if ms == 0 {
		ms = defaultStreamTimeoutMs
	}
	// The request must NOT be tied to this function's lifetime: the caller
	// reads resp.Body in its own goroutine AFTER return, and a canceled
	// context kills that read (the empty-stream bug). Hard deadline is
	// enforced by a watchdog below; idle timeout + byte cap live in the
	// SSE parser.
	reqCtx := context.WithoutCancel(ctx)

	req, err := http.NewRequestWithContext(reqCtx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, errors.NewProviderError(fmt.Sprintf("Network error: %s", err.Error()), errors.ProviderErrorOptions{
			Provider:  providerName,
			Retryable: true,
		})
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, errors.NewProviderError("Request timed out waiting for stream headers", errors.ProviderErrorOptions{
				Provider:  providerName,
				IsTimeout: true,
				Retryable: true,
			})
		}
		return nil, errors.NewProviderError(fmt.Sprintf("Network error: %s", err.Error()), errors.ProviderErrorOptions{
			Provider:  providerName,
			Retryable: true,
		})
	}

	// Hard deadline for the whole stream: close the body at ms (idempotent;
	// the SSE parser's own defer Body.Close and idle watchdog still apply).
	time.AfterFunc(time.Duration(ms)*time.Millisecond, func() { resp.Body.Close() })

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, errors.NewProviderError(
			fmt.Sprintf("%s stream failed (%d): %s", providerName, resp.StatusCode, string(bodyBytes)),
			errors.ProviderErrorOptions{
				Provider:   providerName,
				StatusCode: resp.StatusCode,
				Retryable:  IsRetryableHttpStatus(resp.StatusCode),
			},
		)
	}
	return resp, nil
}

// ParseOpenAiSSEStream parses an SSE stream from an HTTP response body and
// sends StreamEvents on the returned channel. The channel is closed when the
// stream completes. The caller should start reading the channel immediately.
func ParseOpenAiSSEStream(resp *http.Response, idleTimeoutMs int) <-chan StreamEvent {
	ch := make(chan StreamEvent, 32)

	go func() {
		defer close(ch)
		defer resp.Body.Close()

		idleMs := idleTimeoutMs
		if idleMs == 0 {
			idleMs = defaultIdleTimeoutMs
		}

		reader := bufio.NewReader(resp.Body)
		var usage *TokenUsage
		totalBytes := 0

		idleTimer := time.NewTimer(time.Duration(idleMs) * time.Millisecond)
		defer idleTimer.Stop()

		done := make(chan struct{})
		defer close(done)

		// Watchdog goroutine for idle timeout
		go func() {
			select {
			case <-idleTimer.C:
				// attempt to close the body to unblock the reader
				resp.Body.Close()
			case <-done:
			}
		}()

		resetIdle := func() {
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(time.Duration(idleMs) * time.Millisecond)
		}

		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				resetIdle()
				totalBytes += len(line)
				if totalBytes > defaultMaxRespBytes {
					return
				}

				trimmed := strings.TrimSpace(string(line))
				if trimmed == "" || strings.HasPrefix(trimmed, ":") {
					// continue on empty lines and comments
				} else if trimmed == "data: [DONE]" {
					if usage != nil {
						ch <- NewUsageEvent(*usage)
					}
					ch <- NewDone()
					return
				} else if strings.HasPrefix(trimmed, "data: ") {
					jsonStr := trimmed[6:]
					var chunk map[string]interface{}
					if json.Unmarshal([]byte(jsonStr), &chunk) == nil {
						// extract text delta from choices[0].delta.content
						if choices, ok := chunk["choices"].([]interface{}); ok && len(choices) > 0 {
							if firstChoice, ok := choices[0].(map[string]interface{}); ok {
								if delta, ok := firstChoice["delta"].(map[string]interface{}); ok {
									if content, ok := delta["content"].(string); ok && content != "" {
										ch <- NewTextDelta(content)
									}
								}
							}
						}
						// extract usage
						if rawUsage, ok := chunk["usage"].(map[string]interface{}); ok {
							prompt := 0
							completion := 0
							if v, ok := rawUsage["prompt_tokens"].(float64); ok {
								prompt = int(v)
							}
							if v, ok := rawUsage["completion_tokens"].(float64); ok {
								completion = int(v)
							}
							usage = &TokenUsage{PromptTokens: prompt, CompletionTokens: completion}
						}
					}
				}
			}
			if err != nil {
				break
			}
		}

		if usage != nil {
			ch <- NewUsageEvent(*usage)
		}
		ch <- NewDone()
	}()

	return ch
}
