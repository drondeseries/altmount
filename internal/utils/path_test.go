package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveEmptyDirs(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "altmount-test-remove-dirs")
	err := os.MkdirAll(tempDir, 0755)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	root := filepath.Join(tempDir, "root")
	err = os.MkdirAll(root, 0755)
	if err != nil {
		t.Fatal(err)
	}

	// Create nested empty directories: root/a/b/c
	nested := filepath.Join(root, "a", "b", "c")
	err = os.MkdirAll(nested, 0755)
	if err != nil {
		t.Fatal(err)
	}

	// Remove c, and expect b and a to be removed too
	RemoveEmptyDirs(root, nested)

	// Check if a, b, c were removed
	for _, dir := range []string{"a", "a/b", "a/b/c"} {
		path := filepath.Join(root, dir)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("Expected directory %s to be removed, but it exists", path)
		}
	}

	// Check if root still exists
	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Error("Expected root directory to exist, but it was removed")
	}

	// Test with non-empty directory
	// root/x/y/z, with root/x/keep.txt
	xDir := filepath.Join(root, "x")
	yDir := filepath.Join(xDir, "y")
	zDir := filepath.Join(yDir, "z")
	err = os.MkdirAll(zDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	keepFile := filepath.Join(xDir, "keep.txt")
	err = os.WriteFile(keepFile, []byte("keep"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Remove z, and expect y to be removed, but x should stay
	RemoveEmptyDirs(root, zDir)

	if _, err := os.Stat(zDir); err == nil {
		t.Error("Expected zDir to be removed")
	}
	if _, err := os.Stat(yDir); err == nil {
		t.Error("Expected yDir to be removed")
	}
	if _, err := os.Stat(xDir); os.IsNotExist(err) {
		t.Error("Expected xDir to still exist because it contains keep.txt")
	}
	if _, err := os.Stat(keepFile); os.IsNotExist(err) {
		t.Error("Expected keep.txt to still exist")
	}
}

func TestGetRelativePath(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		baseDirs   []string
		expected   string
	}{
		{
			name:     "no matching prefixes, returns relative",
			target:   "tv/show/episode.mkv",
			baseDirs: []string{"/mnt/disk1/cloud/altmount"},
			expected: "tv/show/episode.mkv",
		},
		{
			name:     "matches single prefix",
			target:   "/mnt/disk1/cloud/altmount/tv/show/episode.mkv",
			baseDirs: []string{"/mnt/disk1/cloud/altmount"},
			expected: "tv/show/episode.mkv",
		},
		{
			name:     "longest prefix wins: temp uploads inside mount path",
			target:   "/mnt/disk1/cloud/altmount/tmp/altmount-uploads/tvHQ/show/episode.mkv",
			baseDirs: []string{"/mnt/disk1/cloud/altmount", "/mnt/disk1/cloud/altmount/tmp/altmount-uploads"},
			expected: "tvHQ/show/episode.mkv",
		},
		{
			name:     "exact match with prefix",
			target:   "/mnt/disk1/cloud/altmount",
			baseDirs: []string{"/mnt/disk1/cloud/altmount"},
			expected: "",
		},
		{
			name:     "trailing slash handled gracefully",
			target:   "/mnt/disk1/cloud/altmount/tvHQ/episode.mkv",
			baseDirs: []string{"/mnt/disk1/cloud/altmount/"},
			expected: "tvHQ/episode.mkv",
		},
		{
			name:     "empty base dirs are ignored",
			target:   "/mnt/disk1/cloud/altmount/tvHQ/episode.mkv",
			baseDirs: []string{"", "/mnt/disk1/cloud/altmount"},
			expected: "tvHQ/episode.mkv",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := GetRelativePath(tt.target, tt.baseDirs...)
			if actual != tt.expected {
				t.Errorf("GetRelativePath(%q, %v) = %q, want %q", tt.target, tt.baseDirs, actual, tt.expected)
			}
		})
	}
}

