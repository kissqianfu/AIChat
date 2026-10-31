package schedule

import (
	"strings"
	"testing"
	"time"
)

func TestParseDaily(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	d, err := ParseDaily("0 3 * * *", loc)
	if err != nil {
		t.Fatalf("ParseDaily() error = %v", err)
	}
	// 上海 03:00 = UTC 19:00（前一天）。
	at := time.Date(2026, 10, 31, 18, 0, 0, 0, time.UTC)
	next := d.Next(at)
	want := time.Date(2026, 10, 31, 19, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("Next() = %v, want %v", next, want)
	}
	// 已过当天 03:00 → 次日。
	at = time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC) // 上海 04:00
	if next := d.Next(at); !next.Equal(want.AddDate(0, 0, 1)) {
		t.Fatalf("Next(after) = %v", next)
	}
}

func TestParseDailyErrors(t *testing.T) {
	tests := []struct{ name, expr string }{
		{"段数不对", "0 3 * *"},
		{"日字段不支持", "0 3 1 * *"},
		{"月字段不支持", "0 3 * 1 *"},
		{"周字段不支持", "0 3 * * 1"},
		{"小时非法", "0 24 * * *"},
		{"分钟非法", "60 3 * * *"},
		{"非数字", "x 3 * * *"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseDaily(tt.expr, time.UTC); err == nil {
				t.Fatalf("ParseDaily(%q) error = nil", tt.expr)
			}
		})
	}
}

func TestDailySlot(t *testing.T) {
	d, err := ParseDaily("0 3 * * *", time.UTC)
	if err != nil {
		t.Fatalf("ParseDaily() error = %v", err)
	}
	if slot := d.Slot(time.Date(2026, 10, 31, 3, 0, 30, 0, time.UTC)); slot != "2026-10-31 03:00" {
		t.Fatalf("Slot(hit) = %q", slot)
	}
	if slot := d.Slot(time.Date(2026, 10, 31, 3, 1, 0, 0, time.UTC)); slot != "" {
		t.Fatalf("Slot(miss) = %q", slot)
	}
}

func TestParseDailyDefaultLocation(t *testing.T) {
	d, err := ParseDaily("30 23 * * *", nil)
	if err != nil {
		t.Fatalf("ParseDaily() error = %v", err)
	}
	next := d.Next(time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC))
	if next.Hour() != 23 || next.Minute() != 30 {
		t.Fatalf("Next() = %v", next)
	}
	if !strings.Contains(next.Location().String(), "UTC") {
		t.Fatalf("loc = %v", next.Location())
	}
}
