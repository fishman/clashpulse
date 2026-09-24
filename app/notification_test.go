package app

import (
	"testing"
	"time"

	"github.com/fishman/clashpulse/core"
)

func TestAllMeasuredSlowRequiresEveryRecentSuccessfulProxy(t *testing.T) {
	now := time.Unix(1700000000, 0)
	groups := []core.GroupSnapshot{{ID: opaqueID("auto"), Type: "Selector", Proxies: []string{opaqueID("alpha"), opaqueID("beta")}}}
	proxies := []core.ProxySnapshot{
		{GroupID: opaqueID("auto"), ID: opaqueID("alpha"), Outcome: "success", LatencyMillis: 251, FinishedAt: now.Unix()},
		{GroupID: opaqueID("auto"), ID: opaqueID("beta"), Outcome: "success", LatencyMillis: 251, FinishedAt: now.Unix()},
	}
	if high, fast := allMeasuredSlow(groups, proxies, 250*time.Millisecond, now, 5*time.Minute); !high || fast {
		t.Fatalf("all high = %v, any fast = %v", high, fast)
	}
	proxies[1].LatencyMillis = 250
	if high, fast := allMeasuredSlow(groups, proxies, 250*time.Millisecond, now, 5*time.Minute); high || !fast {
		t.Fatalf("boundary high = %v, any fast = %v", high, fast)
	}
	proxies[1].Outcome, proxies[1].LatencyMillis = "timeout", 0
	if high, fast := allMeasuredSlow(groups, proxies, 250*time.Millisecond, now, 5*time.Minute); high || fast {
		t.Fatalf("timeout high = %v, any fast = %v", high, fast)
	}
	proxies[1].Outcome, proxies[1].LatencyMillis = "success", 301
	groups = append(groups, core.GroupSnapshot{ID: opaqueID("native"), Type: "URLTest", Proxies: []string{opaqueID("gamma")}})
	proxies = append(proxies, core.ProxySnapshot{GroupID: opaqueID("native"), ID: opaqueID("gamma"), Outcome: "success", LatencyMillis: 310, FinishedAt: now.Add(-3 * time.Minute).Unix()})
	if high, _ := allMeasuredSlow(groups, proxies, 250*time.Millisecond, now, 5*time.Minute); !high {
		t.Fatal("staggered recent URLTest sample was ignored")
	}
	proxies[2].FinishedAt = now.Add(-12 * time.Minute).Unix()
	if high, _ := allMeasuredSlow(groups, proxies, 250*time.Millisecond, now, 5*time.Minute); high {
		t.Fatal("stale URLTest sample claimed all connections were slow")
	}
}
