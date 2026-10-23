package api

import (
	"os"
	"testing"
	"time"

	"github.com/kipsilabs/altmount/internal/config"
	"github.com/stretchr/testify/require"
)

func intPtr(v int) *int { return &v }

// TestParseLibraryRepairIntervalMinutes covers the operator override parser:
// trimming, explicit disable (0), and out-of-range rejection.
func TestParseLibraryRepairIntervalMinutes(t *testing.T) {
	minutes, valid := parseLibraryRepairIntervalMinutes("15")
	require.True(t, valid)
	require.Equal(t, 15, minutes)

	minutes, valid = parseLibraryRepairIntervalMinutes(" 60 ")
	require.True(t, valid)
	require.Equal(t, 60, minutes)

	minutes, valid = parseLibraryRepairIntervalMinutes("0")
	require.True(t, valid)
	require.Equal(t, 0, minutes)

	_, valid = parseLibraryRepairIntervalMinutes("-3")
	require.False(t, valid)

	_, valid = parseLibraryRepairIntervalMinutes("fish")
	require.False(t, valid)

	_, valid = parseLibraryRepairIntervalMinutes("2000")
	require.False(t, valid)
}

// TestLibraryRepairIntervalResolution covers precedence: a valid operator
// override wins over configuration, invalid overrides warn and fall back to
// configuration (never silently disable), and configuration defaults/clamps
// apply when the override is unset.
func TestLibraryRepairIntervalResolution(t *testing.T) {
	envName := "ALTMOUNT_LIBRARY_REPAIR_INTERVAL_MINUTES"
	orig, had := os.LookupEnv(envName)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(envName, orig)
		} else {
			_ = os.Unsetenv(envName)
		}
	})

	cfgWith := func(minutes *int) *Server {
		cfg := &config.Config{}
		if minutes != nil {
			cfg.Library.RepairIntervalMinutes = minutes
		}
		return &Server{configManager: &mockConfigManager{cfg: cfg}}
	}

	// Unset override + unset configuration → default 10 minutes.
	require.NoError(t, os.Unsetenv(envName))
	interval, enabled := cfgWith(nil).libraryRepairInterval()
	require.True(t, enabled)
	require.Equal(t, 10*time.Minute, interval)

	// Valid override beats configuration.
	require.NoError(t, os.Setenv(envName, "3"))
	interval, enabled = cfgWith(intPtr(30)).libraryRepairInterval()
	require.True(t, enabled)
	require.Equal(t, 3*time.Minute, interval)

	// Override 0 explicitly disables, even with a configured cadence.
	require.NoError(t, os.Setenv(envName, "0"))
	_, enabled = cfgWith(intPtr(30)).libraryRepairInterval()
	require.False(t, enabled)

	// Non-integer override reports invalid and falls back to configuration.
	require.NoError(t, os.Setenv(envName, "fish"))
	interval, enabled = cfgWith(intPtr(30)).libraryRepairInterval()
	require.True(t, enabled)
	require.Equal(t, 30*time.Minute, interval)

	// Out-of-range override reports invalid and falls back to configuration.
	require.NoError(t, os.Setenv(envName, "5000"))
	interval, enabled = cfgWith(intPtr(30)).libraryRepairInterval()
	require.True(t, enabled)
	require.Equal(t, 30*time.Minute, interval)

	// A negative override is invalid (not a disable request): it is reported
	// and resolves to the configured cadence.
	require.NoError(t, os.Setenv(envName, "-7"))
	interval, enabled = cfgWith(intPtr(30)).libraryRepairInterval()
	require.True(t, enabled)
	require.Equal(t, 30*time.Minute, interval)

	require.NoError(t, os.Unsetenv(envName))
	_, enabled = cfgWith(intPtr(0)).libraryRepairInterval()
	require.False(t, enabled)

	interval, enabled = cfgWith(intPtr(-5)).libraryRepairInterval()
	require.True(t, enabled)
	require.Equal(t, 10*time.Minute, interval)

	interval, enabled = cfgWith(intPtr(2000)).libraryRepairInterval()
	require.True(t, enabled)
	require.Equal(t, time.Duration(config.MaxLibraryRepairIntervalMinutes)*time.Minute, interval)

	// A nil configManager (test wiring) fails safe to the default cadence.
	interval, enabled = (&Server{}).libraryRepairInterval()
	require.True(t, enabled)
	require.Equal(t, 10*time.Minute, interval)
}
