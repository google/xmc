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
	"log"
	"os"
)

func Infof(format string, v ...interface{}) {
	log.Printf("INFO: "+format, v...)
}

func Info(v ...interface{}) {
	log.Println(append([]interface{}{"INFO: "}, v...)...)
}

func Warningf(format string, v ...interface{}) {
	log.Printf("WARNING: "+format, v...)
}

func Warning(v ...interface{}) {
	log.Println(append([]interface{}{"WARNING: "}, v...)...)
}

func Errorf(format string, v ...interface{}) {
	log.Printf("ERROR: "+format, v...)
}

func Error(v ...interface{}) {
	log.Println(append([]interface{}{"ERROR: "}, v...)...)
}

func Fatalf(format string, v ...interface{}) {
	log.Printf("FATAL: "+format, v...)
	os.Exit(1)
}

func Fatal(v ...interface{}) {
	log.Println(append([]interface{}{"FATAL: "}, v...)...)
	os.Exit(1)
}

func Exitf(format string, v ...interface{}) {
	log.Printf("EXIT: "+format, v...)
	os.Exit(1)
}

func Exit(v ...interface{}) {
	log.Println(append([]interface{}{"EXIT: "}, v...)...)
	os.Exit(1)
}

func InfoContextf(ctx context.Context, format string, v ...interface{})    { Infof(format, v...) }
func WarningContextf(ctx context.Context, format string, v ...interface{}) { Warningf(format, v...) }
func ErrorContextf(ctx context.Context, format string, v ...interface{})   { Errorf(format, v...) }
func FatalContextf(ctx context.Context, format string, v ...interface{})   { Fatalf(format, v...) }

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
