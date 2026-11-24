package storage_test

import (
	"strings"
	"testing"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/storage"
)

func TestMediaArchiveRoundTripAndCorruption(t *testing.T) {
	blobs := &storage.Memory{}
	archive := storage.MediaArchive{Snapshots: &storage.Memory{}, Blobs: blobs}
	img := h.Image{Data: []byte("abc"), MediaType: "image/png"}
	s := h.Start("inspect", nil, img, img)
	if err := archive.Checkpoint("run")(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	raw, err := storage.Load(t.Context(), archive.Snapshots, "run")
	const uri = "media+sha256://ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if err != nil || raw.History[0].Images[0].URL != uri || len(s.History[0].Images[0].Data) != 3 {
		t.Fatal(raw, err)
	}
	saved, err := archive.Load(t.Context(), "run")
	if err != nil || string(saved.History[0].Images[1].Data) != "abc" {
		t.Fatal(saved, err)
	}
	keys, err := blobs.List(t.Context())
	if err != nil || len(keys) != 1 {
		t.Fatal(keys, err)
	}
	if err := blobs.Put(t.Context(), keys[0], []byte("corrupt")); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Load(t.Context(), "run"); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatal(err)
	}
}
