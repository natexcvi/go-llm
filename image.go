package harness

import (
	"errors"
	"net/url"
	"slices"
)

// Image is an image input for a user turn. Supply an HTTP(S) URL or the raw
// image bytes and their media type. Binary data is base64-encoded in snapshots.
type Image struct {
	URL       string `json:"url,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Data      []byte `json:"data,omitempty"`
}

func (i Image) Validate() error {
	if len(i.Data) > 0 {
		if i.URL != "" {
			return errors.New("image has both bytes and URL")
		}
		switch i.MediaType {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
			return nil
		}
		return errors.New("unsupported image media type")
	}
	u, err := url.Parse(i.URL)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("image requires bytes or an absolute HTTP(S) URL")
	}
	return nil
}
func cloneImages(images []Image) []Image {
	result := slices.Clone(images)
	for i := range result {
		result[i].Data = slices.Clone(result[i].Data)
	}
	return result
}
