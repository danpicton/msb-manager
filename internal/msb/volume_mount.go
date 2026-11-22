package msb

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
)

const volumeDiskFile = "disk.raw"

func (c *Client) volumeMountArgs(ctx context.Context, mount VolumeMount, restore bool) ([]string, error) {
	volumes, err := c.VolumeList(ctx)
	if err != nil {
		return nil, err
	}
	for _, volume := range volumes {
		if volume.Name != mount.Name || volume.Kind != "disk" {
			continue
		}
		if !restore {
			return []string{"--mount-named", fmt.Sprintf("%s:%s:kind=disk,size=%d", mount.Name, mount.Mount, volume.QuotaMiB)}, nil
		}
		// The restore CLI's -v shorthand only provisions directory volumes.
		// Map the existing managed disk through restore's explicit disk option.
		paths, err := c.volumeDiskPaths(ctx)
		if err != nil {
			return nil, err
		}
		diskPath, ok := paths[mount.Name]
		if !ok {
			return nil, ErrVolumeNotFound
		}
		return []string{"--mount-disk", diskPath + ":" + mount.Mount + ":format=raw,fstype=ext4"}, nil
	}
	return []string{"-v", mount.Name + ":" + mount.Mount}, nil
}

// volumeDiskPaths derives disk paths from the configured backend's storage
// report. The CLI stores each managed disk as disk.raw in its volume directory.
func (c *Client) volumeDiskPaths(ctx context.Context) (map[string]string, error) {
	stdout, stderr, err := c.run(ctx, "df", "--format", "json")
	if err != nil {
		return nil, wrapRunErr(stderr, err)
	}
	var report struct {
		Volumes struct {
			Items []struct{ Name, Path string }
		}
	}
	if err := json.Unmarshal(stdout, &report); err != nil {
		return nil, fmt.Errorf("parse msb storage usage: %w", err)
	}
	paths := make(map[string]string, len(report.Volumes.Items))
	for _, volume := range report.Volumes.Items {
		if ValidName(volume.Name) && filepath.IsAbs(volume.Path) {
			paths[volume.Name] = filepath.Join(volume.Path, volumeDiskFile)
		}
	}
	return paths, nil
}

// identifyDiskMounts recovers a restored volume's identity from its disk path
// so startup reconciliation and subsequent starts claim the named volume.
func (c *Client) identifyDiskMounts(ctx context.Context, detail *SandboxDetail) error {
	groups := [][]Mount{detail.Mounts}
	if detail.ActiveMounts != nil {
		groups = append(groups, *detail.ActiveMounts)
	}
	managedDiskCandidate := func(m Mount) bool { return filepath.Base(m.DiskPath) == volumeDiskFile }
	if !slices.ContainsFunc(groups, func(mounts []Mount) bool {
		return slices.ContainsFunc(mounts, managedDiskCandidate)
	}) {
		return nil
	}
	paths, err := c.volumeDiskPaths(ctx)
	if err != nil {
		return err
	}
	for _, mounts := range groups {
		for i := range mounts {
			mount := &mounts[i]
			name := filepath.Base(filepath.Dir(mount.DiskPath))
			if diskPath, exists := paths[name]; exists && diskPath == filepath.Clean(mount.DiskPath) {
				mount.Type = "Named"
				mount.Name = name
			}
		}
	}
	return nil
}
