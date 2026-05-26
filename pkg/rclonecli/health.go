package rclonecli

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// HealthCheck performs comprehensive health checks on the rclone system
func (m *Manager) HealthCheck(ctx context.Context) error {
	if !m.serverStarted {
		return fmt.Errorf("rclone RC server is not started")
	}

	if !m.IsReady() {
		return fmt.Errorf("rclone RC server is not ready")
	}

	// Check if we can communicate with the server
	if !m.pingServer() {
		return fmt.Errorf("rclone RC server is not responding")
	}

	// Check mounts health
	m.mountsMutex.RLock()
	unhealthyMounts := make([]string, 0)
	for provider, mount := range m.mounts {
		if mount.Mounted && !m.checkMountHealth(provider) {
			unhealthyMounts = append(unhealthyMounts, provider)
		}
	}
	m.mountsMutex.RUnlock()

	if len(unhealthyMounts) > 0 {
		return fmt.Errorf("unhealthy mounts detected: %v", unhealthyMounts)
	}

	return nil
}

// checkMountHealth checks if a specific mount is healthy
func (m *Manager) checkMountHealth(provider string) bool {
	// Try to list the root directory of the mount
	req := RCRequest{
		Command: "operations/list",
		Args: map[string]any{
			"fs":     fmt.Sprintf("%s:", provider),
			"remote": "",
		},
	}

	_, err := m.makeRequest(req, true)
	return err == nil
}

// RecoverMount attempts to recover a failed mount
func (m *Manager) RecoverMount(ctx context.Context, provider string) error {
	m.mountsMutex.RLock()
	mountInfo, exists := m.mounts[provider]
	m.mountsMutex.RUnlock()

	if !exists {
		return fmt.Errorf("mount for provider %s does not exist", provider)
	}

	m.logger.WarnContext(ctx, "Attempting to recover mount", "provider", provider)

	// Pre-recovery rcd liveness probe. If the rcd subprocess has wedged,
	// every subsequent RPC (mount/unmount, config/create, mount/mount) will
	// hang on context deadline exceeded. Kill+respawn rcd before issuing
	// recovery RPCs to break out of that wedge.
	if !m.pingServerWithTimeout(ctx, 5*time.Second) {
		m.logger.WarnContext(ctx, "rcd unresponsive during recovery, restarting subprocess", "provider", provider)
		if err := m.restartServer(ctx); err != nil {
			return fmt.Errorf("failed to restart wedged rcd: %w", err)
		}
		// After restart there is nothing to RC-unmount; skip straight to Mount,
		// which will recreate the rclone config and FUSE mount on the fresh rcd.
		if err := m.Mount(ctx, provider, mountInfo.LocalPath, mountInfo.WebDAVURL); err != nil {
			return fmt.Errorf("failed to recover mount for %s after rcd restart: %w", provider, err)
		}
		m.logger.InfoContext(ctx, "Successfully recovered mount after rcd restart", "provider", provider)
		m.mountsMutex.Lock()
		mountInfo.consecutiveFailures = 0
		m.mountsMutex.Unlock()
		return nil
	}

	// Ensure target mount is thoroughly cleaned up and the kernel is not busy
	m.logger.InfoContext(ctx, "Forcing cleanup of stale FUSE mount point before recovery remount", "provider", provider)
	_ = m.unmount(ctx, provider)
	_ = m.forceUnmountPath(mountInfo.LocalPath)

	// Wait longer to allow the kernel FUSE driver to release resources
	time.Sleep(3 * time.Second)

	// Try to remount
	if err := m.Mount(ctx, provider, mountInfo.LocalPath, mountInfo.WebDAVURL); err != nil {
		// If mount times out (deadline exceeded or timeout in error string), force a server subprocess restart
		if ctx.Err() != nil || (err != nil && (strings.Contains(err.Error(), "deadline exceeded") || strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "Timeout"))) {
			m.logger.WarnContext(ctx, "Mount timed out / wedged in kernel, performing hard rcd subprocess restart", "provider", provider)
			if rErr := m.restartServer(ctx); rErr != nil {
				return fmt.Errorf("failed to recover mount for %s: %w (rcd restart failed: %v)", provider, err, rErr)
			}
			// After restart, try to Mount again on the fresh process
			if err2 := m.Mount(ctx, provider, mountInfo.LocalPath, mountInfo.WebDAVURL); err2 != nil {
				return fmt.Errorf("failed to recover mount for %s after rcd restart: %w", provider, err2)
			}
			m.logger.InfoContext(ctx, "Successfully recovered mount after rcd restart", "provider", provider)
			m.mountsMutex.Lock()
			mountInfo.consecutiveFailures = 0
			m.mountsMutex.Unlock()
			return nil
		}
		return fmt.Errorf("failed to recover mount for %s: %w", provider, err)
	}

	m.logger.InfoContext(ctx, "Successfully recovered mount", "provider", provider)
	m.mountsMutex.Lock()
	mountInfo.consecutiveFailures = 0
	m.mountsMutex.Unlock()
	return nil
}

