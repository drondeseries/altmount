package health

import (
	"testing"
	"time"

	"github.com/kipsilabs/altmount/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalculateNextCheck_Tiers(t *testing.T) {
	now := time.Now().UTC()
	sched := config.DefaultRecheckSchedule()
	tests := []struct {
		name     string
		age      time.Duration
		wantTier time.Duration
		jitter   time.Duration
	}{
		{"fresh 1h", 1 * time.Hour, 2 * time.Hour, recheckFreshJitter},
		{"fresh boundary 71h", 71 * time.Hour, 2 * time.Hour, recheckFreshJitter},
		{"exact fresh boundary 72h falls to early", 72 * time.Hour, 12 * time.Hour, recheckEarlyJitter},
		{"early 73h", 73 * time.Hour, 12 * time.Hour, recheckEarlyJitter},
		{"early 6d", 6 * 24 * time.Hour, 12 * time.Hour, recheckEarlyJitter},
		{"exact early boundary 7d falls to mid", 7 * 24 * time.Hour, 24 * time.Hour, recheckMidJitter},
		{"mid 8d", 8 * 24 * time.Hour, 24 * time.Hour, recheckMidJitter},
		{"mid 29d", 29 * 24 * time.Hour, 24 * time.Hour, recheckMidJitter},
		{"exact mid boundary 30d falls to aged", 30 * 24 * time.Hour, 7 * 24 * time.Hour, recheckAgedJitter},
		{"aged 31d", 31 * 24 * time.Hour, 7 * 24 * time.Hour, recheckAgedJitter},
		{"aged 1y", 365 * 24 * time.Hour, 7 * 24 * time.Hour, recheckAgedJitter},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := now.Add(-tt.age)
			next := CalculateNextCheck(release, now, sched)
			delta := next.Sub(now)
			assert.GreaterOrEqual(t, delta, tt.wantTier-tt.jitter,
				"next check should be within jitter below the tier interval")
			assert.Less(t, delta, tt.wantTier+tt.jitter,
				"next check should be within jitter above the tier interval")
		})
	}
}

func TestCalculateNextCheck_ZeroConfigDefaults(t *testing.T) {
	// A zero HealthRecheckConfig (unset) resolves to defaults via the accessor.
	cfg := config.DefaultConfig()
	sched := cfg.GetHealthRecheckSchedule()
	require.Equal(t, config.DefaultRecheckSchedule(), sched)
}

func TestCalculateNextCheck_FutureReleaseDate(t *testing.T) {
	now := time.Now().UTC()
	sched := config.DefaultRecheckSchedule()
	// Clock skew / backdated NZB: treated as fresh, never scheduled in the past.
	next := CalculateNextCheck(now.Add(time.Hour), now, sched)
	assert.True(t, next.After(now), "next check must be in the future")
}

func TestCalculateNextCheck_MinFloor(t *testing.T) {
	now := time.Now().UTC()
	sched := config.DefaultRecheckSchedule()
	sched.Fresh = time.Minute
	sched.MinInterval = 30 * time.Minute
	next := CalculateNextCheck(now.Add(-time.Hour), now, sched)
	assert.GreaterOrEqual(t, next.Sub(now), 30*time.Minute, "min floor must clamp tiny intervals")
}

func TestCalculateNextCheck_CustomSchedule(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Health.Recheck.FreshHours = 3
	cfg.Health.Recheck.AgedDays = 14
	sched := cfg.GetHealthRecheckSchedule()
	require.Equal(t, 3*time.Hour, sched.Fresh)
	require.Equal(t, 14*24*time.Hour, sched.Aged)

	now := time.Now().UTC()
	next := CalculateNextCheck(now.Add(-time.Hour), now, sched)
	delta := next.Sub(now)
	assert.GreaterOrEqual(t, delta, 3*time.Hour-recheckFreshJitter)
	assert.Less(t, delta, 3*time.Hour+recheckFreshJitter)
}
