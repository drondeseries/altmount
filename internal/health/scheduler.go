package health

import (
	"math/rand"
	"time"

	"github.com/kipsilabs/altmount/internal/config"
)

// Tier jitter spreads bulk imports so files posted together don't recheck in
// lockstep. Fresh jitter is small (checks are already frequent); aged jitter
// is a full day (weekly checks would otherwise thunder every Monday).
const (
	recheckFreshJitter = 15 * time.Minute
	recheckEarlyJitter = 1 * time.Hour
	recheckMidJitter   = 2 * time.Hour
	recheckAgedJitter  = 24 * time.Hour
)

// calculateInitialCheck calculates the first check time for a newly discovered file
func calculateInitialCheck() time.Time {
	// Spread initial checks over the next 24 hours to avoid thundering herd on bulk imports
	jitterMinutes := rand.Intn(1440)
	return time.Now().UTC().Add(time.Duration(jitterMinutes) * time.Minute)
}

// calculateInitialCheckForNewFile calculates the first check time for a freshly imported file.
// Uses a short jitter (0–5 minutes) to spread concurrent imports without delaying corruption detection.
func calculateInitialCheckForNewFile() time.Time {
	jitterMinutes := rand.Intn(5)
	return time.Now().UTC().Add(time.Duration(jitterMinutes) * time.Minute)
}

// CalculateNextCheck calculates the next check time after a successful health check.
// Tiers are decay-shaped by NZB age: fresh files recheck every couple of hours
// (takedowns + propagation delays cluster in the first days), aged files weekly
// (retention drift is slow but real). A zero schedule resolves to defaults.
// Negative/zero age (clock skew, backdated NZBs) is treated as fresh.
func CalculateNextCheck(releaseDate, lastCheck time.Time, sched ...config.RecheckSchedule) time.Time {
	s := config.DefaultRecheckSchedule()
	if len(sched) > 0 {
		s = sched[0]
	}

	age := lastCheck.Sub(releaseDate) // Age at the time of the last successful check
	if age < 0 {
		age = 0
	}

	var interval time.Duration
	var jitter time.Duration
	switch {
	case age < s.FreshAge:
		interval, jitter = s.Fresh, recheckFreshJitter
	case age < s.EarlyAge:
		interval, jitter = s.Early, recheckEarlyJitter
	case age < s.MidAge:
		interval, jitter = s.Mid, recheckMidJitter
	default:
		interval, jitter = s.Aged, recheckAgedJitter
	}

	// Symmetric jitter: interval ± jitter, so a 2h tier spreads over 1h45–2h15.
	if jitter > 0 {
		interval += time.Duration(rand.Int63n(int64(2*jitter))) - jitter
	}

	// Ensure the interval is at least the configured minimum
	if interval < s.MinInterval {
		interval = s.MinInterval
	}

	return lastCheck.Add(interval)
}
