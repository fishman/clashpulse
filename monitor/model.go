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

type StreamOutcome string

const (
	StreamSuccess   StreamOutcome = "success"
	StreamReset     StreamOutcome = "reset"
	StreamStall     StreamOutcome = "stall"
	StreamTruncated StreamOutcome = "truncated"
	StreamTLS       StreamOutcome = "tls"
	StreamTimeout   StreamOutcome = "timeout"
	StreamError     StreamOutcome = "error"
)

// StreamSample is one HTTPS pull through one node. It is keyed by node name,
// not group, because one probe listener serves every group.
type StreamSample struct {
	Proxy, URL          string
	FinishedAt          time.Time
	Bytes               int64
	FirstByte, Duration time.Duration
	Outcome             StreamOutcome
}

func (s StreamSample) Completed() bool { return s.Outcome == StreamSuccess }

// Throughput is bytes per second over the whole pull; 0 when unmeasurable.
func (s StreamSample) Throughput() int64 {
	if s.Duration <= 0 {
		return 0
	}
	return int64(float64(s.Bytes) / s.Duration.Seconds())
}

type StreamStability struct {
	Proxy              string
	Samples, Completed int
	MedianThroughput   int64
}

type Decision struct {
	Switch       bool
	LowerLatency bool
	Group        string
	Old          string
	New          string
	Reason       string
	Evidence     []Sample
	Streams      []StreamSample
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
	FinishedAt      time.Time
	Samples         []Sample
	Streams         []StreamSample
	Decisions       []Decision
	Alerts          []AlertEvent
	Stability       []ProxyStability
	StreamStability []StreamStability
}

// ProxyStability is one proxy's probe window in a group after a round.
type ProxyStability struct {
	Group     string
	Proxy     string
	Stability Stability
}
