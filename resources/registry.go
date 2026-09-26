package resources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/download"
)

const (
	DefaultMaxBytes     int64 = 64 << 20
	stateFileName             = ".resources.json"
	legacyDirName             = "generations"
	transactionFileName       = ".resources.txn"
	maxStateBytes       int64 = 1 << 20
)

var ErrPinMismatch = errors.New("resource SHA-256 pin mismatch")

type ResourceFailure struct {
	ResourceID string
	Err        error
}

func (e *ResourceFailure) Error() string {
	return fmt.Sprintf("resource %q: %v", e.ResourceID, e.Err)
}

func (e *ResourceFailure) Unwrap() error { return e.Err }

// ErrPlanUnvalidated is returned when a staged resource generation is
// committed before the caller validates its complete candidate Mihomo config.
var ErrPlanUnvalidated = errors.New("resource plan has not passed candidate configuration validation")

type resourceState struct {
	SHA256          string                `json:"sha256"`
	SourceHash      string                `json:"source_hash"`
	ETag            string                `json:"etag,omitempty"`
	LastModified    string                `json:"last_modified,omitempty"`
	Kind            config.ResourceKind   `json:"kind"`
	Format          config.ResourceFormat `json:"format"`
	RuleType        config.RuleType       `json:"rule_type,omitempty"`
	LastCheckedUnix int64                 `json:"last_checked_unix,omitempty"`
	LastSuccessUnix int64                 `json:"last_success_unix,omitempty"`
}

type stateDocument struct {
	Version    int                      `json:"version"`
	Generation string                   `json:"generation,omitempty"`
	CommitID   string                   `json:"commit_id,omitempty"`
	Resources  map[string]resourceState `json:"resources"`
}

type Registry struct {
	home     string
	client   *download.Client
	maxBytes int64
	mu       sync.Mutex
	active   stateDocument
	failures map[string]string
}

func resourceStateMatches(resource config.Resource, state resourceState) bool {
	declaration := resourceState{Kind: resource.Kind, Format: resource.Format, RuleType: resource.RuleType}
	return sameResourceState(declaration, state)
}

func sameResourceState(left, right resourceState) bool {
	return left.Kind == right.Kind && left.Format == right.Format && left.RuleType == right.RuleType
}

// NewRegistry opens the private resource store and recovers interrupted work
// before migrating legacy generations or exposing managed paths.
func NewRegistry(home string, client *download.Client) (*Registry, error) {
	if client == nil {
		return nil, fmt.Errorf("resources: bounded downloader is required")
	}
	if home == "" {
		return nil, fmt.Errorf("resources: managed home is required")
	}
	absolute, err := filepath.Abs(home)
	if err != nil {
		return nil, fmt.Errorf("resources: resolve managed home")
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("resources: create managed home: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || resolved != absolute {
		return nil, fmt.Errorf("resources: managed home must not contain symlinks")
	}
	info, err := os.Lstat(absolute)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("resources: managed home must be a real directory")
	}
	if err := os.Chmod(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("resources: secure managed home: %w", err)
	}
	registry := &Registry{
		home: absolute, client: client, maxBytes: DefaultMaxBytes,
		failures: make(map[string]string),
	}
	if err := registry.recoverTransaction(); err != nil {
		return nil, err
	}
	if err := registry.removeOrphanTransactions(); err != nil {
		return nil, err
	}
	active, err := loadStateFile(filepath.Join(absolute, stateFileName))
	if err != nil {
		return nil, err
	}
	if active.Version == 1 {
		active, err = registry.migrateV1(active)
		if err != nil {
			return nil, err
		}
	}
	if active.Version == 2 && active.CommitID != "" && verifyManifestFiles(absolute, active, registry.maxBytes) == nil {
		if err := removeLegacyGenerations(filepath.Join(absolute, legacyDirName)); err != nil {
			return nil, fmt.Errorf("resources: remove migrated generations: %w", err)
		}
	}
	registry.active = active
	return registry, nil
}

// Home returns the stable managed resource root.
func (r *Registry) Home() string {
	if r == nil {
		return ""
	}
	return r.home
}

// CommitID identifies the currently committed resource manifest.
func (r *Registry) CommitID() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active.CommitID
}

