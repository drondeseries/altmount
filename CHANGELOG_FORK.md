# AltMount Advanced Edition - Fork Changelog

This document tracks the unique enhancements and fixes available in the `drondeseries/altmount` fork compared to the upstream `javi11/altmount`.

## 🚀 Advanced ARR Integration (Sonarr/Radarr)
- **Persistent Import History:** Keeps a permanent record of successful imports even after they are removed from the active queue. Includes configurable retention days.
- **Queue Visibility Engine:** Recently completed items are automatically injected into global queue polls. This prevents "missing imports" when Sonarr polls after a job has finished but before it was processed.
- **Full Path Reporting:** SABnzbd API now reports the absolute file path for SYMLINK and STRM strategies, allowing ARRs to "force" imports even if VFS caches are slightly behind.
- **Smart History Deletion:** Explicitly ignores "delete files" commands from ARR instances to protect the virtual library while correctly logging the requests for troubleshooting.
- **Safe Upgrade Logic:** Automatically detects ARR "Upgrade" events and surgically removes old metadata/links to prevent ghost files in the mount.
- **Registration Reliability:** Prioritizes the configured `webhook_base_url` for all automatic ARR registrations, ensuring reachable callbacks in complex network setups.

## 🛠 Library Maintenance & Recovery
- **Smart Library Regeneration:** A powerful maintenance tool that reconciles the database with the physical disk.
    - Supports both **SYMLINK** and **STRM** strategies.
    - Detects and fixes "recovered" paths pointing to the raw mount.
    - Verifies physical file existence and recreates missing links on-the-fly.
- **Automatic Path Stripping:** Intelligently handles `complete_dir` prefixes to ensure library paths are always clean and relative to the intended import directory.

## 🛡 Streaming Protection & Performance
- **Streaming Failure Protection:** Monitors for playback errors and automatically triggers an ARR "Repair Search" if a file hits a configurable failure threshold.
- **Smart Zero-Filling:** Dynamically replaces minor missing Usenet segments with zeros to prevent media player crashes or freezes during streaming.
- **Magic Byte Detection:** Improved file type identification (RAR, 7z, PAR2) using actual file headers rather than unreliable extensions.
- **WebDAV PUT Support:** Added support for HTTP PUT operations, enabling sidecar file writes and updates directly from rclone.

## 🖥 UI & UX Improvements
- **Maintenance Hub:** New maintenance actions dropdown on the Health page.
- **Advanced Config Tooltips:** Comprehensive documentation tooltips for every advanced setting in the dashboard.
- **Persistent Settings:** Fixed issues where data verification and failure masking settings wouldn't persist correctly in the UI.
