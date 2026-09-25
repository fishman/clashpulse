package subscriptions

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/download"
)

const (
	defaultMaxProfileBytes int64 = 10 << 20
	maxProfileBytes        int64 = 32 << 20
	maxCandidateBytes      int64 = 64 << 20
)

// ErrCleanupPending means deletion committed but quarantine cleanup needs retry.
var ErrCleanupPending = errors.New("subscriptions: deletion cleanup pending")

// privateRecord is the only persistent representation. It may contain a URL
// with credentials and is therefore written only below 0700 directories in
// 0600 files. Snapshot generations are immutable and the record is the commit
// pointer, so a failed promotion leaves the previous generation authoritative.
type privateRecord struct {
	Subscription         config.Subscription `json:"subscription"`
	ETag                 string              `json:"etag,omitempty"`
	LastModified         string              `json:"last_modified,omitempty"`
	CheckedAt            time.Time           `json:"checked_at,omitempty"`
	LastSuccess          time.Time           `json:"last_success,omitempty"`
	LastFailureAt        time.Time           `json:"last_failure_at,omitempty"`
	LastFailure          string              `json:"last_failure,omitempty"`
	Usage                *Usage              `json:"usage,omitempty"`
	Hash                 string              `json:"hash,omitempty"`
	CandidateHash        string              `json:"candidate_hash,omitempty"`
	AppliedHash          string              `json:"applied_hash,omitempty"`
	AppliedProfileHash   string              `json:"applied_profile_hash,omitempty"`
	AppliedCandidateHash string              `json:"applied_candidate_hash,omitempty"`
	Active               bool                `json:"-"`
}

type idLock struct {
	mu   sync.Mutex
	refs int
}

// Store owns private subscription metadata and last-known-good source and
// generated snapshots. It uses config.Write's existing private atomic-file
// implementation plus stdlib locking; no database or file-lock dependency is
// justified for this single-owner store.
type Store struct {
	mu                  sync.RWMutex
	dir                 string
	records             map[string]privateRecord
	activeID            string
	activeProfileHash   string
	activeCandidateHash string

	locksMu   sync.Mutex
	locks     map[string]*idLock
	removeAll func(string) error
}

// NewStore opens or creates a private subscription state directory. Existing
// records and generations are integrity-checked before they become visible.
func NewStore(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, ErrStore
	}
	absolute, err := filepath.Abs(dir)
	if err != nil || absolute == filepath.Dir(absolute) || ensurePrivateDir(absolute) != nil {
		return nil, ErrStore
	}
	store := &Store{dir: absolute, records: make(map[string]privateRecord), locks: make(map[string]*idLock), removeAll: os.RemoveAll}
	if err := cleanupQuarantine(absolute, store.removeAll); err != nil {
		return nil, ErrCleanupPending
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return nil, ErrStore
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		id := entry.Name()
		if !validID(id) {
			return nil, ErrStore
		}
		directory := filepath.Join(absolute, id)
		if ensurePrivateDir(directory) != nil {
			return nil, ErrStore
		}
		data, readErr := readPrivateFile(filepath.Join(directory, "record.json"), 1<<20)
		if readErr != nil {
			return nil, ErrStore
		}
		var record privateRecord
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&record) != nil || record.Subscription.ID != id || requireJSONEOF(decoder) != nil {
			return nil, ErrStore
		}
		normalized, normalizeErr := normalizeSubscription(record.Subscription, false)
		if normalizeErr != nil || normalized != record.Subscription || !validOptionalHash(record.Hash) || !validOptionalHash(record.CandidateHash) || !validFailure(record.LastFailure) {
			return nil, ErrStore
		}
		if record.Hash == "" {
			if record.CandidateHash != "" {
				return nil, ErrStore
			}
		} else if record.CandidateHash == "" || store.verifyGeneration(id, record) != nil {
			return nil, ErrStore
		}
		store.records[id] = record
	}

	activePath := filepath.Join(absolute, "active.json")
	if _, err := os.Lstat(activePath); err == nil {
		activeData, err := readPrivateFile(activePath, 1<<20)
		if err != nil {
			return nil, ErrStore
		}
		var active struct {
			ID            string `json:"id"`
			ProfileHash   string `json:"profile_hash,omitempty"`
			CandidateHash string `json:"candidate_hash,omitempty"`
		}
		decoder := json.NewDecoder(bytes.NewReader(activeData))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&active) != nil || requireJSONEOF(decoder) != nil {
			return nil, ErrStore
		}
		if active.ID != "" {
			if !validID(active.ID) {
				return nil, ErrStore
			}
			record, ok := store.records[active.ID]
			if !ok {
				return nil, ErrStore
			}
			if active.ProfileHash == "" && active.CandidateHash == "" {
				active.ProfileHash = record.AppliedProfileHash
				active.CandidateHash = record.AppliedCandidateHash
				if active.ProfileHash == "" && record.AppliedHash != "" {
					active.ProfileHash = record.Hash
					active.CandidateHash = record.AppliedHash
				}
				if store.verifyPair(active.ID, active.ProfileHash, active.CandidateHash) != nil {
					active.ProfileHash = record.Hash
					active.CandidateHash = record.CandidateHash
				}
			}
			if store.verifyPair(active.ID, active.ProfileHash, active.CandidateHash) != nil {
				return nil, ErrStore
			}
		} else if active.ProfileHash != "" || active.CandidateHash != "" {
			return nil, ErrStore
		}
		store.activeID = active.ID
		store.activeProfileHash = active.ProfileHash
		store.activeCandidateHash = active.CandidateHash
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, ErrStore
	}

	for id, record := range store.records {
		record.AppliedHash = ""
		record.AppliedProfileHash = ""
		record.AppliedCandidateHash = ""
		record.Active = id == store.activeID
		if record.Active {
			record.AppliedProfileHash = store.activeProfileHash
			record.AppliedCandidateHash = store.activeCandidateHash
			record.AppliedHash = store.activeCandidateHash
		}
		store.records[id] = record
	}
	store.cleanupUnusedGenerations()
	return store, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ErrStore
	}
	return nil
}

