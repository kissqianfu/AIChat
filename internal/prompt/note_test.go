package prompt

import (
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

func TestArkNoteWithoutTitle(t *testing.T) {
	if note := arkNoteOf(&port.Ark{ArkName: "位置"}); note != "\n（分享了一张位置）" {
		t.Fatalf("arkNoteOf = %q", note)
	}
	if note := arkNoteOf(nil); note != "" {
		t.Fatalf("arkNoteOf(nil) = %q", note)
	}
}
