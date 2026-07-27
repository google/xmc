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

package clickhouse

import (
	"testing"
	"time"
)

func TestMicroOrTime_ScanInt64(t *testing.T) {
	var m MicroOrTime
	nowMicros := time.Now().UnixMicro()

	if err := m.Scan(nowMicros); err != nil {
		t.Fatalf("Scan int64 failed: %v", err)
	}

	protoTS := m.ToProtoTimestamp()
	if protoTS == nil {
		t.Fatal("expected non-nil proto timestamp")
	}

	if protoTS.Seconds != nowMicros/1000000 {
		t.Errorf("got seconds %d, want %d", protoTS.Seconds, nowMicros/1000000)
	}
}

func TestMicroOrTime_ScanTime(t *testing.T) {
	var m MicroOrTime
	now := time.Now().Truncate(time.Second)

	if err := m.Scan(now); err != nil {
		t.Fatalf("Scan time.Time failed: %v", err)
	}

	protoTS := m.ToProtoTimestamp()
	if protoTS == nil {
		t.Fatal("expected non-nil proto timestamp")
	}

	if protoTS.Seconds != now.Unix() {
		t.Errorf("got seconds %d, want %d", protoTS.Seconds, now.Unix())
	}
}

func TestMicroOrTime_ScanInvalid(t *testing.T) {
	var m MicroOrTime
	if err := m.Scan("invalid"); err == nil {
		t.Error("expected error scanning string into MicroOrTime, got nil")
	}
}