func (s *Store) lockID(id string) func() {
	s.locksMu.Lock()
	lock := s.locks[id]
	if lock == nil {
		lock = &idLock{}
		s.locks[id] = lock
	}
	lock.refs++
	s.locksMu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		s.locksMu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(s.locks, id)
		}
		s.locksMu.Unlock()
	}
}

func (s *Store) add(record privateRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := record.Subscription.ID
	if _, exists := s.records[id]; exists {
		return ErrAlreadyExists
	}
	directory := filepath.Join(s.dir, id)
	if err := os.Mkdir(directory, 0o700); err != nil {
		if os.IsExist(err) {
			return ErrAlreadyExists
		}
		return ErrStore
	}
	if os.Chmod(directory, 0o700) != nil || s.writeRecord(directory, record) != nil {
		_ = os.RemoveAll(directory)
		return ErrStore
	}
	s.records[id] = record
	return nil
}

// ActiveID returns the persisted active subscription selection.
func (s *Store) ActiveID() (string, error) {
	s.mu.RLock()
	id := s.activeID
	s.mu.RUnlock()
	if id == "" {
		return "", ErrNoSnapshot
	}
	return id, nil
}

// Profile returns the current validated last-known-good source for a subscription.
func (s *Store) Profile(id string) ([]byte, error) {
	s.mu.RLock()
	record, ok := s.records[id]
	if !ok {
		s.mu.RUnlock()
		return nil, ErrNotFound
	}
	profile, err := s.readPairProfile(id, record.Hash, record.CandidateHash)
	s.mu.RUnlock()
	return profile, err
}

// ActiveProfile returns the active subscription's applied source generation.
func (s *Store) ActiveProfile() (string, []byte, error) {
	s.mu.RLock()
	id, profileHash, candidateHash := s.activeID, s.activeProfileHash, s.activeCandidateHash
	if id == "" {
		s.mu.RUnlock()
		return "", nil, ErrNoSnapshot
	}
	profile, err := s.readPairProfile(id, profileHash, candidateHash)
	s.mu.RUnlock()
	if err != nil {
		return "", nil, err
	}
	return id, profile, nil
}

func (s *Store) appliedProfile(id string) ([]byte, error) {
	s.mu.RLock()
	record, ok := s.records[id]
	if !ok {
		s.mu.RUnlock()
		return nil, ErrNotFound
	}
	profile, err := s.readPairProfile(id, record.AppliedProfileHash, record.AppliedCandidateHash)
	s.mu.RUnlock()
	return profile, err
}

