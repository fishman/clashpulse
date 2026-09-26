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

// Plan is one immutable staged generation. Stage never changes active resource
// files or metadata. The caller validates the complete candidate configuration
// before Commit atomically changes the active-generation pointer.
type Plan struct {
	mu         sync.Mutex
	registry   *Registry
	generation string
	directory  string
	maxBytes   int64
	paths      map[string]string
	resources  map[string]resourceState
	base       stateDocument
	changed    bool
	validated  bool
	committed  bool
	commitID   string
	rolledBack bool
	closed     bool
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
	if base.Generation != "" {
		previousDirectory = filepath.Join(r.root, base.Generation)
	}
	prepared, err := prepareResources(ctx, r, snapshot, route, maxBytes, base, previousDirectory, due)
	if err != nil {
		return nil, err
	}
	plan := &Plan{
		registry: r, maxBytes: maxBytes, base: cloneState(base),
		paths: make(map[string]string, len(prepared)), resources: make(map[string]resourceState, len(prepared)),
		changed: !sameResourceGeneration(prepared, base.Resources),
	}
	if !plan.changed {
		plan.generation = base.Generation
		if base.Generation != "" {
			plan.directory = previousDirectory
		}
		for id, item := range prepared {
			plan.paths[id] = filepath.Join(plan.directory, filename(item.resource))
			plan.resources[id] = item.state
		}
		return plan, nil
	}
	generation, err := newGenerationID()
	if err != nil {
		return nil, fmt.Errorf("resources: create generation identity: %w", err)
	}
	r.mu.Lock()
	if err := verifyRealDirectory(r.root, r.home); err != nil {
		r.mu.Unlock()
		return nil, fmt.Errorf("resources: generation root is unsafe: %w", err)
	}
	directory := filepath.Join(r.root, generation)
	if err := os.Mkdir(directory, 0o700); err != nil {
		r.mu.Unlock()
		return nil, fmt.Errorf("resources: create staged generation: %w", err)
	}
	r.pending[generation] = struct{}{}
	r.mu.Unlock()
	plan.generation = generation
	plan.directory = directory
	failed := true
	defer func() {
		if failed {
			r.mu.Lock()
			if verifyRealDirectory(r.root, r.home) == nil && verifyRealDirectory(directory, r.root) == nil {
				_ = os.RemoveAll(directory)
			}
			delete(r.pending, generation)
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
		path := filepath.Join(directory, filename(item.resource))
		if err := writeStaged(path, directory, body); err != nil {
			return nil, fmt.Errorf("resource %q: %w", id, err)
		}
		if err := os.Chmod(path, 0o400); err != nil {
			return nil, fmt.Errorf("resource %q: secure staged file: %w", id, err)
		}
		plan.paths[id] = path
		plan.resources[id] = item.state
	}
	failed = false
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
	if base.Generation != r.active.Generation || base.CommitID != r.active.CommitID {
		return 0, stateDocument{}, fmt.Errorf("resources: active generation changed outside registry")
	}
	if base.Generation != "" {
		if err := verifyRealDirectory(filepath.Join(r.root, base.Generation), r.root); err != nil {
			return 0, stateDocument{}, err
		}
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
		body, etag, lastModified, err := stagedBody(ctx, registry, resource, route, maxBytes, base, previousDirectory)
		if err != nil {
			failures = append(failures, registry.recordFailure(resource.ID, err))
			continue
		}
		if err := Validate(resource, body); err != nil {
			failures = append(failures, registry.recordFailure(resource.ID, err))
			continue
		}
		sum := digest(body)
		if resource.SHA256 != "" && !strings.EqualFold(resource.SHA256, sum) {
			failures = append(failures, registry.recordFailure(resource.ID, ErrPinMismatch))
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

func stagedBody(ctx context.Context, registry *Registry, resource config.Resource, route download.Route, maxBytes int64, base stateDocument, previousDirectory string) ([]byte, string, string, error) {
	if !isRemote(resource.URL) {
		body, err := readLocal(resource.URL, maxBytes)
		return body, "", "", err
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
		return nil, "", "", err
	}
	if response.StatusCode == 304 {
		if previousDirectory == "" || old.SourceHash != sourceHash || old.SHA256 == "" {
			return nil, "", "", fmt.Errorf("conditional response has no verified cached resource")
		}
		oldPath := filepath.Join(previousDirectory, filename(resource))
		body, err := readManaged(oldPath, maxBytes)
		if err != nil || digest(body) != old.SHA256 {
			return nil, "", "", fmt.Errorf("conditional response has no verified cached resource")
		}
		if resource.SHA256 != "" && !strings.EqualFold(resource.SHA256, old.SHA256) {
			return nil, "", "", ErrPinMismatch
		}
		etag, lastModified := old.ETag, old.LastModified
		if response.ETag != "" {
			etag = response.ETag
		}
		if response.LastModified != "" {
			lastModified = response.LastModified
		}
		return body, etag, lastModified, nil
	}
	return response.Body, response.ETag, response.LastModified, nil
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

// Home returns this candidate's Mihomo data home. Fixed-name geodata and
// registry-controlled provider files all live directly in this directory.
func (p *Plan) Home() string {
	if p == nil {
		return ""
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

// Validate invokes the selected-binary integration callback with this complete
// staged home. It is required for every plan; Mihomo's own parser validates the
// embedded rules of MRS resources before their generation can be committed.
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

// Commit atomically selects this generation only after successful candidate
// validation. The current and prior generations remain available to the runtime
// and rollback; superseded generations are pruned before promotion.
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
	if current.Generation != p.base.Generation || current.CommitID != p.base.CommitID {
		return nil, fmt.Errorf("resources: active generation changed while candidate was being validated")
	}
	if p.changed {
		if err := r.collectOldGenerationsLocked(current.Generation, p.generation); err != nil {
			return nil, err
		}
	}
	commitID, err := newCommitID()
	if err != nil {
		return nil, fmt.Errorf("resources: create commit identity: %w", err)
	}
	document := stateDocument{Version: 1, Generation: p.generation, CommitID: commitID, Resources: cloneResourceStates(p.resources)}
	if err := writeStateAtomic(filepath.Join(r.home, stateFileName), r.home, document); err != nil {
		return nil, err
	}
	r.active = document
	delete(r.pending, p.generation)
	p.commitID = commitID
	p.committed = true
	return clonePaths(p.paths), nil
}

func (r *Registry) collectOldGenerationsLocked(active, candidate string) error {
	if err := verifyRealDirectory(r.root, r.home); err != nil {
		return fmt.Errorf("resources: generation root is unsafe: %w", err)
	}
	entries, err := os.ReadDir(r.root)
	if err != nil {
		return fmt.Errorf("resources: list generations: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !validGeneration(name) || name == active || name == candidate || r.pins[name] > 0 {
			continue
		}
		if _, pending := r.pending[name]; pending {
			continue
		}
		if err := os.RemoveAll(filepath.Join(r.root, name)); err != nil {
			return fmt.Errorf("resources: remove superseded generation: %w", err)
		}
	}
	return nil
}

// Rollback restores the previously committed generation after runtime readiness
// fails. It refuses to overwrite a generation committed after this plan.
func (p *Plan) Rollback() error {
	if p == nil {
		return fmt.Errorf("resources: nil plan")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.committed || p.rolledBack {
		return fmt.Errorf("resources: plan is not an unrolled committed generation")
	}
	r := p.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	current, err := loadStateFile(filepath.Join(r.home, stateFileName))
	if err != nil {
		return err
	}
	if current.Generation != p.generation || current.CommitID != p.commitID {
		return fmt.Errorf("resources: cannot roll back after another generation was committed")
	}
	if err := writeStateAtomic(filepath.Join(r.home, stateFileName), r.home, cloneState(p.base)); err != nil {
		return err
	}
	r.active = cloneState(p.base)
	p.rolledBack = true
	return nil
}

// Abort removes a staged generation that has not been committed. Committed
// generations must be restored through Rollback and are never implicitly deleted.
func (p *Plan) Abort() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.committed && !p.rolledBack {
		return fmt.Errorf("resources: committed generation must be rolled back, not aborted")
	}
	if p.closed {
		return nil
	}
	if p.changed && p.directory != "" {
		r := p.registry
		r.mu.Lock()
		if r.pins[p.generation] == 0 {
			if err := verifyRealDirectory(r.root, r.home); err != nil {
				r.mu.Unlock()
				return fmt.Errorf("resources: generation root is unsafe: %w", err)
			}
			if err := verifyRealDirectory(p.directory, r.root); err != nil && !os.IsNotExist(err) {
				r.mu.Unlock()
				return fmt.Errorf("resources: staged generation is unsafe: %w", err)
			}
			if err := os.RemoveAll(p.directory); err != nil {
				r.mu.Unlock()
				return fmt.Errorf("resources: remove staged generation: %w", err)
			}
		}
		delete(r.pending, p.generation)
		r.mu.Unlock()
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
	if err := verifyRealDirectory(p.directory, p.registry.root); err != nil {
		return fmt.Errorf("resources: staged generation is unsafe: %w", err)
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
