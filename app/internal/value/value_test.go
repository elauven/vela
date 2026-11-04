package value

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

type user struct {
	ID      int64     `json:"id"`
	Name    string    `json:"name"`
	Joined  time.Time `json:"joined"`
	Manager *user     `json:"manager"`
	Tags    []string  `json:"tags"`
	secret  string
}

func TestFromGo(t *testing.T) {
	joined := time.Date(2026, 3, 17, 9, 30, 0, 0, time.FixedZone("CET", 3600))
	for _, tc := range []struct {
		name string
		in   any
		want any
		json string
	}{
		{"nil", nil, nil, "null"},
		{"int", 42, 42.0, "42"},
		{"large int loses precision as in JavaScript", int64(1<<53 + 1), float64(1 << 53), "9007199254740993"},
		{"float", 1.5, 1.5, "1.5"},
		{"string with html", "<b>&</b>", "<b>&</b>", `"<b>&</b>"`},
		{"bool", true, true, "true"},
		{"nil slice", []int(nil), nil, "null"},
		{"empty slice", []int{}, []any{}, "[]"},
		{"time", joined, "2026-03-17T09:30:00+01:00", `"2026-03-17T09:30:00+01:00"`},
		{"struct", user{ID: 1, Name: "Ada", Joined: joined, Tags: []string{"a"}, secret: "x"}, map[string]any{
			"id": 1.0, "name": "Ada", "joined": "2026-03-17T09:30:00+01:00", "manager": nil, "tags": []any{"a"},
		}, ""},
		{"nested", map[string]any{"a": []any{map[string]int{"b": 2}, nil}}, map[string]any{"a": []any{map[string]any{"b": 2.0}, nil}}, `{"a":[{"b":2},null]}`},
		{"map with int keys", map[int]string{2: "b", 1: "a"}, map[string]any{"1": "a", "2": "b"}, `{"1":"a","2":"b"}`},
	} {
		got, data, err := FromGo(tc.in)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %#v, want %#v", tc.name, got, tc.want)
		}
		if tc.json != "" && string(data) != tc.json {
			t.Errorf("%s: JSON %s, want %s", tc.name, data, tc.json)
		}
	}
	for _, bad := range []any{math.NaN(), make(chan int), func() {}, map[string]any{"x": math.Inf(1)}} {
		if _, _, err := FromGo(bad); err == nil || !strings.Contains(err.Error(), "not JSON") {
			t.Errorf("%T: %v", bad, err)
		}
	}
}

func TestInto(t *testing.T) {
	view, _, _ := FromGo(user{ID: 7, Name: "Grace"})
	got, err := Into[user](view)
	if err != nil || got.ID != 7 || got.Name != "Grace" {
		t.Errorf("Into: %+v %v", got, err)
	}
	if _, err := Into[int](view); err == nil {
		t.Error("an object into int")
	}
}