func (s *Store) readPairProfile(id, profileHash, candidateHash string) ([]byte, error) {
	if !validID(id) || !validOptionalHash(profileHash) || profileHash == "" || !validOptionalHash(candidateHash) || candidateHash == "" {
		return nil, ErrNoSnapshot
	}
	profile, candidate, readErr := readGenerationFiles(filepath.Join(s.dir, id), profileHash, candidateHash)
	if readErr != nil || !generationMatches(profile, candidate, profileHash, candidateHash) {
		return nil, ErrStore
	}
	return profile, nil
}

func profileFilename(profileHash string) string {
	return "profile-" + profileHash + ".yaml"
}

func candidateFilename(profileHash, candidateHash string) string {
	return "candidate-" + profileHash + "-" + candidateHash + ".yaml"
}

func readGenerationFiles(directory, profileHash, candidateHash string) ([]byte, []byte, error) {
	profile, profileErr := readPrivateFile(filepath.Join(directory, profileFilename(profileHash)), maxProfileBytes)
	candidate, candidateErr := readPrivateFile(filepath.Join(directory, candidateFilename(profileHash, candidateHash)), maxCandidateBytes)
	if profileErr != nil || candidateErr != nil {
		return nil, nil, ErrStore
	}
	return profile, candidate, nil
}

func generationMatches(profile, candidate []byte, profileHash, candidateHash string) bool {
	return hashBytes(profile) == profileHash && hashBytes(candidate) == candidateHash
}

func (s *Store) get(id string) (privateRecord, bool) {
	s.mu.RLock()
	record, ok := s.records[id]
	record.Active = ok && s.activeID == id
	s.mu.RUnlock()
	return record, ok
}

func (s *Store) isActive(id string) bool {
	s.mu.RLock()
	active := s.activeID == id
	s.mu.RUnlock()
	return active
}

func (s *Store) list() []privateRecord {
	s.mu.RLock()
	records := make([]privateRecord, 0, len(s.records))
	for id, record := range s.records {
		record.Active = s.activeID == id
		records = append(records, record)
	}
	s.mu.RUnlock()
	sort.Slice(records, func(i, j int) bool {
		return records[i].Subscription.ID < records[j].Subscription.ID
	})
	return records
}

func (s *Store) replace(record privateRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[record.Subscription.ID]; !ok {
		return ErrNotFound
	}
	directory := filepath.Join(s.dir, record.Subscription.ID)
	if s.writeRecord(directory, record) != nil {
		return ErrStore
	}
	s.records[record.Subscription.ID] = record
	return nil
}

func (s *Store) delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[id]; !ok {
		return ErrNotFound
	}
	if s.activeID == id {
		return ErrActive
	}
	quarantine, err := os.MkdirTemp(s.dir, ".delete-"+id+"-")
	if err != nil {
		return ErrStore
	}
	if err := os.Rename(filepath.Join(s.dir, id), filepath.Join(quarantine, "record")); err != nil {
		_ = os.Remove(quarantine)
		return ErrStore
	}
	delete(s.records, id)
	if syncDirectory(quarantine) != nil || syncDirectory(s.dir) != nil {
		return ErrCleanupPending
	}
	if s.removeAll(quarantine) != nil || syncDirectory(s.dir) != nil {
		return ErrCleanupPending
	}
	return nil
}

func (s *Store) touchSuccess(id string, checkedAt time.Time, etag, lastModified string, usage *Usage) {
	s.mu.Lock()
	record, ok := s.records[id]
	if ok {
		record.CheckedAt = checkedAt
		record.LastSuccess = checkedAt
		if etag != "" {
			record.ETag = etag
		}
		if lastModified != "" {
			record.LastModified = lastModified
		}
		if usage != nil {
			record.Usage = cloneUsage(usage)
		}
		record.LastFailureAt = time.Time{}
		record.LastFailure = ""
		s.records[id] = record
	}
	s.mu.Unlock()
}

