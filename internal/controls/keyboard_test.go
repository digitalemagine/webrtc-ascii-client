package controls

import (
	"reflect"
	"testing"
)

func TestKeyParserFeed(t *testing.T) {
	t.Parallel()

	quit := Command{Type: CmdQuit}
	tests := []struct {
		name  string
		reads []string // successive reads
		flush bool     // read timeout after the last read
		want  []Command
	}{
		{name: "plain keys", reads: []string{"qc+-fh"}, want: []Command{
			quit, {Type: CmdToggleColor}, {Type: CmdIncreaseQuality},
			{Type: CmdDecreaseQuality}, {Type: CmdToggleSize}, {Type: CmdShowHelp},
		}},
		{name: "renderer keys", reads: []string{"17"}, want: []Command{
			{Type: CmdSwitchRenderer, Value: 0}, {Type: CmdSwitchRenderer, Value: 6},
		}},
		{name: "unmapped keys", reads: []string{"xyz 0 8\r\n"}},
		{name: "utf-8 is ignored", reads: []string{"é→"}},
		{name: "lone esc waits for timeout", reads: []string{"\x1b"}},
		{name: "lone esc after timeout", reads: []string{"\x1b"}, flush: true, want: []Command{quit}},
		{name: "arrow key", reads: []string{"\x1b[A"}, flush: true},
		{name: "arrow key split across reads", reads: []string{"\x1b", "[A"}, flush: true},
		{name: "arrow key split after bracket", reads: []string{"\x1b[", "A"}, flush: true},
		{name: "csi with parameters", reads: []string{"\x1b[1;5C"}, flush: true},
		{name: "ss3 function key", reads: []string{"\x1bOP"}, flush: true},
		{name: "alt key", reads: []string{"\x1bq"}, flush: true},
		{name: "keys around a sequence", reads: []string{"c\x1b[Bc"}, want: []Command{
			{Type: CmdToggleColor}, {Type: CmdToggleColor},
		}},
		{name: "double esc", reads: []string{"\x1b\x1b"}, flush: true, want: []Command{quit, quit}},
		{name: "esc then arrow", reads: []string{"\x1b\x1b[A"}, flush: true, want: []Command{quit}},
		{name: "incomplete sequence dropped on timeout", reads: []string{"\x1b["}, flush: true},
		{name: "malformed csi resumes parsing", reads: []string{"\x1b[\x01q"}, want: []Command{quit}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var p keyParser
			var got []Command
			for _, r := range tt.reads {
				got = append(got, p.feed([]byte(r))...)
			}
			if tt.flush {
				got = append(got, p.flush()...)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKeyboardHandlerStopWithoutStart(t *testing.T) {
	t.Parallel()

	k, err := NewKeyboardHandler()
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Stop(); err != nil {
		t.Fatalf("Stop without Start: %v", err)
	}
	if err := k.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}