// SetMaxBytes sets the positive per-resource source size limit. Configure it
// before concurrent Stage calls.
func (r *Registry) SetMaxBytes(maxBytes int64) error {
	if r == nil || maxBytes <= 0 {
		return fmt.Errorf("resources: max bytes must be positive")
	}
	r.mu.Lock()
	r.maxBytes = maxBytes
	r.mu.Unlock()
	return nil
}

func (r *Registry) clearFailure(id string) {
	r.mu.Lock()
	delete(r.failures, id)
	r.mu.Unlock()
}

func (r *Registry) recordFailure(id string, err error) error {
	message := "resource refresh or validation failed"
	if status, ok := download.StatusErrorFrom(err); ok {
		message = status.Error()
	} else if errors.Is(err, ErrPinMismatch) {
		message = ErrPinMismatch.Error()
	}
	r.mu.Lock()
	r.failures[id] = message
	r.mu.Unlock()
	return &ResourceFailure{ResourceID: id, Err: err}
}

// Paths returns the validated stable files for the currently committed resource set.
func (r *Registry) Paths(snapshot config.Snapshot) (map[string]string, error) {
	_, paths, err := r.PathsWithHome(snapshot)
	return paths, err
}

// PathsWithHome resolves stable resource files and their Mihomo data directory.
func (r *Registry) PathsWithHome(snapshot config.Snapshot) (string, map[string]string, error) {
	if r == nil {
		return "", nil, fmt.Errorf("resources: nil registry")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.validateSnapshot(snapshot); err != nil {
		return "", nil, err
	}
	current, err := loadStateFile(filepath.Join(r.home, stateFileName))
	if err != nil {
		return "", nil, err
	}
	if current.Version != r.active.Version || current.CommitID != r.active.CommitID {
		return "", nil, fmt.Errorf("resources: active manifest changed outside registry")
	}
	paths, err := r.pathsLocked(snapshot)
	if err != nil {
		return "", nil, err
	}
	return r.home, paths, nil
}

// ActiveHome returns the stable managed resource root.
func (r *Registry) ActiveHome() (string, error) {
	if r == nil {
		return "", fmt.Errorf("resources: nil registry")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, err := loadStateFile(filepath.Join(r.home, stateFileName))
	if err != nil {
		return "", err
	}
	if current.Version != r.active.Version || current.CommitID != r.active.CommitID {
		return "", fmt.Errorf("resources: active manifest changed outside registry")
	}
	if err := ensureRoot(r.home); err != nil {
		return "", err
	}
	return r.home, nil
}

func (r *Registry) pathsLocked(snapshot config.Snapshot) (map[string]string, error) {
	if err := ensureRoot(r.home); err != nil {
		return nil, err
	}
	if len(r.active.Resources) == 0 {
		if hasEnabledResources(snapshot) {
			return nil, fmt.Errorf("resources: no committed resource files")
		}
		return map[string]string{}, nil
	}
	paths := make(map[string]string)
	for _, resource := range snapshot.Resources {
		if !resource.Enabled {
			continue
		}
		path := filepath.Join(r.home, filename(resource))
		data, err := readManaged(path, r.maxBytes)
		if err != nil {
			return nil, fmt.Errorf("resource %q has no safe managed file: %w", resource.ID, err)
		}
		if err := Validate(resource, data); err != nil {
			return nil, err
		}
		sum := digest(data)
		if resource.SHA256 != "" && !strings.EqualFold(resource.SHA256, sum) {
			return nil, fmt.Errorf("resource %q: %w", resource.ID, ErrPinMismatch)
		}
		cached, ok := r.active.Resources[resource.ID]
		if !ok || cached.SHA256 != sum || cached.SourceHash != digest([]byte(resource.URL)) || !resourceStateMatches(resource, cached) {
			return nil, fmt.Errorf("resource %q managed bytes differ from committed state", resource.ID)
		}
		paths[resource.ID] = path
	}
	return paths, nil
}

func (r *Registry) validateSnapshot(snapshot config.Snapshot) error {
	seen := make(map[string]struct{}, len(snapshot.Resources))
	destinations := make(map[string]string, len(snapshot.Resources))
	for _, resource := range snapshot.Resources {
		if _, ok := seen[resource.ID]; ok {
			return fmt.Errorf("resources: duplicate ID %q", resource.ID)
		}
		seen[resource.ID] = struct{}{}
		if err := ValidateDeclaration(resource); err != nil {
			return err
		}
		if resource.URL == "" {
			return fmt.Errorf("resource %q has no source", resource.ID)
		}
		if resource.Interval < 0 {
			return fmt.Errorf("resource %q has invalid interval", resource.ID)
		}
		if resource.SHA256 != "" {
			pin, err := hex.DecodeString(resource.SHA256)
			if err != nil || len(pin) != sha256.Size {
				return fmt.Errorf("resource %q has invalid SHA-256 pin", resource.ID)
			}
		}
		u, err := url.Parse(resource.URL)
		if err != nil {
			return fmt.Errorf("resource %q has invalid source", resource.ID)
		}
		if u.Scheme != "" {
			if u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" {
				return fmt.Errorf("resource %q source must be an HTTPS URL without credentials", resource.ID)
			}
		} else if strings.ContainsRune(resource.URL, '\x00') || strings.TrimSpace(resource.URL) == "" {
			return fmt.Errorf("resource %q has invalid local source path", resource.ID)
		}
		if resource.Kind == config.ResourceGeoIP || resource.Kind == config.ResourceGeoSite || resource.Kind == config.ResourceMMDB {
			name := filename(resource)
			if previous, ok := destinations[name]; ok {
				return fmt.Errorf("resources %q and %q share managed destination", previous, resource.ID)
			}
			destinations[name] = resource.ID
		}
	}
	return nil
}

func loadStateFile(path string) (stateDocument, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return stateDocument{Version: 2, Resources: make(map[string]resourceState)}, nil
	}
	if err != nil {
		return stateDocument{}, fmt.Errorf("resources: inspect metadata: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxStateBytes {
		return stateDocument{}, fmt.Errorf("resources: unsafe metadata file")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return stateDocument{}, fmt.Errorf("resources: secure metadata file: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return stateDocument{}, fmt.Errorf("resources: read metadata: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxStateBytes))
	decoder.DisallowUnknownFields()
	var document stateDocument
	if err := decoder.Decode(&document); err != nil {
		return stateDocument{}, fmt.Errorf("resources: decode metadata: %w", err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return stateDocument{}, fmt.Errorf("resources: invalid trailing metadata")
	}
	if err := validateStateDocument(document); err != nil {
		return stateDocument{}, err
	}
	return document, nil
}

func validateStateDocument(document stateDocument) error {
	if (document.Version != 1 && document.Version != 2) || document.Resources == nil {
		return fmt.Errorf("resources: unsupported metadata version")
	}
	if document.Version == 1 && document.Generation != "" && !validGeneration(document.Generation) {
		return fmt.Errorf("resources: invalid legacy generation")
	}
	if document.Version == 2 && document.Generation != "" {
		return fmt.Errorf("resources: v2 metadata cannot reference a generation")
	}
	if document.Version == 2 && len(document.Resources) > 0 && document.CommitID == "" {
		return fmt.Errorf("resources: committed metadata has no identity")
	}
	if document.CommitID != "" {
		if len(document.CommitID) != 32 {
			return fmt.Errorf("resources: invalid metadata commit identity")
		}
		if _, err := hex.DecodeString(document.CommitID); err != nil {
			return fmt.Errorf("resources: invalid metadata commit identity")
		}
	}
	for id, state := range document.Resources {
		if !validID(id) || len(state.SHA256) != sha256.Size*2 || (state.SourceHash != "" && len(state.SourceHash) != sha256.Size*2) || len(state.ETag) > 4096 || len(state.LastModified) > 4096 {
			return fmt.Errorf("resources: invalid metadata entry")
		}
		if _, err := hex.DecodeString(state.SHA256); err != nil {
			return fmt.Errorf("resources: invalid metadata hash")
		}
		if state.SourceHash != "" {
			if _, err := hex.DecodeString(state.SourceHash); err != nil {
				return fmt.Errorf("resources: invalid source identity")
			}
		}
		if err := ValidateDeclaration(config.Resource{ID: id, Kind: state.Kind, Format: state.Format, RuleType: state.RuleType, Enabled: true}); err != nil {
			return fmt.Errorf("resources: invalid metadata declaration: %w", err)
		}
	}
	if _, err := manifestNames(document); err != nil {
		return err
	}
	return nil
}

func writeStateAtomic(path, parent string, document stateDocument) error {
	data, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("resources: encode metadata: %w", err)
	}
	if int64(len(data)) > maxStateBytes {
		return fmt.Errorf("resources: metadata exceeds size limit")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("resources: refusing unsafe metadata destination")
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("resources: inspect metadata destination: %w", err)
	}
	temp, err := os.CreateTemp(parent, ".resource-state-*.tmp")
	if err != nil {
		return fmt.Errorf("resources: create metadata temporary: %w", err)
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("resources: write metadata: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("resources: sync metadata: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("resources: close metadata: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("resources: atomically promote metadata: %w", err)
	}
	if directory, err := os.Open(parent); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func verifyRealDirectory(path, parent string) error {
	if filepath.Dir(path) != parent {
		return fmt.Errorf("invalid generation path")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("generation path is not a real directory")
	}
	return nil
}

func validGeneration(value string) bool {
	if !strings.HasPrefix(value, "gen-") || len(value) != 36 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "gen-"))
	return err == nil
}

func filename(resource config.Resource) string {
	switch resource.Kind {
	case config.ResourceGeoIP:
		return "geoip.dat"
	case config.ResourceGeoSite:
		return "geosite.dat"
	case config.ResourceMMDB:
		return "Country.mmdb"
	default:
		return ManagedFilename(resource)
	}
}

// ManagedFilename returns the registry-controlled filename for rule resources.
func ManagedFilename(resource config.Resource) string {
	sum := sha256.Sum256([]byte(resource.ID))
	extension := ".invalid"
	switch resource.Format {
	case config.FormatYAML, config.FormatText, config.FormatMRS:
		extension = formatExtension(resource.Format)
	}
	return hex.EncodeToString(sum[:]) + extension
}

func formatExtension(format config.ResourceFormat) string {
	switch format {
	case config.FormatDAT:
		return ".dat"
	case config.FormatMMDB:
		return ".mmdb"
	case config.FormatYAML:
		return ".yaml"
	case config.FormatText:
		return ".txt"
	case config.FormatMRS:
		return ".mrs"
	default:
		return ".invalid"
	}
}

func hasEnabledResources(snapshot config.Snapshot) bool {
	for _, resource := range snapshot.Resources {
		if resource.Enabled {
			return true
		}
	}
	return false
}

func isRemote(source string) bool { return strings.Contains(source, "://") }

func readLocal(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("read local resource: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("local resource source must be a regular non-symlink file")
	}
	if info.Size() <= 0 || info.Size() > maxBytes {
		return nil, fmt.Errorf("local resource exceeds size limit or is empty")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open local resource: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read local resource: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("local resource exceeds size limit")
	}
	return data, nil
}

func readManaged(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("managed destination is not a regular non-symlink file")
	}
	if info.Size() <= 0 || info.Size() > maxBytes {
		return nil, fmt.Errorf("managed file is empty or exceeds size limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("managed file exceeds size limit")
	}
	return data, nil
}

func writeStaged(path, parent string, body []byte) error {
	if filepath.Dir(path) != parent {
		return fmt.Errorf("resources: invalid staged destination")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("resources: refusing unsafe staged destination")
		}
		return fmt.Errorf("resources: staged destination already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("resources: inspect staged destination: %w", err)
	}
	temp, err := os.CreateTemp(parent, ".resource-*.tmp")
	if err != nil {
		return fmt.Errorf("resources: create temporary file: %w", err)
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("resources: secure temporary file: %w", err)
	}
	if _, err := temp.Write(body); err != nil {
		temp.Close()
		return fmt.Errorf("resources: write temporary resource: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("resources: sync temporary resource: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("resources: close temporary resource: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("resources: stage resource: %w", err)
	}
	return nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func boundedValidator(value string) string {
	if len(value) > 4096 {
		return ""
	}
	return value
}
