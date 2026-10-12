// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// Package logging provides a logger that logs to Google Cloud Logging.
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

const (
	keyLabelsGCP      = "logging.googleapis.com/labels"
	keySourceLocation = "logging.googleapis.com/sourceLocation"
	keyTraceGCP       = "logging.googleapis.com/trace"

	keySeverity  = "severity"
	keyMessage   = "message"
	keyTimestamp = "timestamp"
	keyTrace     = "trace"

	trimPrefix = "github.com/google/xmc/"
)

// contextKey is a private type to prevent key collisions in context
type contextKey struct {
	name string
}

// loggerKey is the specific key used to store the logger.
var loggerKey = &contextKey{"logger"}

// traceIDKey is used to store traceID in context.
var traceIDKey = &contextKey{"traceID"}

// labelsKey is used to store labels in context.
var labelsKey = &contextKey{"labels"}

type labelsMap map[string]string

func getLabelsFromContext(ctx context.Context) labelsMap {
	labels, ok := ctx.Value(labelsKey).(labelsMap)
	if !ok {
		return labelsMap{}
	}
	// Copy map to avoid mutation of parent context's map
	newLabels := make(labelsMap, len(labels))
	for k, v := range labels {
		newLabels[k] = v
	}
	return newLabels
}

func withLabelsInContext(ctx context.Context, labels labelsMap) context.Context {
	return context.WithValue(ctx, labelsKey, labels)
}

// WithLabels adds labels to the context for additive logging.
func WithLabels(ctx context.Context, labels ...slog.Attr) context.Context {
	l := getLabelsFromContext(ctx)
	for _, label := range labels {
		l[label.Key] = label.Value.String()
	}
	return withLabelsInContext(ctx, l)
}

// NewGCPLogger creates a slog.Logger configured for Google Cloud Logging
func NewGCPLogger(level slog.Level, projectID string) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: level,
		// AddSource adds the source file and line number to the log record.
		// GCP displays this nicely in the Logs Explorer.
		AddSource: true,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				a.Key = keySeverity
			}

			if a.Key == slog.MessageKey {
				a.Key = keyMessage
			}

			if a.Key == slog.TimeKey {
				a.Key = keyTimestamp
				if a.Value.Kind() == slog.KindTime {
					a.Value = slog.StringValue(a.Value.Time().Format(time.RFC3339Nano))
				}
			}

			if a.Key == slog.SourceKey {
				a.Key = keySourceLocation
				source, _ := a.Value.Any().(*slog.Source)
				if source != nil {
					source.File = filepath.Base(source.File)
					source.Function = strings.TrimPrefix(source.Function, trimPrefix)
				}
			}

			if a.Key == keyTrace && projectID != "" {
				a.Key = keyTraceGCP
				a.Value = slog.StringValue(fmt.Sprintf("projects/%s/traces/%s", projectID, a.Value.String()))
			}

			return a
		},
	}

	handler := slog.NewJSONHandler(os.Stdout, opts)
	return slog.New(handler)
}

// FromContext retrieves the logger from the context.
// If no logger is found, it returns the Default logger to prevent nil panics.
func FromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}

// WithLogger creates a new context with the provided logger attached.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

// Strip removes logging values from context and returns a new context.
// This is useful when we want to pass context to external libraries
// without propagating logging values.
func Strip(ctx context.Context) context.Context {
	return &stripContext{ctx}
}

type stripContext struct {
	context.Context
}

func (c *stripContext) Value(key any) any {
	if key == loggerKey || key == traceIDKey || key == labelsKey {
		return nil
	}
	return c.Context.Value(key)
}

// WithValues is a helper to add attributes to the logger in the CURRENT context
// and return a new context.
func WithValues(ctx context.Context, args ...any) context.Context {
	logger := FromContext(ctx)
	// Create a child logger with the new attributes
	childLogger := logger.With(args...)
	// Return new context containing the child logger
	return WithLogger(ctx, childLogger)
}

func log(ctx context.Context, level slog.Level, msg string, args ...any) {
	l := FromContext(ctx)
	if !l.Handler().Enabled(ctx, level) {
		return
	}

	for i := 0; i < len(args); i++ {
		if a, ok := args[i].(slog.Attr); ok {
			if a.Key == "error" {
				msg = fmt.Sprintf("%s: %v", msg, a.Value.Any())
			}
		} else if key, ok := args[i].(string); ok && key == "error" && i+1 < len(args) {
			msg = fmt.Sprintf("%s: %v", msg, args[i+1])
			i++ // we consumed value, skip it in next iteration
		}
	}

	var pcs [1]uintptr
	// Skip runtime.Callers, log function, and Info/Warn/Error/Debug wrapper.
	runtime.Callers(3, pcs[:])
	r := slog.NewRecord(time.Now(), level, msg, pcs[0])
	r.Add(args...)
	if level >= slog.LevelError {
		r.AddAttrs(slog.String("stacktrace", string(debug.Stack())))
	}
	if labels, ok := ctx.Value(labelsKey).(labelsMap); ok && len(labels) > 0 {
		var labelAttrs []any
		for k, v := range labels {
			labelAttrs = append(labelAttrs, slog.String(k, v))
		}
		r.AddAttrs(slog.Group(keyLabelsGCP, labelAttrs...))
	}
	if err := l.Handler().Handle(ctx, r); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing log entry: %v\n", err)
	}
}

// Info logs an info message with attributes using the logger from context.
func Info(ctx context.Context, msg string, args ...any) {
	log(ctx, slog.LevelInfo, msg, args...)
}

// Warn logs a warning message with attributes using the logger from context.
func Warn(ctx context.Context, msg string, args ...any) {
	log(ctx, slog.LevelWarn, msg, args...)
}

// Error logs an error message with attributes using the logger from context.
func Error(ctx context.Context, msg string, args ...any) {
	log(ctx, slog.LevelError, msg, args...)
}

// Debug logs a debug message with attributes using the logger from context.
func Debug(ctx context.Context, msg string, args ...any) {
	log(ctx, slog.LevelDebug, msg, args...)
}

// Fatal logs an error message with attributes and exits.
func Fatal(ctx context.Context, msg string, args ...any) {
	log(ctx, slog.LevelError, msg, args...)
	os.Exit(1)
}
