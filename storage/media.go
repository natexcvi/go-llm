package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	h "github.com/natexcvi/go-llm"
)

const mediaPrefix = "media+sha256://"

// MediaURI returns the canonical upstream content address for a byte sequence.
func MediaURI(data []byte) string { return fmt.Sprintf("%s%x", mediaPrefix, sha256.Sum256(data)) }

// MediaArchive stores large user images separately from the transcript.
// Supply separately scoped stores for snapshots and blobs within a tenant.
// Blobs are written before the snapshot, so failure leaves the previous
// checkpoint readable. The application owns retention and garbage collection.
type MediaArchive struct {
	Snapshots, Blobs Store
	MinBytes         int
}

func mediaKey(uri string) (string, error) {
	digest, ok := strings.CutPrefix(uri, mediaPrefix)
	if !ok || len(digest) != 64 || digest != strings.ToLower(digest) {
		return "", errors.New("invalid media URI")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", err
	}
	return digest + ".bin", nil
}
func (m MediaArchive) Checkpoint(key string) func(context.Context, h.Snapshot) error {
	return func(ctx context.Context, s h.Snapshot) error {
		if m.Blobs == nil || m.Snapshots == nil || m.MinBytes < 0 {
			return errors.New("invalid media archive configuration")
		}
		s.History = slices.Clone(s.History)
		written := map[string]bool{}
		for i := range s.History {
			images := slices.Clone(s.History[i].Images)
			s.History[i].Images = images
			for j, image := range images {
				if err := image.Validate(); err != nil {
					return err
				}
				if len(image.Data) == 0 || len(image.Data) < m.MinBytes {
					continue
				}
				uri := MediaURI(image.Data)
				blobKey, _ := mediaKey(uri)
				if !written[uri] {
					if err := m.Blobs.Put(ctx, blobKey, image.Data); err != nil {
						return err
					}
					written[uri] = true
				}
				images[j].Data = nil
				images[j].URL = uri
			}
		}
		return Checkpoint(m.Snapshots, key)(ctx, s)
	}
}

// Load restores the image bytes and verifies each blob against its content
// address. It also accepts ordinary checkpoints containing inline images.
func (m MediaArchive) Load(ctx context.Context, key string) (h.Snapshot, error) {
	if m.Blobs == nil || m.Snapshots == nil {
		return h.Snapshot{}, errors.New("invalid media archive configuration")
	}
	s, err := Load(ctx, m.Snapshots, key)
	if err != nil {
		return h.Snapshot{}, err
	}
	for i := range s.History {
		for j, image := range s.History[i].Images {
			if !strings.HasPrefix(image.URL, mediaPrefix) {
				continue
			}
			k, err := mediaKey(image.URL)
			if err != nil {
				return h.Snapshot{}, err
			}
			b, err := m.Blobs.Get(ctx, k)
			if err != nil {
				return h.Snapshot{}, err
			}
			if MediaURI(b) != image.URL {
				return h.Snapshot{}, fmt.Errorf("blob digest mismatch for %s", image.URL)
			}
			image.Data = b
			image.URL = ""
			if err = image.Validate(); err != nil {
				return h.Snapshot{}, err
			}
			s.History[i].Images[j] = image
		}
	}
	return s, nil
}