// MonitorMounts continuously monitors mount health and attempts recovery
func (m *Manager) MonitorMounts(ctx context.Context) {
	if !m.serverStarted {
		return
	}

	ticker := time.NewTicker(30 * time.Second) // Check every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.logger.DebugContext(ctx, "Mount monitoring stopped")
			return
		case <-ticker.C:
			m.performMountHealthCheck()
		}
	}
}

// performMountHealthCheck checks and attempts to recover unhealthy mounts
func (m *Manager) performMountHealthCheck() {
	if !m.IsReady() {
		return
	}

	// IsReady() only reflects startup state. Probe the rcd subprocess with a
	// bounded timeout so a wedged rcd is detected even when no individual
	// mount has failed yet.
	if !m.pingServerWithTimeout(m.ctx, 5*time.Second) {
		m.logger.WarnContext(m.ctx, "rcd unresponsive during health check, restarting subprocess")
		if err := m.restartServer(m.ctx); err != nil {
			m.logger.ErrorContext(m.ctx, "Failed to restart wedged rcd", "err", err)
			return
		}
		// restartServer marked all mounts as unmounted. Re-establish each one
		// against the fresh rcd; each Mount call is independent.
		m.mountsMutex.RLock()
		toRemount := make([]*MountInfo, 0, len(m.mounts))
		for _, mount := range m.mounts {
			toRemount = append(toRemount, mount)
		}
		m.mountsMutex.RUnlock()

		for _, mount := range toRemount {
			info := mount
			go func() {
				if err := m.Mount(m.ctx, info.Provider, info.LocalPath, info.WebDAVURL); err != nil {
					m.logger.ErrorContext(m.ctx, "Failed to remount after rcd restart", "err", err, "provider", info.Provider)
				}
			}()
		}
		// Don't fall through to per-mount recovery on this tick; remounts are
		// in flight and the next tick will assess health.
		return
	}

	m.mountsMutex.RLock()
	providers := make([]string, 0, len(m.mounts))
	for provider, mount := range m.mounts {
		if mount.Mounted {
			providers = append(providers, provider)
		}
	}
	m.mountsMutex.RUnlock()

	for _, provider := range providers {
		if !m.checkMountHealth(provider) {
			m.mountsMutex.Lock()
			mount, exists := m.mounts[provider]
			var failures int
			if exists {
				mount.consecutiveFailures++
				failures = mount.consecutiveFailures
			}
			m.mountsMutex.Unlock()

			m.logger.WarnContext(m.ctx, "Mount health check failed", "provider", provider, "consecutive_failures", failures)

			if failures >= 3 {
				m.logger.ErrorContext(m.ctx, "Mount health check failed 3 consecutive times, initiating recovery", "provider", provider)

				// Mark mount as unhealthy
				m.mountsMutex.Lock()
				if exists {
					mount.Error = "Health check failed (3 consecutive failures)"
					mount.Mounted = false
				}
				m.mountsMutex.Unlock()

				// Attempt recovery
				go func(provider string) {
					if err := m.RecoverMount(m.ctx, provider); err != nil {
						m.logger.ErrorContext(m.ctx, "Failed to recover mount", "err", err, "provider", provider)
					}
				}(provider)
			}
		} else {
			// Reset failures on success
			m.mountsMutex.Lock()
			if mount, exists := m.mounts[provider]; exists {
				mount.consecutiveFailures = 0
			}
			m.mountsMutex.Unlock()
		}
	}
}
