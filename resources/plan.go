package resources

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/download"
)

// CandidateValidator renders the complete generated Mihomo configuration
// using the supplied private home and resource paths, then validates it with
// the selected executable. Commit is impossible until this callback succeeds.
type CandidateValidator func(home string, paths map[string]string) error

// Plan stages candidate resource bytes without changing the committed files.
// Commit promotes them under stable names and leaves a recovery journal until
// Finalize acknowledges that the runtime accepted the replacement.
type Plan struct {
	mu             sync.Mutex
	registry       *Registry
	stageID        string
	directory      string
	transactionDir string
	maxBytes       int64
	paths          map[string]string
	resources      map[string]resourceState
	base           stateDocument
	changed        bool
	validated      bool
	committed      bool
	commitID       string
	rolledBack     bool
	finalized      bool
	closed         bool
}

// Stage refreshes every enabled resource and is intended for startup or binary
// changes where a complete generation is required.
func (r *Registry) Stage(ctx context.Context, snapshot config.Snapshot, route download.Route) (*Plan, error) {
	due := make([]string, 0, len(snapshot.Resources))
	for _, resource := range snapshot.Resources {
		if resource.Enabled {
			due = append(due, resource.ID)
		}
	}
	return r.StageDue(ctx, snapshot, route, due)
}

// StageDue stages one complete candidate generation, refreshing only dueIDs
// and copying every other enabled resource from a verified committed version.
// If a non-due resource has no matching known-good version, staging fails closed.
func (r *Registry) StageDue(ctx context.Context, snapshot config.Snapshot, route download.Route, dueIDs []string) (*Plan, error) {
	if r == nil {
		return nil, fmt.Errorf("resources: nil registry")
	}
	due := make(map[string]struct{}, len(dueIDs))
	for _, id := range dueIDs {
		if !validID(id) {
			return nil, fmt.Errorf("resources: invalid due resource ID %q", id)
		}
		due[id] = struct{}{}
	}
	known := make(map[string]config.Resource, len(snapshot.Resources))
	for _, resource := range snapshot.Resources {
		known[resource.ID] = resource
	}
	for id := range due {
		resource, ok := known[id]
		if !ok || !resource.Enabled {
			return nil, fmt.Errorf("resources: due resource %q is not enabled in snapshot", id)
		}
	}
	return r.stageDue(ctx, snapshot, route, due)
}

func (r *Registry) stageDue(ctx context.Context, snapshot config.Snapshot, route download.Route, due map[string]struct{}) (*Plan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	maxBytes, base, err := r.stageSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	previousDirectory := ""
	if len(base.Resources) > 0 {
		previousDirectory = r.home
	}
	prepared, err := prepareResources(ctx, r, snapshot, route, maxBytes, base, previousDirectory, due)
	if err != nil {
		return nil, err
	}
	plan := &Plan{
		registry: r, maxBytes: maxBytes, base: cloneState(base), directory: r.home,
		paths: make(map[string]string, len(prepared)), resources: make(map[string]resourceState, len(prepared)),
		changed: !sameResourceGeneration(prepared, base.Resources),
	}
	if !plan.changed {
		for id, item := range prepared {
			plan.paths[id] = filepath.Join(r.home, filename(item.resource))
			plan.resources[id] = item.state
		}
		return plan, nil
	}
	stageDir, err := makePrivateDir(r.home, ".resource-stage-")
	if err != nil {
		return nil, fmt.Errorf("resources: create staged resource directory: %w", err)
	}
	plan.stageID = filepath.Base(stageDir)
	plan.directory = stageDir
	r.mu.Lock()
	r.pending[plan.stageID] = struct{}{}
	r.mu.Unlock()
	staged := false
	defer func() {
		if !staged {
			_ = os.RemoveAll(stageDir)
			r.mu.Lock()
			delete(r.pending, plan.stageID)
			r.mu.Unlock()
		}
	}()
	for id, item := range prepared {
		body := item.body
		if body == nil {
			body, err = readManaged(item.sourcePath, maxBytes)
			if err != nil || digest(body) != item.state.SHA256 {
				return nil, fmt.Errorf("resource %q committed copy changed while staging", id)
			}
		}
		path := filepath.Join(stageDir, filename(item.resource))
		if err := writeStaged(path, stageDir, body); err != nil {
			return nil, fmt.Errorf("resource %q: %w", id, err)
		}
		if err := os.Chmod(path, 0o400); err != nil {
			return nil, fmt.Errorf("resource %q: secure staged file: %w", id, err)
		}
		plan.paths[id] = path
		plan.resources[id] = item.state
	}
	staged = true
	return plan, nil
}

