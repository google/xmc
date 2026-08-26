// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dashboard

import (
	"context"
	"fmt"
	"testing"
	"time"

	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
)

func TestListDashboards_Empty(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dashboards, nextPageToken, err := repo.ListDashboards(ctx, 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListDashboards on empty store failed: %v", err)
	}
	if len(dashboards) != 0 {
		t.Errorf("got %d dashboards, want 0", len(dashboards))
	}
	if nextPageToken != "" {
		t.Errorf("got nextPageToken %q, want empty", nextPageToken)
	}
}

func TestListDashboards_Basic(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	for i := 1; i <= 3; i++ {
		dash := blankDashboardProto(fmt.Sprintf("dashboards/test-list-basic-%d", i))
		dash.Title = fmt.Sprintf("Dashboard Title %d", i)
		if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
			t.Fatalf("CreateDashboard %d failed: %v", i, err)
		}
	}

	dashboards, _, err := repo.ListDashboards(ctx, 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListDashboards failed: %v", err)
	}
	if len(dashboards) != 3 {
		t.Fatalf("got %d dashboards, want 3", len(dashboards))
	}
}

func TestListDashboards_Pagination(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	for i := 1; i <= 5; i++ {
		dash := blankDashboardProto(fmt.Sprintf("dashboards/test-list-page-%d", i))
		dash.Title = fmt.Sprintf("Page Dashboard %d", i)
		if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
			t.Fatalf("CreateDashboard %d failed: %v", i, err)
		}
	}

	// Page 1: 2 items
	page1, token1, err := repo.ListDashboards(ctx, 2, "", "", "dashboard_id asc", 0)
	if err != nil {
		t.Fatalf("ListDashboards page 1 failed: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page 1: got %d items, want 2", len(page1))
	}
	if token1 == "" {
		t.Fatalf("page 1: expected non-empty token")
	}

	// Page 2: 2 items
	page2, token2, err := repo.ListDashboards(ctx, 2, token1, "", "dashboard_id asc", 0)
	if err != nil {
		t.Fatalf("ListDashboards page 2 failed: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page 2: got %d items, want 2", len(page2))
	}
	if token2 == "" {
		t.Fatalf("page 2: expected non-empty token")
	}

	// Page 3: 1 item
	page3, token3, err := repo.ListDashboards(ctx, 2, token2, "", "dashboard_id asc", 0)
	if err != nil {
		t.Fatalf("ListDashboards page 3 failed: %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("page 3: got %d items, want 1", len(page3))
	}
	if token3 != "" {
		t.Errorf("page 3: expected empty token, got %q", token3)
	}
}

func TestListDashboards_Filter(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash1 := blankDashboardProto("dashboards/filter-alpha")
	dash1.Title = "Alpha Target"
	if err := repo.CreateDashboard(ctx, dash1, nil); err != nil {
		t.Fatalf("CreateDashboard dash1 failed: %v", err)
	}

	dash2 := blankDashboardProto("dashboards/filter-beta")
	dash2.Title = "Beta Target"
	if err := repo.CreateDashboard(ctx, dash2, nil); err != nil {
		t.Fatalf("CreateDashboard dash2 failed: %v", err)
	}

	filter := `title = "Alpha Target"`
	dashboards, _, err := repo.ListDashboards(ctx, 10, "", filter, "", 0)
	if err != nil {
		t.Fatalf("ListDashboards with filter failed: %v", err)
	}
	if len(dashboards) != 1 {
		t.Fatalf("got %d dashboards, want 1", len(dashboards))
	}
	if dashboards[0].GetTitle() != "Alpha Target" {
		t.Errorf("got Title %q, want 'Alpha Target'", dashboards[0].GetTitle())
	}
}

