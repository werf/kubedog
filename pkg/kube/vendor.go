package kube

import (
	"path/filepath"
	"regexp"
	"strings"

	"k8s.io/client-go/util/homedir"
)

// Code is taken from the k8s.io/cli-runtime/pkg/genericclioptions/config_flags.go
// because it is not publicly exposed by the package, but we need it to create
// compatible CachedDiscoveryClient for base64 config the same as for the default config loader.

var defaultCacheDir = filepath.Join(homedir.HomeDir(), ".kube", "cache")

// overlyCautiousIllegalFileCharacters matches characters that *might* not be supported.  Windows is really restrictive, so this is really restrictive
var overlyCautiousIllegalFileCharacters = regexp.MustCompile(`[^(\w/\.)]`)

// computeDiscoverCacheDir takes the parentDir and the host and comes up with a "usually non-colliding" name.
func computeDiscoverCacheDir(parentDir, host string) string {
	// strip the optional scheme from host if its there:
	schemelessHost := strings.Replace(strings.Replace(host, "https://", "", 1), "http://", "", 1)
	// now do a simple collapse of non-AZ09 characters.  Collisions are possible but unlikely.  Even if we do collide the problem is short lived
	safeHost := overlyCautiousIllegalFileCharacters.ReplaceAllString(schemelessHost, "_")
	return filepath.Join(parentDir, safeHost)
}