func (r *Registry) stageSnapshot(snapshot config.Snapshot) (int64, stateDocument, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.validateSnapshot(snapshot); err != nil {
		return 0, stateDocument{}, err
	}
	base, err := loadStateFile(filepath.Join(r.home, stateFileName))
	if err != nil {
		return 0, stateDocument{}, err
	}
	if base.Version != r.active.Version || base.CommitID != r.active.CommitID {
		return 0, stateDocument{}, fmt.Errorf("resources: active manifest changed outside registry")
	}
	if base.Version != 2 {
		return 0, stateDocument{}, fmt.Errorf("resources: legacy manifest requires migration")
	}
	if err := ensureRoot(r.home); err != nil {
		return 0, stateDocument{}, err
	}
	return r.maxBytes, base, nil
}

type preparedResource struct {
	resource   config.Resource
	state      resourceState
	body       []byte
	sourcePath string
}

func prepareResources(ctx context.Context, registry *Registry, snapshot config.Snapshot, route download.Route, maxBytes int64, base stateDocument, previousDirectory string, due map[string]struct{}) (map[string]preparedResource, error) {
	prepared := make(map[string]preparedResource, len(snapshot.Resources))
	var failures []error
	for _, resource := range snapshot.Resources {
		if !resource.Enabled {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, refresh := due[resource.ID]; !refresh {
			item, err := prepareCommittedResource(resource, base.Resources[resource.ID], previousDirectory, maxBytes)
			if err != nil {
				return nil, fmt.Errorf("resource %q is not due and has no verified active version: %w", resource.ID, err)
			}
			prepared[resource.ID] = item
			continue
		}
		body, etag, lastModified, responseCode, responseBody, err := stagedBody(ctx, registry, resource, route, maxBytes, base, previousDirectory)
		if err != nil {
			failures = append(failures, registry.recordFailure(resource.ID, err))
			continue
		}
		if err := Validate(resource, body); err != nil {
			if responseBody != "" {
				err = errors.Join(err, download.StatusError{Code: responseCode, ResponseBody: responseBody})
			}
			failures = append(failures, registry.recordFailure(resource.ID, err))
			continue
		}
		sum := digest(body)
		if resource.SHA256 != "" && !strings.EqualFold(resource.SHA256, sum) {
			failure := error(ErrPinMismatch)
			if responseBody != "" {
				failure = errors.Join(failure, download.StatusError{Code: responseCode, ResponseBody: responseBody})
			}
			failures = append(failures, registry.recordFailure(resource.ID, failure))
			continue
		}
		checkedAt := time.Now().UTC().Unix()
		state := resourceState{
			SHA256: sum, SourceHash: digest([]byte(resource.URL)),
			ETag: boundedValidator(etag), LastModified: boundedValidator(lastModified),
			Kind: resource.Kind, Format: resource.Format, RuleType: resource.RuleType,
			LastCheckedUnix: checkedAt, LastSuccessUnix: checkedAt,
		}
		item := preparedResource{resource: resource, state: state, body: body}
		previous := base.Resources[resource.ID]
		if previousDirectory != "" && previous.SHA256 == sum && resourceStateMatches(resource, previous) {
			oldPath := filepath.Join(previousDirectory, filename(resource))
			cached, readErr := readManaged(oldPath, maxBytes)
			if readErr == nil && digest(cached) == sum {
				item.body = nil
				item.sourcePath = oldPath
			}
		}
		registry.clearFailure(resource.ID)
		prepared[resource.ID] = item
	}
	if len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	return prepared, nil
}

func prepareCommittedResource(resource config.Resource, state resourceState, previousDirectory string, maxBytes int64) (preparedResource, error) {
	if previousDirectory == "" || state.SHA256 == "" || state.SourceHash != digest([]byte(resource.URL)) || !resourceStateMatches(resource, state) {
		return preparedResource{}, fmt.Errorf("no committed source-compatible version")
	}
	path := filepath.Join(previousDirectory, filename(resource))
	data, err := readManaged(path, maxBytes)
	if err != nil || digest(data) != state.SHA256 {
		return preparedResource{}, fmt.Errorf("committed version is missing or changed")
	}
	if err := Validate(resource, data); err != nil {
		return preparedResource{}, fmt.Errorf("committed version is invalid: %w", err)
	}
	if resource.SHA256 != "" && !strings.EqualFold(resource.SHA256, state.SHA256) {
		return preparedResource{}, ErrPinMismatch
	}
	return preparedResource{resource: resource, state: state, sourcePath: path}, nil
}

func stagedBody(ctx context.Context, registry *Registry, resource config.Resource, route download.Route, maxBytes int64, base stateDocument, previousDirectory string) ([]byte, string, string, int, string, error) {
	if !isRemote(resource.URL) {
		body, err := readLocal(resource.URL, maxBytes)
		return body, "", "", 0, "", err
	}
	old := base.Resources[resource.ID]
	sourceHash := digest([]byte(resource.URL))
	cacheUsable := previousDirectory != "" && old.SourceHash == sourceHash && old.SHA256 != "" && resourceStateMatches(resource, old)
	if cacheUsable {
		cached, err := readManaged(filepath.Join(previousDirectory, filename(resource)), maxBytes)
		cacheUsable = err == nil && digest(cached) == old.SHA256 && Validate(resource, cached) == nil
		if cacheUsable && resource.SHA256 != "" {
			cacheUsable = strings.EqualFold(resource.SHA256, old.SHA256)
		}
	}
	if !cacheUsable {
		old.ETag = ""
		old.LastModified = ""
	}
	response, err := registry.client.Fetch(ctx, download.Request{
		URL: resource.URL, Route: route, ETag: old.ETag,
		LastModified: old.LastModified, MaxBytes: maxBytes,
	})
	if err != nil {
		return nil, "", "", 0, "", err
	}
	if response.StatusCode == 304 {
		if previousDirectory == "" || old.SourceHash != sourceHash || old.SHA256 == "" {
			return nil, "", "", response.StatusCode, response.DiagnosticBody, fmt.Errorf("conditional response has no verified cached resource")
		}
		oldPath := filepath.Join(previousDirectory, filename(resource))
		body, err := readManaged(oldPath, maxBytes)
		if err != nil || digest(body) != old.SHA256 {
			return nil, "", "", response.StatusCode, response.DiagnosticBody, fmt.Errorf("conditional response has no verified cached resource")
		}
		if resource.SHA256 != "" && !strings.EqualFold(resource.SHA256, old.SHA256) {
			return nil, "", "", response.StatusCode, response.DiagnosticBody, ErrPinMismatch
		}
		etag, lastModified := old.ETag, old.LastModified
		if response.ETag != "" {
			etag = response.ETag
		}
		if response.LastModified != "" {
			lastModified = response.LastModified
		}
		return body, etag, lastModified, response.StatusCode, response.DiagnosticBody, nil
	}
	return response.Body, response.ETag, response.LastModified, response.StatusCode, response.DiagnosticBody, nil
}

// Changed reports whether enabled resource identities or bytes differ from the
// active generation. Conditional metadata changes alone are not resource changes.
func (p *Plan) Changed() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.changed
}

