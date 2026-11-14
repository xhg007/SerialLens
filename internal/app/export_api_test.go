package app

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportSaveFile(t *testing.T) {
	e := NewExportAPI()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.csv")

	content := "timestamp,ch1\r\n2026-11-14T13:00:00.000Z,42\r\n"
	if err := e.SaveFile(path, base64.StdEncoding.EncodeToString([]byte(content))); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != content {
		t.Fatalf("content mismatch:\n got %q\nwant %q", got, content)
	}
}

func TestExportSaveFileBinary(t *testing.T) {
	e := NewExportAPI()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.png")

	raw := []byte{0x89, 0x50, 0x4E, 0x47, 0x00, 0xFF, 0x00}
	if err := e.SaveFile(path, base64.StdEncoding.EncodeToString(raw)); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(raw) {
		t.Fatalf("binary mismatch: got %x want %x", got, raw)
	}
}

func TestExportSaveFileInvalid(t *testing.T) {
	e := NewExportAPI()
	dir := t.TempDir()
	cases := []struct {
		name    string
		path    string
		b64     string
		wantErr string
	}{
		{"empty path", "", base64.StdEncoding.EncodeToString([]byte("x")), "empty path"},
		{"bad base64", filepath.Join(dir, "a.txt"), "!!!not-base64!!!", "decode base64"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := e.SaveFile(c.path, c.b64)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("want error containing %q, got %v", c.wantErr, err)
			}
		})
	}
}
