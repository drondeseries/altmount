package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEffectiveLibraryAutoSave(t *testing.T) {
	require.Equal(t, "off", Config{}.EffectiveLibraryAutoSave())
	require.Equal(t, "stremio", Config{Library: LibraryConfig{AutoSave: "stremio"}}.EffectiveLibraryAutoSave())
	require.Equal(t, "arrs", Config{Library: LibraryConfig{AutoSave: " ARRS "}}.EffectiveLibraryAutoSave())
	require.Equal(t, "all", Config{Library: LibraryConfig{AutoSave: "all"}}.EffectiveLibraryAutoSave())
	require.Equal(t, "off", Config{Library: LibraryConfig{AutoSave: "bogus"}}.EffectiveLibraryAutoSave())
	// Invalid new-section value beats a valid legacy one (explicit beats inherited).
	require.Equal(t, "off", Config{
		Library: LibraryConfig{AutoSave: "bogus"},
		Stremio: StremioConfig{LibraryAutoSave: "all"},
	}.EffectiveLibraryAutoSave())
	// Legacy Stremio-level values are honored when the library section is unset.
	require.Equal(t, "all", Config{
		Stremio: StremioConfig{LibraryAutoSave: "all"},
	}.EffectiveLibraryAutoSave())
	// New section wins over legacy.
	require.Equal(t, "stremio", Config{
		Library: LibraryConfig{AutoSave: "stremio"},
		Stremio: StremioConfig{LibraryAutoSave: "all"},
	}.EffectiveLibraryAutoSave())
}

func TestEffectiveLibraryAutoImportHealth(t *testing.T) {
	require.False(t, Config{}.EffectiveLibraryAutoImportHealth())
	off := false
	on := true
	require.True(t, Config{Library: LibraryConfig{AutoImportHealth: &on}}.EffectiveLibraryAutoImportHealth())
	require.False(t, Config{Library: LibraryConfig{AutoImportHealth: &off}}.EffectiveLibraryAutoImportHealth())
	require.True(t, Config{
		Stremio: StremioConfig{LibraryAutoImportHealth: &on},
	}.EffectiveLibraryAutoImportHealth())
	require.False(t, Config{
		Library: LibraryConfig{AutoImportHealth: &off},
		Stremio: StremioConfig{LibraryAutoImportHealth: &on},
	}.EffectiveLibraryAutoImportHealth())
}

func TestEffectiveLibraryRepairIntervalMinutes(t *testing.T) {
	require.Equal(t, 10, Config{}.EffectiveLibraryRepairIntervalMinutes())
	thirty := 30
	ten := 10
	zero := 0
	neg := -5
	huge := 2000
	require.Equal(t, 30, Config{Library: LibraryConfig{RepairIntervalMinutes: &thirty}}.EffectiveLibraryRepairIntervalMinutes())
	require.Equal(t, 10, Config{Library: LibraryConfig{RepairIntervalMinutes: &ten}}.EffectiveLibraryRepairIntervalMinutes())
	require.Equal(t, 0, Config{Library: LibraryConfig{RepairIntervalMinutes: &zero}}.EffectiveLibraryRepairIntervalMinutes())
	require.Equal(t, 10, Config{Library: LibraryConfig{RepairIntervalMinutes: &neg}}.EffectiveLibraryRepairIntervalMinutes())
	require.Equal(t, 1440, Config{Library: LibraryConfig{RepairIntervalMinutes: &huge}}.EffectiveLibraryRepairIntervalMinutes())
}
