package binaries

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// NetBird publishes its combined server (management, signal, relay, STUN and
// the embedded IdP) only as a container image. The official executable is the
// image's top layer, /go/bin/netbird-server; it is taken unmodified. The tag
// names the same version as the latest stable client release, and every
// further step is pinned by digest: index → platform manifest → layer.
const (
	serverRegistry   = "https://ghcr.io"
	serverRepository = "netbirdio/netbird-server"
)

// serverPlatforms names each GOARCH's image platform. The arm image is built
// with GOARM=7, the CPU of DSM's armv7 models.
var serverPlatforms = map[string]struct{ arch, variant string }{
	"amd64": {"amd64", ""},
	"arm64": {"arm64", ""},
	"arm":   {"arm", "v7"},
}

var digestRE = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

const (
	ociIndex    = "application/vnd.oci.image.index.v1+json"
	ociManifest = "application/vnd.oci.image.manifest.v1+json"
	dockerList  = "application/vnd.docker.distribution.manifest.list.v2+json"
	dockerImage = "application/vnd.docker.distribution.manifest.v2+json"
)

var gzipLayers = map[string]bool{
	"application/vnd.oci.image.layer.v1.tar+gzip":       true,
	"application/vnd.docker.image.rootfs.diff.tar.gzip": true,
}

func (s *OfficialSource) registryToken(ctx context.Context) (string, error) {
	raw, e := s.get(ctx, serverRegistry+"/token?scope=repository:"+serverRepository+":pull", 64<<10)
	if e != nil {
		return "", e
	}
	var reply struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &reply) != nil || reply.Token == "" || len(reply.Token) > 8192 || strings.ContainsAny(reply.Token, "\r\n ") {
		return "", errors.New("invalid registry token")
	}
	return reply.Token, nil
}

// manifest reads one manifest; a digest reference must match its bytes.
func (s *OfficialSource) manifest(ctx context.Context, token, reference, accept string) ([]byte, error) {
	var b strings.Builder
	e := s.fetchWith(ctx, serverRegistry+"/v2/"+serverRepository+"/manifests/"+reference, 1<<20, &b, map[string]string{"Authorization": "Bearer " + token, "Accept": accept})
	if e != nil {
		return nil, e
	}
	raw := []byte(b.String())
	if strings.HasPrefix(reference, "sha256:") {
		sum := sha256.Sum256(raw)
		if "sha256:"+hex.EncodeToString(sum[:]) != reference {
			return nil, errors.New("manifest digest mismatch")
		}
	}
	return raw, nil
}

// serverRelease pins the platform's executable layer of the image tagged
// version. Download then verifies the layer bytes against that digest.
func (s *OfficialSource) serverRelease(ctx context.Context, version string) (Release, error) {
	r := Release{Product: "netbird-server", Version: version}
	platform, ok := serverPlatforms[s.arch]
	if !ok {
		return r, errors.New("unsupported architecture")
	}
	token, e := s.registryToken(ctx)
	if e != nil {
		return r, e
	}
	raw, e := s.manifest(ctx, token, version, ociIndex+", "+dockerList)
	if e != nil {
		return r, e
	}
	var index struct {
		MediaType string `json:"mediaType"`
		Manifests []struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
			Platform  struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
				Variant      string `json:"variant"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if json.Unmarshal(raw, &index) != nil || index.MediaType != ociIndex && index.MediaType != dockerList {
		return r, errors.New("invalid image index")
	}
	digest := ""
	for _, m := range index.Manifests {
		if m.Platform.OS == "linux" && m.Platform.Architecture == platform.arch && m.Platform.Variant == platform.variant {
			if digest != "" || !digestRE.MatchString(m.Digest) || m.MediaType != ociManifest && m.MediaType != dockerImage {
				return r, errors.New("ambiguous image platform")
			}
			digest = m.Digest
		}
	}
	if digest == "" {
		return r, errors.New("official NetBird server image missing for this architecture")
	}
	raw, e = s.manifest(ctx, token, digest, ociManifest+", "+dockerImage)
	if e != nil {
		return r, e
	}
	var image struct {
		Layers []struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
			Size      int64  `json:"size"`
		} `json:"layers"`
	}
	if json.Unmarshal(raw, &image) != nil || len(image.Layers) == 0 {
		return r, errors.New("invalid image manifest")
	}
	// The executable is the last layer: the image's only COPY on its base.
	top := image.Layers[len(image.Layers)-1]
	if !gzipLayers[top.MediaType] || !digestRE.MatchString(top.Digest) || top.Size <= 0 || top.Size > s.maxBinary {
		return r, errors.New("unexpected image layer")
	}
	r.URL = serverRegistry + "/v2/" + serverRepository + "/blobs/" + top.Digest
	r.SHA256 = strings.TrimPrefix(top.Digest, "sha256:")
	r.bearer = token
	return r, nil
}
