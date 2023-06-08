package version

import (
	"regexp"

	utilversion "k8s.io/apimachinery/pkg/util/version"
)

// ReleaseVersion represents a released version.
type ReleaseVersion struct {
	*utilversion.Version
}

// ParseGitVersion parses a git version string, such as:
// - v1.1.0-73-g7e6d4f69
// - v1.1.0
func ParseGitVersion(gitVersion string) (*ReleaseVersion, error) {
	formattedVersion := removeGitVersionCommits(gitVersion)
	v, err := utilversion.ParseSemantic(formattedVersion)
	if err != nil {
		return nil, err
	}

	return &ReleaseVersion{
		Version: v,
	}, nil
}

// ReleaseVersion returns the current version with format "vx.y.z".
// It could be patch release or pre-release
func (r *ReleaseVersion) ReleaseVersion() string {
	if r.Version == nil {
		return "<nil>"
	}

	return r.String()
}

// removeGitVersionCommits removes the git commit info from the version
// The git version looks like: v1.0.4-14-g2414721
// the current head of my "parent" branch is based on v1.0.4,
// but since it has a few commits on top of that, describe has added the number of additional commits ("14")
// and an abbreviated object name for the commit itself ("2414721") at the end.
func removeGitVersionCommits(gitVersion string) string {
	// This match the commit info part of the git version
	splitRE := regexp.MustCompile("-[0-9]+-g[0-9a-z]{7}")
	match := splitRE.Split(gitVersion, 2)

	return match[0]
}
