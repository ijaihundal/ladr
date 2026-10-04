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

// Package errors provides the foundational error types used across the LADR backend.
// Ported from packages/ladr-errors/index.ts.
package errors

import (
	stderrors "errors"
	"fmt"
	"os"
	"strings"
)

// LadrError is the base error type for all application errors.
// It carries a short machine-readable code alongside the human message.
type LadrError struct {
	Message string
	Code    string
}

func (e *LadrError) Error() string {
	return e.Message
}

// NewLadrError creates a LadrError with the given message and code.
func NewLadrError(message, code string) *LadrError {
	return &LadrError{Message: message, Code: code}
}

// ProviderError represents an error from an upstream LLM provider.
type ProviderError struct {
	*LadrError
	ProviderName string
	StatusCode   int // 0 if not set
	Retryable    bool
	IsTimeout    bool
}

// ProviderErrorOptions configures a ProviderError.
type ProviderErrorOptions struct {
	Provider   string
	StatusCode int
	Retryable  bool
	IsTimeout  bool
}

// NewProviderError creates a ProviderError.
func NewProviderError(message string, opts ProviderErrorOptions) *ProviderError {
	return &ProviderError{
		LadrError:    NewLadrError(message, "provider_error"),
		ProviderName: opts.Provider,
		StatusCode:   opts.StatusCode,
		Retryable:    opts.Retryable,
		IsTimeout:    opts.IsTimeout,
	}
}

// ResourceLimitType enumerates the resource categories that can be exhausted.
type ResourceLimitType string

const (
	LimitLoops      ResourceLimitType = "loops"
	LimitNodes      ResourceLimitType = "nodes"
	LimitRecursion  ResourceLimitType = "recursion"
	LimitTotalCalls ResourceLimitType = "totalCalls"
)

// ResourceLimitError is returned when a pipeline exceeds a configured resource budget.
type ResourceLimitError struct {
	*LadrError
	LimitType ResourceLimitType
	Limit     int
}

// NewResourceLimitError creates a ResourceLimitError.
func NewResourceLimitError(limitType ResourceLimitType, limit int) *ResourceLimitError {
	return &ResourceLimitError{
		LadrError: NewLadrError(
			fmt.Sprintf("Resource limit exceeded: %s (max %d)", limitType, limit),
			"resource_limit_exceeded",
		),
		LimitType: limitType,
		Limit:     limit,
	}
}

// IsRetryableProviderError returns true if err is a ProviderError with Retryable==true.
func IsRetryableProviderError(err error) bool {
	var pe *ProviderError
	if As(err, &pe) {
		return pe.Retryable
	}
	return false
}

// --- Env var validation ---

// EnvVarSpec describes one environment variable that a service expects.
type EnvVarSpec struct {
	Name        string
	Required    bool
	Description string
}

// ValidateEnvVars checks that every required spec has a non-empty value in the
// environment.  It prints a descriptive error to stderr and returns an error if
// any required variables are missing.
func ValidateEnvVars(specs []EnvVarSpec, service string) error {
	var missing []string
	for _, spec := range specs {
		if spec.Required && os.Getenv(spec.Name) == "" {
			missing = append(missing, fmt.Sprintf("  - %s — %s", spec.Name, spec.Description))
		}
	}
	if len(missing) > 0 {
		msg := fmt.Sprintf("[%s] Missing required environment variables:\n%s",
			service, strings.Join(missing, "\n"))
		fmt.Fprintln(os.Stderr, msg)
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// As is a thin wrapper around errors.As so callers don't need a separate import.
// Declared as a variable so tests can stub it if needed.
var As = func(err error, target any) bool {
	return stderrors.As(err, target)
}
