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
	"runtime"
	"time"
)

func logMsg(ctx context.Context, level slog.Level, msg string) {
	logger := slog.Default()
	if !logger.Handler().Enabled(ctx, level) {
		return
	}
	var pcs [1]uintptr
	// Skip runtime.Callers, logMsg, and the wrapper function (Infof/Warningf/etc.)
	runtime.Callers(3, pcs[:])
	r := slog.NewRecord(time.Now(), level, msg, pcs[0])
	_ = logger.Handler().Handle(ctx, r)
}

func Infof(format string, v ...interface{}) {
	logMsg(context.Background(), slog.LevelInfo, fmt.Sprintf(format, v...))
}

func Info(v ...interface{}) {
	logMsg(context.Background(), slog.LevelInfo, fmt.Sprint(v...))
}

func Warningf(format string, v ...interface{}) {
	logMsg(context.Background(), slog.LevelWarn, fmt.Sprintf(format, v...))
}

func Warning(v ...interface{}) {
	logMsg(context.Background(), slog.LevelWarn, fmt.Sprint(v...))
}

func Errorf(format string, v ...interface{}) {
	logMsg(context.Background(), slog.LevelError, fmt.Sprintf(format, v...))
}

func Error(v ...interface{}) {
	logMsg(context.Background(), slog.LevelError, fmt.Sprint(v...))
}

func Fatalf(format string, v ...interface{}) {
	logMsg(context.Background(), slog.LevelError, fmt.Sprintf(format, v...))
	os.Exit(1)
}

func Fatal(v ...interface{}) {
	logMsg(context.Background(), slog.LevelError, fmt.Sprint(v...))
	os.Exit(1)
}

func Exitf(format string, v ...interface{}) {
	logMsg(context.Background(), slog.LevelError, fmt.Sprintf(format, v...))
	os.Exit(1)
}

func Exit(v ...interface{}) {
	logMsg(context.Background(), slog.LevelError, fmt.Sprint(v...))
	os.Exit(1)
}

func InfoContextf(ctx context.Context, format string, v ...interface{}) {
	logMsg(ctx, slog.LevelInfo, fmt.Sprintf(format, v...))
}

func WarningContextf(ctx context.Context, format string, v ...interface{}) {
	logMsg(ctx, slog.LevelWarn, fmt.Sprintf(format, v...))
}

func ErrorContextf(ctx context.Context, format string, v ...interface{}) {
	logMsg(ctx, slog.LevelError, fmt.Sprintf(format, v...))
}

func FatalContextf(ctx context.Context, format string, v ...interface{}) {
	logMsg(ctx, slog.LevelError, fmt.Sprintf(format, v...))
	os.Exit(1)
}

type Verbose bool

func V(level int) Verbose {
	return Verbose(true)
}

func (v Verbose) Info(args ...interface{}) {
	if v {
		logMsg(context.Background(), slog.LevelInfo, fmt.Sprint(args...))
	}
}

func (v Verbose) Infof(format string, args ...interface{}) {
	if v {
		logMsg(context.Background(), slog.LevelInfo, fmt.Sprintf(format, args...))
	}
}

func (v Verbose) Warning(args ...interface{}) {
	if v {
		logMsg(context.Background(), slog.LevelWarn, fmt.Sprint(args...))
	}
}

func (v Verbose) Warningf(format string, args ...interface{}) {
	if v {
		logMsg(context.Background(), slog.LevelWarn, fmt.Sprintf(format, args...))
	}
}
