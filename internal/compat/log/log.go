// Copyright 2025 Google LLC
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

package log

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

func Infof(format string, v ...interface{}) {
	slog.Default().Info(fmt.Sprintf(format, v...))
}

func Info(v ...interface{}) {
	slog.Default().Info(fmt.Sprint(v...))
}

func Warningf(format string, v ...interface{}) {
	slog.Default().Warn(fmt.Sprintf(format, v...))
}

func Warning(v ...interface{}) {
	slog.Default().Warn(fmt.Sprint(v...))
}

func Errorf(format string, v ...interface{}) {
	slog.Default().Error(fmt.Sprintf(format, v...))
}

func Error(v ...interface{}) {
	slog.Default().Error(fmt.Sprint(v...))
}

func Fatalf(format string, v ...interface{}) {
	slog.Default().Error(fmt.Sprintf(format, v...))
	os.Exit(1)
}

func Fatal(v ...interface{}) {
	slog.Default().Error(fmt.Sprint(v...))
	os.Exit(1)
}

func Exitf(format string, v ...interface{}) {
	slog.Default().Error(fmt.Sprintf(format, v...))
	os.Exit(1)
}

func Exit(v ...interface{}) {
	slog.Default().Error(fmt.Sprint(v...))
	os.Exit(1)
}

func InfoContextf(ctx context.Context, format string, v ...interface{}) {
	slog.Default().InfoContext(ctx, fmt.Sprintf(format, v...))
}

func WarningContextf(ctx context.Context, format string, v ...interface{}) {
	slog.Default().WarnContext(ctx, fmt.Sprintf(format, v...))
}

func ErrorContextf(ctx context.Context, format string, v ...interface{}) {
	slog.Default().ErrorContext(ctx, fmt.Sprintf(format, v...))
}

func FatalContextf(ctx context.Context, format string, v ...interface{}) {
	slog.Default().ErrorContext(ctx, fmt.Sprintf(format, v...))
	os.Exit(1)
}

type Verbose bool

func V(level int) Verbose {
	return Verbose(true)
}

func (v Verbose) Info(args ...interface{}) {
	if v {
		Info(args...)
	}
}

func (v Verbose) Infof(format string, args ...interface{}) {
	if v {
		Infof(format, args...)
	}
}

func (v Verbose) Warning(args ...interface{}) {
	if v {
		Warning(args...)
	}
}

func (v Verbose) Warningf(format string, args ...interface{}) {
	if v {
		Warningf(format, args...)
	}
}