func (s *Store) touchFailure(id string, checkedAt time.Time, failure string) bool {
	if !validFailure(failure) || failure == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok {
		return false
	}
	record.CheckedAt = checkedAt
	record.LastFailureAt = checkedAt
	record.LastFailure = failure
	persisted := s.writeRecord(filepath.Join(s.dir, id), record) == nil
	s.records[id] = record
	return persisted
}
func (s *Store) promote(id string, profile, candidate []byte, checkedAt time.Time, etag, lastModified string, usage *Usage) (privateRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok {
		return privateRecord{}, ErrNotFound
	}
	previous := record
	profileHash := hashBytes(profile)
	candidateHash := hashBytes(candidate)
	directory := filepath.Join(s.dir, id)
	if ensurePrivateDir(directory) != nil {
		return privateRecord{}, ErrStore
	}
	profilePath := filepath.Join(directory, profileFilename(profileHash))
	candidatePath := filepath.Join(directory, candidateFilename(profileHash, candidateHash))
	if config.Write(profilePath, profile) != nil {
		return privateRecord{}, ErrStore
	}
	if config.Write(candidatePath, candidate) != nil {
		return privateRecord{}, ErrStore
	}
	record.Hash = profileHash
	record.CandidateHash = candidateHash
	record.CheckedAt = checkedAt
	record.LastSuccess = checkedAt
	record.LastFailureAt = time.Time{}
	record.LastFailure = ""
	record.ETag = etag
	record.LastModified = lastModified
	record.Usage = cloneUsage(usage)
	if s.writeRecord(directory, record) != nil {
		return privateRecord{}, ErrStore
	}
	s.records[id] = record
	return previous, nil
}

func (s *Store) rollbackPromotion(id string, previous privateRecord, profileHash, candidateHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[id]; !ok {
		return ErrNotFound
	}
	if s.writeRecord(filepath.Join(s.dir, id), previous) != nil {
		s.records[id] = previous
		return ErrStore
	}
	s.records[id] = previous
	directory := filepath.Join(s.dir, id)
	if !generationReferenced(previous, profileHash, candidateHash) {
		_ = os.Remove(filepath.Join(directory, candidateFilename(profileHash, candidateHash)))
	}
	if profileHash != previous.Hash && profileHash != previous.AppliedProfileHash {
		_ = os.Remove(filepath.Join(directory, profileFilename(profileHash)))
	}
	return nil
}

func (s *Store) finalizePromotion(id string) {
	s.mu.Lock()
	if record, ok := s.records[id]; ok {
		cleanupGenerations(filepath.Join(s.dir, id), record.Hash, record.CandidateHash, record.AppliedProfileHash, record.AppliedCandidateHash)
	}
	s.mu.Unlock()
}

func generationReferenced(record privateRecord, profileHash, candidateHash string) bool {
	return record.Hash == profileHash && record.CandidateHash == candidateHash ||
		record.AppliedProfileHash == profileHash && record.AppliedCandidateHash == candidateHash
}

func (s *Store) recordSnapshot(id string) (privateRecord, []byte, []byte, error) {
	s.mu.RLock()
	record, ok := s.records[id]
	if !ok {
		s.mu.RUnlock()
		return privateRecord{}, nil, nil, ErrNotFound
	}
	if record.Hash == "" || record.CandidateHash == "" {
		s.mu.RUnlock()
		return privateRecord{}, nil, nil, ErrNoSnapshot
	}
	profile, candidate, readErr := readGenerationFiles(filepath.Join(s.dir, id), record.Hash, record.CandidateHash)
	s.mu.RUnlock()
	if readErr != nil || !generationMatches(profile, candidate, record.Hash, record.CandidateHash) {
		return privateRecord{}, nil, nil, ErrStore
	}
	return record, profile, candidate, nil
}

// setApplied commits activation with active.json as the sole durable applied
// pointer. In-memory applied fields are updated only after that atomic write.
func (s *Store) setApplied(id, profileHash, candidateHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[id]; !ok {
		return ErrNotFound
	}
	if s.verifyPair(id, profileHash, candidateHash) != nil {
		return ErrNoSnapshot
	}
	marker, err := json.Marshal(struct {
		ID            string `json:"id"`
		ProfileHash   string `json:"profile_hash"`
		CandidateHash string `json:"candidate_hash"`
	}{ID: id, ProfileHash: profileHash, CandidateHash: candidateHash})
	if err != nil || config.Write(filepath.Join(s.dir, "active.json"), marker) != nil {
		return ErrStore
	}
	for recordID, record := range s.records {
		record.AppliedHash = ""
		record.AppliedProfileHash = ""
		record.AppliedCandidateHash = ""
		record.Active = recordID == id
		s.records[recordID] = record
	}
	record := s.records[id]
	record.AppliedHash = candidateHash
	record.AppliedProfileHash = profileHash
	record.AppliedCandidateHash = candidateHash
	s.records[id] = record
	s.activeID = id
	s.activeProfileHash = profileHash
	s.activeCandidateHash = candidateHash
	s.cleanupUnusedGenerations()
	return nil
}

