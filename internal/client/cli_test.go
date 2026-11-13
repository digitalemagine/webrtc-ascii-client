package client

import (
	"slices"
	"testing"
)

func TestSTUNFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		values  []string
		wantSet bool
		want    []string
		wantErr bool
	}{
		{name: "not given", wantSet: false},
		{name: "empty disables", values: []string{""}, wantSet: true},
		{name: "repeated", values: []string{"stun:a:3478", "stuns:b"}, wantSet: true, want: []string{"stun:a:3478", "stuns:b"}},
		{name: "not a STUN URL", values: []string{"turn:a"}, wantErr: true},
	}
	for _, tt := range tests {
		var f STUNFlag
		var err error
		for _, v := range tt.values {
			if err = f.Set(v); err != nil {
				break
			}
		}
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: error %v, wantErr %v", tt.name, err, tt.wantErr)
			continue
		}
		if tt.wantErr {
			continue
		}
		if f.set != tt.wantSet || !slices.Equal(f.urls, tt.want) {
			t.Errorf("%s: got set=%v urls=%v, want set=%v urls=%v", tt.name, f.set, f.urls, tt.wantSet, tt.want)
		}
	}
}

func TestParseTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind    string
		args    []string
		want    Target
		wantErr bool
	}{
		{kind: "auto", args: []string{"http://mtx:8889/cam/whep"}, want: Target{Kind: "whep", URL: "http://mtx:8889/cam/whep"}},
		{kind: "auto", args: []string{"ws://janus:8188", "1"}, want: Target{Kind: "janus", URL: "ws://janus:8188", SessionID: "1"}},
		{kind: "simple", args: []string{"ws://h/ws", "room"}, want: Target{Kind: "simple", URL: "ws://h/ws", SessionID: "room"}},
		{kind: "janus", args: []string{"ws://janus:8188"}, wantErr: true}, // needs a mountpoint
		{kind: "auto", args: nil, wantErr: true},
		{kind: "janus", args: []string{"ws://janus:8188", "1", "-o", "out.h264"}, wantErr: true}, // option after the URL
	}
	for _, tt := range tests {
		got, err := ParseTarget(tt.kind, tt.args)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("ParseTarget(%q, %q) = %+v, %v; want %+v, error %v", tt.kind, tt.args, got, err, tt.want, tt.wantErr)
		}
	}
}
