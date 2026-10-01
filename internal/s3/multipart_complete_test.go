package s3

import (
	"errors"
	"slices"
	"testing"

	"github.com/i-got-this-faa/fbs/internal/metadata"
)

func TestNormalizeCompletedParts(t *testing.T) {
	t.Parallel()

	got, err := normalizeCompletedParts([]CompletePart{
		{PartNumber: 1, ETag: "a"},
		{PartNumber: 2, ETag: "b"},
		{PartNumber: 1, ETag: "a2"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	want := []CompletePart{{PartNumber: 1, ETag: "a2"}, {PartNumber: 2, ETag: "b"}}
	if !slices.Equal(got, want) {
		t.Fatalf("parts = %+v, want %+v (last entry wins at its first position)", got, want)
	}

	if _, err := normalizeCompletedParts(nil); !errors.Is(err, errNoCompletedParts) {
		t.Fatalf("empty err = %v, want errNoCompletedParts", err)
	}
	if _, err := normalizeCompletedParts([]CompletePart{{PartNumber: 2}, {PartNumber: 1}}); !errors.Is(err, errPartOrder) {
		t.Fatalf("descending err = %v, want errPartOrder", err)
	}
}

func TestMatchStoredParts(t *testing.T) {
	t.Parallel()

	stored := []metadata.MultipartPart{
		{PartNumber: 1, ETag: "aaa", Size: 10},
		{PartNumber: 2, ETag: "bbb", Size: 3},
	}
	tests := []struct {
		name      string
		requested []CompletePart
		wantErr   error
	}{
		{"all parts, small final part", []CompletePart{{1, `"aaa"`}, {2, "BBB"}}, nil},
		{"unknown part", []CompletePart{{1, "aaa"}, {3, "ccc"}}, errUnknownPart},
		{"etag mismatch", []CompletePart{{1, "zzz"}}, errUnknownPart},
		{"small non-final part", []CompletePart{{2, "bbb"}, {1, "aaa"}}, errPartTooSmall},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, err := matchStoredParts(tt.requested, stored, 5)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if err == nil && len(matched) != len(tt.requested) {
				t.Fatalf("matched %d parts, want %d", len(matched), len(tt.requested))
			}
		})
	}
}

func TestPartsAfterMarker(t *testing.T) {
	t.Parallel()

	parts := []metadata.MultipartPart{{PartNumber: 1}, {PartNumber: 3}, {PartNumber: 5}}
	numbers := func(page []metadata.MultipartPart) []int {
		out := []int{}
		for _, p := range page {
			out = append(out, p.PartNumber)
		}
		return out
	}

	page, truncated := partsAfterMarker(parts, 1, 1)
	if !slices.Equal(numbers(page), []int{3}) || !truncated {
		t.Fatalf("marker 1 limit 1 = %v truncated=%v", numbers(page), truncated)
	}
	page, truncated = partsAfterMarker(parts, 0, 10)
	if !slices.Equal(numbers(page), []int{1, 3, 5}) || truncated {
		t.Fatalf("marker 0 = %v truncated=%v", numbers(page), truncated)
	}
	page, truncated = partsAfterMarker(parts, 5, 10)
	if len(page) != 0 || truncated {
		t.Fatalf("marker past last = %v truncated=%v", numbers(page), truncated)
	}
}
