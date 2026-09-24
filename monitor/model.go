package monitor

import "time"

type Outcome uint8

const (
	OutcomeError Outcome = iota
	OutcomeSuccess
	OutcomeTimeout
)

type Sample struct {
	Group      string
	Proxy      string
	URL        string
	FinishedAt time.Time
	Latency    time.Duration
	Outcome    Outcome
}

type Decision struct {
	Switch   bool
	Old      string
	New      string
	Reason   string
	Evidence []Sample
}

type GroupState struct {
	Group             string
	Selected          string
	Proxies           []string
	ProbeEnabled      bool
	AutomationEnabled bool
	ManualOverride    bool
	SwitchInFlight    bool
	LastSwitchAt      time.Time
}

type AlertState struct {
	HighLatency bool
}

type AlertTransition uint8

const (
	AlertNoChange AlertTransition = iota
	AlertHighLatency
	AlertRecovered
)

type AlertEvent struct {
	Transition AlertTransition
	Group      string
	Threshold  time.Duration
	Evidence   []Sample
}

type Batch struct {
	FinishedAt time.Time
	Samples    []Sample
	Decisions  []Decision
	Alerts     []AlertEvent
}
