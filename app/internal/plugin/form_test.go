package plugin

import (
	"maps"
	"testing"
)

func TestClean(t *testing.T) {
	fields := []Field{
		{Key: "name", Label: "名称", Required: true},
		{Key: "count", Label: "数量", Type: Number, Min: Int(1), Max: Int(10), Default: "3"},
		{Key: "mode", Label: "模式", Type: Select, Options: []Option{{Value: "a"}, {Value: "b"}}},
		{Key: "on", Label: "开关", Type: Toggle, Default: "true"},
		{Key: "off", Label: "关闭", Type: Toggle},
	}
	got, err := Clean(fields, map[string]string{"name": " x ", "mode": "b", "extra": "drop"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"name": "x", "count": "3", "mode": "b", "on": "true", "off": "false"}
	if !maps.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	got, err = Clean(fields, map[string]string{"name": "x", "on": "false"})
	if err != nil || got["on"] != "false" {
		t.Fatalf("toggle off: %v %v", got, err)
	}
	for _, raw := range []map[string]string{
		{},
		{"name": "x", "count": "11"},
		{"name": "x", "count": "a"},
		{"name": "x", "mode": "c"},
		{"name": "x", "on": "maybe"},
	} {
		if _, err := Clean(fields, raw); err == nil {
			t.Errorf("Clean(%v) succeeded", raw)
		}
	}
}