func (s *Store) verifyGeneration(id string, record privateRecord) error {
	return s.verifyPair(id, record.Hash, record.CandidateHash)
}

func (s *Store) verifyPair(id, profileHash, candidateHash string) error {
	_, err := s.readPairProfile(id, profileHash, candidateHash)
	return err
}

func (s *Store) cleanupUnusedGenerations() {
	for id, record := range s.records {
		cleanupGenerations(filepath.Join(s.dir, id), record.Hash, record.CandidateHash, record.AppliedProfileHash, record.AppliedCandidateHash)
	}
}

func cleanupGenerations(directory, currentProfile, currentCandidate, appliedProfile, appliedCandidate string) {
	var currentProfileFile, currentCandidateFile string
	if currentProfile != "" && currentCandidate != "" {
		currentProfileFile = profileFilename(currentProfile)
		currentCandidateFile = candidateFilename(currentProfile, currentCandidate)
	}
	var appliedProfileFile, appliedCandidateFile string
	if appliedProfile != "" && appliedCandidate != "" {
		appliedProfileFile = profileFilename(appliedProfile)
		appliedCandidateFile = candidateFilename(appliedProfile, appliedCandidate)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if (strings.HasPrefix(name, "profile-") && name != currentProfileFile && name != appliedProfileFile) ||
			(strings.HasPrefix(name, "candidate-") && name != currentCandidateFile && name != appliedCandidateFile) {
			_ = os.Remove(filepath.Join(directory, name))
		}
	}
}

func cleanupQuarantine(directory string, removeAll func(string) error) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	found := false
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".delete-") {
			continue
		}
		found = true
		if removeAll(filepath.Join(directory, entry.Name())) != nil {
			return ErrCleanupPending
		}
	}
	if found && syncDirectory(directory) != nil {
		return ErrCleanupPending
	}
	return nil
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func (s *Store) writeRecord(directory string, record privateRecord) error {
	// Applied generation pointers are derived from active.json; record.json is
	// never a second durable commit point for runtime activation.
	record.AppliedHash = ""
	record.AppliedProfileHash = ""
	record.AppliedCandidateHash = ""
	record.Active = false
	data, err := json.Marshal(record)
	if err != nil {
		return ErrStore
	}
	if ensurePrivateDir(directory) != nil || config.Write(filepath.Join(directory, "record.json"), data) != nil {
		return ErrStore
	}
	return nil
}

func (s *Store) nextDue(excluded map[string]bool) time.Time {
	_, next := s.dueIDs(time.Now(), excluded)
	return next
}

func (s *Store) dueIDs(now time.Time, excluded map[string]bool) ([]string, time.Time) {
	s.mu.RLock()
	ids := make([]string, 0)
	var next time.Time
	for id, record := range s.records {
		if !record.Subscription.Enabled || excluded[id] {
			continue
		}
		due := record.CheckedAt.Add(record.Subscription.RefreshInterval)
		if record.CheckedAt.IsZero() {
			due = now
		}
		if !due.After(now) {
			ids = append(ids, id)
		}
		if next.IsZero() || due.Before(next) {
			next = due
		}
	}
	s.mu.RUnlock()
	sort.Strings(ids)
	return ids, next
}

