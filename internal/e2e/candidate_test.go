package e2e

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectCandidate(t *testing.T) {
	for _, variant := range []string{"valid", "wrong checksum", "wrong index", "wrong platform", "corrupt blob", "missing layer", "duplicate platform", "unsafe entry"} {
		t.Run(variant, func(t *testing.T) {
			entries := map[string][]byte{"oci-layout": []byte(`{"imageLayoutVersion":"1.0.0"}`)}
			add := func(data []byte, media string) ociDescriptor {
				digest := sha256.Sum256(data)
				value := hex.EncodeToString(digest[:])
				entries["blobs/sha256/"+value] = data
				return ociDescriptor{Digest: "sha256:" + value, Size: int64(len(data)), MediaType: media}
			}
			config := add([]byte(`{"os":"linux","architecture":"arm64"}`), "application/vnd.oci.image.config.v1+json")
			layer := add([]byte("layer"), "application/vnd.oci.image.layer.v1.tar")
			manifestJSON, _ := json.Marshal(ociDocument{SchemaVersion: 2, Config: config, Layers: []ociDescriptor{layer}})
			manifest := add(manifestJSON, "application/vnd.oci.image.manifest.v1+json")
			manifests := []ociDescriptor{manifest}
			if variant == "duplicate platform" {
				manifests = append(manifests, manifest)
			}
			indexJSON, _ := json.Marshal(ociDocument{SchemaVersion: 2, Manifests: manifests})
			index := add(indexJSON, "application/vnd.oci.image.index.v1+json")
			entries["index.json"], _ = json.Marshal(ociDocument{SchemaVersion: 2, Manifests: []ociDescriptor{index}})
			if variant == "corrupt blob" {
				entries["blobs/sha256/"+strings.TrimPrefix(layer.Digest, "sha256:")] = []byte("bad")
			}
			if variant == "missing layer" {
				delete(entries, "blobs/sha256/"+strings.TrimPrefix(layer.Digest, "sha256:"))
			}
			if variant == "unsafe entry" {
				entries["../escape"] = []byte("bad")
			}
			var archive bytes.Buffer
			writer := tar.NewWriter(&archive)
			for name, data := range entries {
				if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data))}); err != nil {
					t.Fatal(err)
				}
				if _, err := writer.Write(data); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			filename := filepath.Join(t.TempDir(), "candidate.tar")
			if err := os.WriteFile(filename, archive.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			checksum := sha256.Sum256(archive.Bytes())
			expected := "sha256:" + hex.EncodeToString(checksum[:])
			platform := "linux/arm64"
			if variant == "wrong checksum" {
				expected = "sha256:" + strings.Repeat("0", 64)
			}
			if variant == "wrong index" {
				index.Digest = "sha256:" + strings.Repeat("0", 64)
			}
			if variant == "wrong platform" {
				platform = "linux/amd64"
			}
			result, err := InspectCandidate(filename, expected, index.Digest, platform)
			if variant != "valid" {
				if err == nil {
					t.Fatal("invalid candidate accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.ManifestDigest != manifest.Digest || result.ConfigDigest != config.Digest || result.ArchiveSHA256 != expected {
				t.Fatalf("incorrect provenance: %+v", result)
			}
		})
	}
}
