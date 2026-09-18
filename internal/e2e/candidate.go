package e2e

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

var sha256Pattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type Candidate struct {
	ArchiveSHA256  string `json:"archiveSha256"`
	IndexDigest    string `json:"indexDigest"`
	ManifestDigest string `json:"manifestDigest"`
	ConfigDigest   string `json:"configDigest"`
	Platform       string `json:"platform"`
}

type ociDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

type ociDocument struct {
	SchemaVersion int             `json:"schemaVersion"`
	Manifests     []ociDescriptor `json:"manifests"`
	Config        ociDescriptor   `json:"config"`
	Layers        []ociDescriptor `json:"layers"`
}

type archiveBlob struct {
	size int64
	data []byte
}

func InspectCandidate(filename, expectedArchive, expectedIndex, platform string) (Candidate, error) {
	result := Candidate{IndexDigest: expectedIndex, Platform: platform}
	if !sha256Pattern.MatchString(expectedArchive) || !sha256Pattern.MatchString(expectedIndex) {
		return result, fmt.Errorf("archive checksum and index digest must be sha256:<64 lowercase hex characters>")
	}
	if platform != "linux/amd64" && platform != "linux/arm64" {
		return result, fmt.Errorf("candidate platform must be linux/amd64 or linux/arm64")
	}
	file, err := os.Open(filename)
	if err != nil {
		return result, err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return result, err
	}
	result.ArchiveSHA256 = "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if result.ArchiveSHA256 != expectedArchive {
		return result, fmt.Errorf("candidate archive checksum mismatch")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	blobs := make(map[string]archiveBlob)
	seen := make(map[string]bool)
	var index, layout []byte
	var retained int64
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, fmt.Errorf("read OCI archive: %w", err)
		}
		name := strings.TrimPrefix(header.Name, "./")
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg || seen[name] {
			return result, fmt.Errorf("unsupported or duplicate OCI archive entry")
		}
		seen[name] = true
		if name == "index.json" || name == "oci-layout" {
			if header.Size > 4<<20 {
				return result, fmt.Errorf("OCI metadata exceeds limit")
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				return result, err
			}
			if name == "index.json" {
				index = data
			} else {
				layout = data
			}
			continue
		}
		if !strings.HasPrefix(name, "blobs/sha256/") || !sha256Pattern.MatchString("sha256:"+strings.TrimPrefix(name, "blobs/sha256/")) {
			return result, fmt.Errorf("unexpected OCI archive entry")
		}
		digest := "sha256:" + strings.TrimPrefix(name, "blobs/sha256/")
		hasher.Reset()
		blob := archiveBlob{size: header.Size}
		if header.Size <= 4<<20 {
			retained += header.Size
			if retained > 64<<20 {
				return result, fmt.Errorf("OCI retained metadata exceeds limit")
			}
			blob.data, err = io.ReadAll(io.TeeReader(reader, hasher))
		} else {
			_, err = io.Copy(hasher, reader)
		}
		if err != nil {
			return result, err
		}
		if "sha256:"+hex.EncodeToString(hasher.Sum(nil)) != digest {
			return result, fmt.Errorf("OCI blob digest mismatch")
		}
		blobs[digest] = blob
	}
	var version struct {
		Version string `json:"imageLayoutVersion"`
	}
	if json.Unmarshal(layout, &version) != nil || version.Version != "1.0.0" {
		return result, fmt.Errorf("invalid OCI layout")
	}
	var root ociDocument
	if json.Unmarshal(index, &root) != nil || root.SchemaVersion != 2 {
		return result, fmt.Errorf("invalid OCI root index")
	}
	indexHash := sha256.Sum256(index)
	if "sha256:"+hex.EncodeToString(indexHash[:]) != expectedIndex {
		if len(root.Manifests) != 1 || root.Manifests[0].Digest != expectedIndex {
			return result, fmt.Errorf("OCI root does not identify expected index/manifest")
		}
	}
	var matches []Candidate
	var visit func(ociDescriptor, int) error
	getBlob := func(descriptor ociDescriptor) (archiveBlob, error) {
		blob, exists := blobs[descriptor.Digest]
		if !exists || blob.size != descriptor.Size {
			return blob, fmt.Errorf("missing OCI blob or descriptor size mismatch")
		}
		return blob, nil
	}
	visit = func(descriptor ociDescriptor, depth int) error {
		if depth > 8 {
			return fmt.Errorf("OCI index nesting exceeds limit")
		}
		blob, err := getBlob(descriptor)
		if err != nil {
			return err
		}
		var document ociDocument
		if json.Unmarshal(blob.data, &document) != nil || document.SchemaVersion != 2 {
			return fmt.Errorf("invalid OCI manifest/index")
		}
		switch descriptor.MediaType {
		case "application/vnd.oci.image.index.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json":
			for _, child := range document.Manifests {
				if err := visit(child, depth+1); err != nil {
					return err
				}
			}
		case "application/vnd.oci.image.manifest.v1+json", "application/vnd.docker.distribution.manifest.v2+json":
			config, err := getBlob(document.Config)
			if err != nil {
				return err
			}
			var properties struct {
				OS           string `json:"os"`
				Architecture string `json:"architecture"`
			}
			if json.Unmarshal(config.data, &properties) != nil {
				return fmt.Errorf("invalid image configuration")
			}
			for _, layer := range document.Layers {
				if _, err := getBlob(layer); err != nil {
					return err
				}
			}
			if properties.OS+"/"+properties.Architecture == platform {
				candidate := result
				candidate.ManifestDigest, candidate.ConfigDigest = descriptor.Digest, document.Config.Digest
				matches = append(matches, candidate)
			}
		default:
			return fmt.Errorf("unsupported OCI descriptor media type")
		}
		return nil
	}
	for _, descriptor := range root.Manifests {
		if err := visit(descriptor, 0); err != nil {
			return result, err
		}
	}
	if len(matches) != 1 {
		return result, fmt.Errorf("expected exactly one candidate manifest for %s, got %d", platform, len(matches))
	}
	return matches[0], nil
}
