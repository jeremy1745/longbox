package service

import (
	"testing"
	"time"

	"github.com/jeremy/longbox/internal/model"
)

func ptrStr(s string) *string { return &s }

func TestFilterMissingIssues(t *testing.T) {
	skipped := ptrStr("skipped")
	ignored := ptrStr("ignored")

	cases := []struct {
		name string
		in   []model.Issue
		want []int64
	}{
		{
			name: "empty input returns empty",
			in:   nil,
			want: nil,
		},
		{
			name: "only owned issues filtered out",
			in: []model.Issue{
				{ID: 1, HasFile: true},
				{ID: 2, HasFile: true},
			},
			want: nil,
		},
		{
			name: "missing issues without skip status are kept",
			in: []model.Issue{
				{ID: 1, HasFile: false},
				{ID: 2, HasFile: false},
			},
			want: []int64{1, 2},
		},
		{
			name: "skip_status excludes the issue regardless of value",
			in: []model.Issue{
				{ID: 1, HasFile: false, SkipStatus: skipped},
				{ID: 2, HasFile: false, SkipStatus: ignored},
				{ID: 3, HasFile: false},
			},
			want: []int64{3},
		},
		{
			name: "owned beats skip status check (still excluded)",
			in: []model.Issue{
				{ID: 1, HasFile: true, SkipStatus: skipped},
				{ID: 2, HasFile: false},
			},
			want: []int64{2},
		},
		{
			name: "mixed series order is preserved",
			in: []model.Issue{
				{ID: 10, HasFile: false},
				{ID: 11, HasFile: true},
				{ID: 12, HasFile: false, SkipStatus: skipped},
				{ID: 13, HasFile: false},
			},
			want: []int64{10, 13},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterMissingIssues(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d issues, want %d (got=%v)", len(got), len(tc.want), got)
			}
			for i, issue := range got {
				if issue.ID != tc.want[i] {
					t.Errorf("index %d: got id=%d, want id=%d", i, issue.ID, tc.want[i])
				}
			}
		})
	}
}

func TestBacklogQueueNextRetryTimeUsesBackoffSchedule(t *testing.T) {
	q := &BacklogQueue{
		settings: BacklogSettings{
			MaxRetries:          3,
			RetryBackoffMinutes: []int{5, 15, 60},
		},
	}

	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 1, want: 5 * time.Minute},
		{attempt: 2, want: 15 * time.Minute},
		{attempt: 3, want: 60 * time.Minute},
		{attempt: 4, want: 60 * time.Minute}, // clamps to last entry
	}

	for _, tc := range cases {
		got := q.nextRetryTime(tc.attempt)
		if got == nil {
			t.Fatalf("attempt %d: nextRetryTime returned nil", tc.attempt)
		}
		// Allow a small slack window for clock movement during test execution.
		delta := time.Until(*got) - tc.want
		if delta > 2*time.Second || delta < -2*time.Second {
			t.Errorf("attempt %d: backoff = %v, want ~%v (delta=%v)", tc.attempt, time.Until(*got), tc.want, delta)
		}
	}
}

func TestBacklogQueueNextRetryReturnsNilWhenNoSchedule(t *testing.T) {
	q := &BacklogQueue{settings: BacklogSettings{MaxRetries: 3}}
	if got := q.nextRetryTime(1); got != nil {
		t.Fatalf("expected nil with empty backoff schedule, got %v", got)
	}
}
