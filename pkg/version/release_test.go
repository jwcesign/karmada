package version

import (
	"testing"
)

func TestReleaseVersion(t *testing.T) {
	tests := []struct {
		name               string
		gitVersion         string
		expectPatchRelease string
		expectError        bool
	}{
		{
			name:               "first minor release",
			gitVersion:         "v1.1.0",
			expectPatchRelease: "v1.1.0",
			expectError:        false,
		},
		{
			name:               "subsequent minor release",
			gitVersion:         "v1.1.1",
			expectPatchRelease: "v1.1.1",
			expectError:        false,
		},
		{
			name:               "normal git version",
			gitVersion:         "v1.1.1-6-gf20c721a",
			expectPatchRelease: "v1.1.1",
			expectError:        false,
		},
		{
			name:        "abnormal version",
			gitVersion:  "vx.y.z-6-gf20c721a",
			expectError: true,
		},
		{
			name:               "prerelease alpha version",
			gitVersion:         "v1.7.0-alpha.1-3-gf20c721a",
			expectPatchRelease: "v1.7.0-alpha.1",
			expectError:        false,
		},
	}

	for i := range tests {
		tc := tests[i]
		t.Run(tc.name, func(t *testing.T) {
			rv, err := ParseGitVersion(tc.gitVersion)
			if err != nil {
				if !tc.expectError {
					t.Fatalf("No error is expected but got: %v", err)
				}
				// Stop and passes this test as error is expected.
				return
			} else if err == nil {
				if tc.expectError {
					t.Fatalf("Expect error, but got nil")
				}
			}

			if rv.ReleaseVersion() != tc.expectPatchRelease {
				t.Fatalf("expect patch release: %s, but got: %s", tc.expectPatchRelease, rv.ReleaseVersion())
			}
		})
	}
}

func TestReleaseVersion_PatchRelease(t *testing.T) {
	tests := []struct {
		name        string
		gitVersion  string
		ignoreError bool
		expect      string
	}{
		{
			name:        "invalid version should dump nil",
			gitVersion:  "",
			ignoreError: true,
			expect:      "<nil>",
		},
		{
			name:        "standard semantic version",
			gitVersion:  "v1.2.1",
			ignoreError: false,
			expect:      "v1.2.1",
		},
		{
			name:        "standard semantic version suffixed with commits",
			gitVersion:  "v1.2.3-12-g2e860210",
			ignoreError: false,
			expect:      "v1.2.3",
		},
	}

	for i := range tests {
		tc := tests[i]
		t.Run(tc.name, func(t *testing.T) {
			rv, err := ParseGitVersion(tc.gitVersion)
			if err != nil {
				if tc.ignoreError {
					// initialize to avoid panic because just focus on the version inside ReleaseVersion.
					rv = &ReleaseVersion{Version: nil}
				} else {
					t.Fatalf("unexpected error: %v", err)
				}
			}

			if rv.ReleaseVersion() != tc.expect {
				t.Fatalf("expect patch release: %s, but got: %s", tc.expect, rv.ReleaseVersion())
			}
		})
	}
}