func sameResourceGeneration(next map[string]preparedResource, active map[string]resourceState) bool {
	if len(next) != len(active) {
		return false
	}
	for id, item := range next {
		previous, ok := active[id]
		if !ok || previous.SHA256 != item.state.SHA256 || !sameResourceState(previous, item.state) {
			return false
		}
	}
	return true
}

// Home returns the staged candidate home, or the stable root after commit.
func (p *Plan) Home() string {
	if p == nil {
		return ""
	}
	if p.committed {
		return p.registry.home
	}
	return p.directory
}

// Paths returns a defensive copy of candidate resource paths keyed by stable ID.
func (p *Plan) Paths() map[string]string {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return clonePaths(p.paths)
}

// Validate runs the callback for the complete generated Mihomo config before commit.
// For a resource-only cache without a profile, use ValidateResources instead.
func (p *Plan) Validate(validator CandidateValidator) error {
	if p == nil {
		return fmt.Errorf("resources: nil plan")
	}
	if validator == nil {
		return fmt.Errorf("resources: candidate configuration validator is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.committed || p.rolledBack {
		return fmt.Errorf("resources: plan is no longer pending validation")
	}
	if err := p.verifyStagedFiles(); err != nil {
		p.validated = false
		return err
	}
	if err := validator(p.directory, clonePaths(p.paths)); err != nil {
		p.validated = false
		return fmt.Errorf("resources: candidate Mihomo configuration validation failed: %w", err)
	}
	if err := p.verifyStagedFiles(); err != nil {
		p.validated = false
		return err
	}
	p.validated = true
	return nil
}

// ValidateResources allows caching valid resource bytes without a profile.
// A later profile must pass Validate before Mihomo can use this generation.
func (p *Plan) ValidateResources() error {
	return p.Validate(func(string, map[string]string) error { return nil })
}

// Commit promotes the complete file set and leaves rollback data until Finalize.
func (p *Plan) Commit() (map[string]string, error) {
	if p == nil {
		return nil, fmt.Errorf("resources: nil plan")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.committed || p.rolledBack {
		return nil, fmt.Errorf("resources: plan is no longer pending commit")
	}
	if !p.validated {
		return nil, ErrPlanUnvalidated
	}
	if err := p.verifyStagedFiles(); err != nil {
		return nil, err
	}
	r := p.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	current, err := loadStateFile(filepath.Join(r.home, stateFileName))
	if err != nil {
		return nil, err
	}
	if current.Version != p.base.Version || current.CommitID != p.base.CommitID {
		return nil, fmt.Errorf("resources: active manifest changed while candidate was being validated")
	}
	commitID, err := newCommitID()
	if err != nil {
		return nil, fmt.Errorf("resources: create commit identity: %w", err)
	}
	document := stateDocument{Version: 2, CommitID: commitID, Resources: cloneResourceStates(p.resources)}
	if p.changed {
		p.transactionDir, err = r.promote(current, document, p.paths, false)
	} else {
		err = writeStateAtomic(filepath.Join(r.home, stateFileName), r.home, document)
	}
	if err != nil {
		return nil, err
	}
	r.active = document
	p.commitID = commitID
	p.committed = true
	stablePaths := make(map[string]string, len(p.resources))
	for id, state := range p.resources {
		stablePaths[id] = filepath.Join(r.home, resourceFilename(id, state))
	}
	p.paths = stablePaths
	return clonePaths(stablePaths), nil
}

// Finalize discards rollback files after the runtime has accepted this commit.
func (p *Plan) Finalize() error {
	if p == nil {
		return fmt.Errorf("resources: nil plan")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finalized {
		return nil
	}
	if !p.committed || p.rolledBack {
		return fmt.Errorf("resources: plan is not an unrolled committed plan")
	}
	r := p.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	current, err := loadStateFile(filepath.Join(r.home, stateFileName))
	if err != nil {
		return err
	}
	if current.Version != 2 || current.CommitID != p.commitID {
		return fmt.Errorf("resources: cannot finalize after another manifest was committed")
	}
	if p.changed {
		journal, transactionDir, err := readTransaction(r.home, filepath.Join(r.home, transactionFileName))
		if err != nil {
			return err
		}
		if journal.Candidate.CommitID != p.commitID || transactionDir != p.transactionDir {
			return fmt.Errorf("resources: transaction does not belong to this plan")
		}
		if err := finalizeTransaction(r.home, transactionDir); err != nil {
			return err
		}
	}
	if p.stageID != "" {
		if err := os.RemoveAll(p.directory); err != nil {
			return fmt.Errorf("resources: remove staged resources: %w", err)
		}
		delete(r.pending, p.stageID)
	}
	p.finalized = true
	p.closed = true
	return nil
}

// Rollback restores the previous stable files and manifest after runtime failure.
func (p *Plan) Rollback() error {
	if p == nil {
		return fmt.Errorf("resources: nil plan")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.committed || p.rolledBack || p.finalized {
		return fmt.Errorf("resources: plan is not an unfinalized committed plan")
	}
	r := p.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	current, err := loadStateFile(filepath.Join(r.home, stateFileName))
	if err != nil {
		return err
	}
	if current.Version != 2 || current.CommitID != p.commitID {
		return fmt.Errorf("resources: cannot roll back after another manifest was committed")
	}
	if p.changed {
		journal, transactionDir, err := readTransaction(r.home, filepath.Join(r.home, transactionFileName))
		if err != nil {
			return err
		}
		if journal.Candidate.CommitID != p.commitID || transactionDir != p.transactionDir {
			return fmt.Errorf("resources: transaction does not belong to this plan")
		}
		if err := restoreTransaction(r.home, filepath.Join(r.home, transactionFileName), transactionDir, journal); err != nil {
			return err
		}
	} else if err := writeStateAtomic(filepath.Join(r.home, stateFileName), r.home, cloneState(p.base)); err != nil {
		return err
	}
	r.active = cloneState(p.base)
	p.rolledBack = true
	return nil
}

// Abort discards an uncommitted staging directory after validation or rollback.
func (p *Plan) Abort() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	if p.committed && !p.rolledBack {
		return fmt.Errorf("resources: committed plan must be finalized or rolled back")
	}
	if p.stageID != "" {
		r := p.registry
		r.mu.Lock()
		defer r.mu.Unlock()
		if filepath.Base(p.directory) != p.stageID || !validPrivateDir(p.stageID) || filepath.Dir(p.directory) != r.home {
			return fmt.Errorf("resources: staged directory is unsafe")
		}
		if err := verifyRealDirectory(p.directory, r.home); err == nil {
			if err := os.RemoveAll(p.directory); err != nil {
				return fmt.Errorf("resources: remove staged resources: %w", err)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("resources: staged directory is unsafe: %w", err)
		}
		delete(r.pending, p.stageID)
	}
	p.closed = true
	return nil
}

func (p *Plan) verifyStagedFiles() error {
	if len(p.paths) != len(p.resources) {
		return fmt.Errorf("resources: staged generation is incomplete")
	}
	if len(p.paths) == 0 && p.directory == "" {
		return nil
	}
	if p.directory == p.registry.home {
		if err := ensureRoot(p.directory); err != nil {
			return fmt.Errorf("resources: managed root is unsafe: %w", err)
		}
	} else if err := verifyRealDirectory(p.directory, p.registry.root); err != nil {
		return fmt.Errorf("resources: staged resource directory is unsafe: %w", err)
	}
	for id, path := range p.paths {
		if filepath.Clean(path) != filepath.Join(p.directory, filepath.Base(path)) {
			return fmt.Errorf("resources: invalid staged path for %q", id)
		}
		data, err := readManaged(path, p.maxBytes)
		if err != nil {
			return fmt.Errorf("resource %q staged file is unsafe: %w", id, err)
		}
		state, ok := p.resources[id]
		if !ok || digest(data) != state.SHA256 {
			return fmt.Errorf("resource %q changed after staging", id)
		}
	}
	return nil
}

func newGenerationID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return "gen-" + hex.EncodeToString(id[:]), nil
}

func newCommitID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func clonePaths(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for id, path := range in {
		out[id] = path
	}
	return out
}

func cloneResourceStates(in map[string]resourceState) map[string]resourceState {
	out := make(map[string]resourceState, len(in))
	for id, state := range in {
		out[id] = state
	}
	return out
}

func cloneState(in stateDocument) stateDocument {
	return stateDocument{Version: in.Version, Generation: in.Generation, CommitID: in.CommitID, Resources: cloneResourceStates(in.Resources)}
}