func TestListDashboards_OrderBy(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	titles := []string{"Zebra", "Apple", "Mango"}
	for i, title := range titles {
		dash := blankDashboardProto(fmt.Sprintf("dashboards/test-order-%d", i))
		dash.Title = title
		if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
			t.Fatalf("CreateDashboard failed: %v", err)
		}
	}

	dashboards, _, err := repo.ListDashboards(ctx, 10, "", "", "title asc", 0)
	if err != nil {
		t.Fatalf("ListDashboards with orderBy failed: %v", err)
	}
	if len(dashboards) != 3 {
		t.Fatalf("got %d dashboards, want 3", len(dashboards))
	}
	if dashboards[0].GetTitle() != "Apple" || dashboards[1].GetTitle() != "Mango" || dashboards[2].GetTitle() != "Zebra" {
		t.Errorf("unexpected order: %v, %v, %v", dashboards[0].GetTitle(), dashboards[1].GetTitle(), dashboards[2].GetTitle())
	}
}

func TestListDashboards_InvalidFilter(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	_, _, err := repo.ListDashboards(ctx, 10, "", "invalid = [filter", "", 0)
	if err == nil {
		t.Errorf("ListDashboards with invalid filter succeeded, want error")
	}
}

func TestListDashboards_WithStaleness(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	dash := blankDashboardProto("dashboards/test-list-staleness-1")
	dash.Title = "Staleness Dashboard"
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard failed: %v", err)
	}

	dashboards, _, err := repo.ListDashboards(ctx, 10, "", "", "", 10)
	if err != nil {
		t.Fatalf("ListDashboards with staleness failed: %v", err)
	}
	if len(dashboards) != 1 {
		t.Fatalf("got %d dashboards, want 1", len(dashboards))
	}
}

func TestExtractDashboardCursor(t *testing.T) {
	t.Run("nil dashboard", func(t *testing.T) {
		_, err := ExtractDashboardCursor(nil)
		if err == nil {
			t.Errorf("ExtractDashboardCursor(nil) succeeded, want error")
		}
	})

	t.Run("empty fields populated with zero values", func(t *testing.T) {
		dash := &dpb.Dashboard{
			Name: "dashboards/d1",
		}
		cursor, err := ExtractDashboardCursor(dash)
		if err != nil {
			t.Fatalf("ExtractDashboardCursor failed: %v", err)
		}
		if cursor["dashboard_id"] != "d1" {
			t.Errorf("dashboard_id = %v, want 'd1'", cursor["dashboard_id"])
		}
		if _, ok := cursor["title"]; ok {
			t.Errorf("title should not be present in cursor")
		}
		if _, ok := cursor["owner"]; ok {
			t.Errorf("owner should not be present in cursor")
		}
		if cursor["created"] != (time.Time{}) {
			t.Errorf("created = %v, want zero time", cursor["created"])
		}
		if cursor["updated"] != (time.Time{}) {
			t.Errorf("updated = %v, want zero time", cursor["updated"])
		}
	})

	t.Run("populated fields", func(t *testing.T) {
		now := time.Now().Truncate(time.Microsecond)
		dash := &dpb.Dashboard{
			Name:       "dashboards/d2",
			Title:      "My Dashboard",
			Owner:      "user@example.com",
			CreateTime: timestamppb.New(now),
			UpdateTime: timestamppb.New(now),
		}
		cursor, err := ExtractDashboardCursor(dash)
		if err != nil {
			t.Fatalf("ExtractDashboardCursor failed: %v", err)
		}
		if cursor["dashboard_id"] != "d2" {
			t.Errorf("dashboard_id = %v, want 'd2'", cursor["dashboard_id"])
		}
		if _, ok := cursor["title"]; ok {
			t.Errorf("title should not be present in cursor")
		}
		if _, ok := cursor["owner"]; ok {
			t.Errorf("owner should not be present in cursor")
		}
		if cursor["created"] != now.UTC() {
			t.Errorf("created = %v, want %v", cursor["created"], now.UTC())
		}
		if cursor["updated"] != now.UTC() {
			t.Errorf("updated = %v, want %v", cursor["updated"], now.UTC())
		}
	})

	t.Run("invalid dashboard name returns error", func(t *testing.T) {
		dash := &dpb.Dashboard{
			Name: "invalid-name",
		}
		if _, err := ExtractDashboardCursor(dash); err == nil {
			t.Errorf("ExtractDashboardCursor with invalid name succeeded, want error")
		}
	})
}