func publicEntry(record privateRecord) Entry {
	parsedURL := record.Subscription.URL
	if strings.HasPrefix(parsedURL, "//") {
		parsedURL = "https:" + parsedURL
	} else if !strings.Contains(parsedURL, "://") {
		parsedURL = "https://" + parsedURL
	}
	var sourceHost string
	if parsed, err := url.Parse(parsedURL); err == nil {
		sourceHost = strings.ToLower(parsed.Hostname())
	}
	var nextDue time.Time
	if record.Subscription.Enabled {
		nextDue = record.CheckedAt.Add(record.Subscription.RefreshInterval)
		if record.CheckedAt.IsZero() {
			nextDue = time.Now()
		}
	}
	hasSnapshot := record.Hash != "" && record.CandidateHash != ""
	appliedHash := record.AppliedProfileHash
	if len(appliedHash) > 12 {
		appliedHash = appliedHash[:12]
	}
	pendingActivation := record.Active && record.Hash != record.AppliedProfileHash
	return Entry{
		ID:                record.Subscription.ID,
		Name:              record.Subscription.Name,
		SourceHost:        sourceHost,
		Enabled:           record.Subscription.Enabled,
		RefreshInterval:   record.Subscription.RefreshInterval,
		Timeout:           record.Subscription.Timeout,
		Route:             record.Subscription.Route,
		AllowHTTP:         record.Subscription.AllowHTTP,
		AllowInvalidTLS:   record.Subscription.AllowInvalidTLS,
		CheckedAt:         record.CheckedAt,
		LastSuccess:       record.LastSuccess,
		LastFailure:       record.LastFailure,
		LastFailureAt:     record.LastFailureAt,
		NextDue:           nextDue,
		Hash:              record.Hash,
		AppliedHash:       appliedHash,
		HasSnapshot:       hasSnapshot,
		Active:            record.Active,
		PendingActivation: pendingActivation,
		Usage:             cloneUsage(record.Usage),
	}
}

func cloneUsage(usage *Usage) *Usage {
	if usage == nil {
		return nil
	}
	copy := *usage
	return &copy
}

func normalizeSubscription(subscription config.Subscription, generateID bool) (config.Subscription, error) {
	subscription.ID = strings.TrimSpace(subscription.ID)
	if subscription.ID == "" && generateID {
		id, err := newID()
		if err != nil {
			return config.Subscription{}, ErrStore
		}
		subscription.ID = id
	}
	if !validID(subscription.ID) {
		return config.Subscription{}, ErrInvalid
	}
	subscription.Name = strings.TrimSpace(subscription.Name)
	if subscription.Name == "" {
		subscription.Name = subscription.ID
	}
	if len(subscription.Name) > 256 {
		return config.Subscription{}, ErrInvalid
	}
	if subscription.RefreshInterval == 0 {
		subscription.RefreshInterval = 12 * time.Hour
	}
	if subscription.Timeout == 0 {
		subscription.Timeout = 30 * time.Second
	}
	if subscription.RefreshInterval < 0 || subscription.Timeout < 0 {
		return config.Subscription{}, ErrInvalid
	}
	if subscription.Route == "" {
		subscription.Route = string(download.Direct)
	}
	switch download.Route(subscription.Route) {
	case download.Direct, download.SystemProxy, download.MihomoProxy:
	default:
		return config.Subscription{}, ErrInvalid
	}
	subscription.URL = strings.TrimSpace(subscription.URL)
	if !validURL(subscription.URL, subscription.AllowHTTP) {
		return config.Subscription{}, ErrInvalid
	}
	if !config.ValidSubscriptionUserAgent(subscription.UserAgent) {
		return config.Subscription{}, ErrInvalid
	}
	return subscription, nil
}

func validFailure(failure string) bool {
	switch failure {
	case "", ErrFetch.Error(), ErrNoSnapshot.Error(), ErrInvalidProfile.Error(), ErrRender.Error(), ErrValidation.Error(), context.DeadlineExceeded.Error():
		return true
	default:
		return false
	}
}

func validURL(raw string, allowHTTP bool) bool {
	if raw == "" {
		return false
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	} else if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	target, err := url.Parse(raw)
	if err != nil || target.User != nil || target.Host == "" || target.Opaque != "" {
		return false
	}
	if target.Scheme == "https" {
		return true
	}
	return allowHTTP && target.Scheme == "http"
}

func validID(id string) bool {
	if len(id) == 0 || len(id) > 128 || !isAlphaNumeric(id[0]) {
		return false
	}
	for i := 1; i < len(id); i++ {
		b := id[i]
		if !isAlphaNumeric(b) && b != '.' && b != '_' && b != '-' {
			return false
		}
	}
	return true
}

func isAlphaNumeric(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return "sub_" + hex.EncodeToString(bytes[:]), nil
}

func validOptionalHash(value string) bool {
	if value == "" {
		return true
	}
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func hashBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func ensurePrivateDir(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return ErrStore
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrStore
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return ErrStore
	}
	return nil
}

func readPrivateFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrStore
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, ErrStore
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrStore
	}
	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || int64(len(data)) > limit {
		return nil, ErrStore
	}
	return data, nil
}
