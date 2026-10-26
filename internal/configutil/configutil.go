// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package configutil provides helpers to load configuration from environment variables using a flag-like API.
package configutil

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// getEnv returns the environment variable value for the given key,
// or defaultVal if the key is not set.
func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}

// StringEnv binds the env value for the key to the string pointer p.
func StringEnv(p *string, key, defaultVal string) {
	*p = getEnv(key, defaultVal)
}

// IntEnv binds the env value for the key parsed as an int to the int pointer p.
func IntEnv(p *int, key string, defaultVal int) {
	valStr := getEnv(key, "")
	if valStr == "" {
		*p = defaultVal
		return
	}
	i, err := strconv.Atoi(valStr)
	if err != nil {
		slog.Warn("Failed to parse config env var as int, falling back to default", "key", key, "value", valStr, "default", defaultVal, "error", err)
		*p = defaultVal
		return
	}
	*p = i
}

// Int64Env binds the env value for the key parsed as an int64 to the int64 pointer p.
func Int64Env(p *int64, key string, defaultVal int64) {
	valStr := getEnv(key, "")
	if valStr == "" {
		*p = defaultVal
		return
	}
	i, err := strconv.ParseInt(valStr, 10, 64)
	if err != nil {
		slog.Warn("Failed to parse config env var as int64, falling back to default", "key", key, "value", valStr, "default", defaultVal, "error", err)
		*p = defaultVal
		return
	}
	*p = i
}

// BoolEnv binds the env value for the key parsed as a bool to the bool pointer p.
func BoolEnv(p *bool, key string, defaultVal bool) {
	valStr := getEnv(key, "")
	if valStr == "" {
		*p = defaultVal
		return
	}
	b, err := strconv.ParseBool(valStr)
	if err != nil {
		slog.Warn("Failed to parse config env var as bool, falling back to default", "key", key, "value", valStr, "default", defaultVal, "error", err)
		*p = defaultVal
		return
	}
	*p = b
}

// DurationEnv binds the env value for the key parsed as a Duration to the Duration pointer p.
func DurationEnv(p *time.Duration, key string, defaultVal time.Duration) {
	valStr := getEnv(key, "")
	if valStr == "" {
		*p = defaultVal
		return
	}
	d, err := time.ParseDuration(valStr)
	if err != nil {
		slog.Warn("Failed to parse config env var as duration, falling back to default", "key", key, "value", valStr, "default", defaultVal, "error", err)
		*p = defaultVal
		return
	}
	*p = d
}

// Float64Env binds the env value for the key parsed as a float64 to the float64 pointer p.
func Float64Env(p *float64, key string, defaultVal float64) {
	valStr := getEnv(key, "")
	if valStr == "" {
		*p = defaultVal
		return
	}
	f, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		slog.Warn("Failed to parse config env var as float64, falling back to default", "key", key, "value", valStr, "default", defaultVal, "error", err)
		*p = defaultVal
		return
	}
	*p = f
}

// ReadSecretFile reads a secret from path and strips only trailing CR/LF line terminators so
// significant whitespace in credentials is preserved. envKey names the file-path environment
// variable in any read error.
func ReadSecretFile(path, envKey string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read %s %q: %w", envKey, path, err)
	}
	return strings.TrimRight(string(content), "\r\n"), nil
}
