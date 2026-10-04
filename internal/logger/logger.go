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

// Package logger provides structured JSON logging backed by log/slog.
// Ported from packages/ladr-logger/index.ts.
package logger

import (
	"log/slog"
	"os"
	"strings"
)

// redactReplaceAttr is the set of keys whose values should be redacted.
var redactKeys = map[string]bool{
	"apiKey":        true,
	"authorization": true,
	"x-api-key":     true,
	"auth":          true,
	"token":         true,
	"secret":        true,
	"password":      true,
}

// redactReplacer replaces sensitive attribute values with "[REDACTED]".
func redactReplacer(_ []string, a slog.Attr) slog.Attr {
	if redactKeys[strings.ToLower(a.Key)] {
		a.Value = slog.StringValue("[REDACTED]")
	}
	return a
}

// levelFromString converts a textual log level (case-insensitive) to a slog.Level.
// Defaults to slog.LevelInfo when empty or unrecognised — mirroring the TS "info"
// default.
func levelFromString(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "trace", "verbose":
		// Map trace/verbose to Debug for parity with TS pino extras.
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

// handler builds the shared JSON handler used by the root logger and child
// loggers.
func handler() slog.Handler {
	level := levelFromString(os.Getenv("LOG_LEVEL"))
	return slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: redactReplacer,
	})
}

// L returns the package-level root logger.  Equivalent to the `logger` export
// in the TS module.
func L() *slog.Logger {
	return root
}

var root *slog.Logger

func init() {
	root = slog.New(handler())
}

// CreateChildLogger returns a logger that always includes component=<component>
// in every record, mirroring logger.child({ component }) from the TS source.
func CreateChildLogger(component string) *slog.Logger {
	return root.With(slog.String("component", component))
}
