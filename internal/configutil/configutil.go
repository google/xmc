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
	"os"
	"strconv"
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

// StringVar binds the env value for the key to the string pointer p.
func StringVar(p *string, key, defaultVal string) {
	*p = getEnv(key, defaultVal)
}

// IntVar binds the env value for the key parsed as an int to the int pointer p.
func IntVar(p *int, key string, defaultVal int) {
	valStr := getEnv(key, "")
	if valStr == "" {
		*p = defaultVal
		return
	}
	if i, err := strconv.Atoi(valStr); err == nil {
		*p = i
		return
	}
	*p = defaultVal
}

// Int64Var binds the env value for the key parsed as an int64 to the int64 pointer p.
func Int64Var(p *int64, key string, defaultVal int64) {
	valStr := getEnv(key, "")
	if valStr == "" {
		*p = defaultVal
		return
	}
	if i, err := strconv.ParseInt(valStr, 10, 64); err == nil {
		*p = i
		return
	}
	*p = defaultVal
}

// BoolVar binds the env value for the key parsed as a bool to the bool pointer p.
func BoolVar(p *bool, key string, defaultVal bool) {
	valStr := getEnv(key, "")
	if valStr == "" {
		*p = defaultVal
		return
	}
	if b, err := strconv.ParseBool(valStr); err == nil {
		*p = b
		return
	}
	*p = defaultVal
}

// DurationVar binds the env value for the key parsed as a Duration to the Duration pointer p.
func DurationVar(p *time.Duration, key string, defaultVal time.Duration) {
	valStr := getEnv(key, "")
	if valStr == "" {
		*p = defaultVal
		return
	}
	if d, err := time.ParseDuration(valStr); err == nil {
		*p = d
		return
	}
	*p = defaultVal
}

// Float64Var binds the env value for the key parsed as a float64 to the float64 pointer p.
func Float64Var(p *float64, key string, defaultVal float64) {
	valStr := getEnv(key, "")
	if valStr == "" {
		*p = defaultVal
		return
	}
	if f, err := strconv.ParseFloat(valStr, 64); err == nil {
		*p = f
		return
	}
	*p = defaultVal
}
