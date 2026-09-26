package resources

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/fishman/clashpulse/config"
)

// ResourceStatus reports committed identity without exposing a full source URL.
// LastFailure is process-local and cleared after a successful resource refresh.
type ResourceStatus struct {
	ID          string
	Kind        config.ResourceKind
	Format      config.ResourceFormat
	RuleType    config.RuleType
	SourceHost  string
	SHA256      string
	LastCheck   time.Time
	LastSuccess time.Time
	LastFailure string
	Enabled     bool
	Validated   bool
	Destination string
}

// Status reports committed resource identity without exposing full source URLs.
func (r *Registry) Status(snapshot config.Snapshot) ([]ResourceStatus, error) {
	if r == nil {
		return nil, fmt.Errorf("resources: nil registry")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, err := loadStateFile(filepath.Join(r.home, stateFileName))
	if err != nil {
		return nil, err
	}
	if current.Version != r.active.Version || current.CommitID != r.active.CommitID {
		return nil, fmt.Errorf("resources: active manifest changed outside registry")
	}
	statuses := make([]ResourceStatus, 0, len(snapshot.Resources))
	for _, resource := range snapshot.Resources {
		status := ResourceStatus{
			ID: resource.ID, Kind: resource.Kind, Format: resource.Format,
			RuleType: resource.RuleType, SourceHost: sourceHost(resource.URL),
			Enabled: resource.Enabled, LastFailure: r.failures[resource.ID],
		}
		state, exists := current.Resources[resource.ID]
		if exists {
			status.SHA256 = state.SHA256
			if state.LastCheckedUnix != 0 {
				status.LastCheck = time.Unix(state.LastCheckedUnix, 0).UTC()
			}
			if state.LastSuccessUnix != 0 {
				status.LastSuccess = time.Unix(state.LastSuccessUnix, 0).UTC()
			}
		}
		if resource.Enabled {
			path := filepath.Join(r.home, filename(resource))
			status.Destination = path
			data, readErr := readManaged(path, r.maxBytes)
			if readErr == nil && Validate(resource, data) == nil {
				sum := digest(data)
				pinMatches := resource.SHA256 == "" || strings.EqualFold(resource.SHA256, sum)
				metadataMatches := exists && state.SHA256 == sum && state.SourceHash == digest([]byte(resource.URL)) && resourceStateMatches(resource, state)
				status.Validated = pinMatches && metadataMatches
			}
		}
		if resource.Enabled && !status.Validated && status.LastFailure == "" {
			status.LastFailure = "no committed resource version"
			if exists {
				status.LastFailure = "committed resource is unavailable or invalid"
			}
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func sourceHost(source string) string {
	u, err := url.Parse(source)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "local"
	}
	return u.Hostname()
}
