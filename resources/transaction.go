package resources

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fishman/clashpulse/config"
)

const transactionDirPrefix = ".resources-txn-"

type transactionJournal struct {
	Version   int               `json:"version"`
	Phase     string            `json:"phase"`
	BackupDir string            `json:"backup_dir"`
	Previous  stateDocument     `json:"previous"`
	Candidate stateDocument     `json:"candidate"`
	Files     []transactionFile `json:"files"`
}

type transactionFile struct {
	Name            string `json:"name"`
	Backup          string `json:"backup,omitempty"`
	HadPrevious     bool   `json:"had_previous"`
	PreviousSHA256  string `json:"previous_sha256,omitempty"`
	CandidateSHA256 string `json:"candidate_sha256,omitempty"`
}

func (r *Registry) recoverTransaction() error {
	path := filepath.Join(r.home, transactionFileName)
	journal, dir, err := readTransaction(r.home, path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := restoreTransaction(r.home, path, dir, journal); err != nil {
		return fmt.Errorf("resources: recover interrupted promotion: %w", err)
	}
	return nil
}

func (r *Registry) removeOrphanTransactions() error {
	entries, err := os.ReadDir(r.home)
	if err != nil {
		return fmt.Errorf("resources: list managed home: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, transactionDirPrefix) && !strings.HasPrefix(name, ".resource-stage-") {
			continue
		}
		if !validPrivateDir(name) || !entry.IsDir() {
			return fmt.Errorf("resources: unsafe transaction directory")
		}
		path := filepath.Join(r.home, name)
		if err := verifyRealDirectory(path, r.home); err != nil {
			return fmt.Errorf("resources: unsafe transaction directory: %w", err)
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("resources: remove orphan transaction directory: %w", err)
		}
	}
	return nil
}

func (r *Registry) migrateV1(previous stateDocument) (stateDocument, error) {
	candidate := stateDocument{Version: 2, Resources: cloneResourceStates(previous.Resources)}
	commitID, err := newCommitID()
	if err != nil {
		return stateDocument{}, err
	}
	candidate.CommitID = commitID
	if previous.Generation == "" {
		if len(previous.Resources) != 0 {
			return stateDocument{}, fmt.Errorf("resources: v1 resources have no generation")
		}
		if err := writeStateAtomic(filepath.Join(r.home, stateFileName), r.home, candidate); err != nil {
			return stateDocument{}, err
		}
		return candidate, nil
	}
	legacyRoot := filepath.Join(r.home, legacyDirName)
	oldDir := filepath.Join(legacyRoot, previous.Generation)
	if !validGeneration(previous.Generation) {
		return stateDocument{}, fmt.Errorf("resources: invalid legacy generation")
	}
	if err := verifyRealDirectory(legacyRoot, r.home); err != nil {
		return stateDocument{}, fmt.Errorf("resources: legacy generation root is unsafe: %w", err)
	}
	if err := verifyRealDirectory(oldDir, legacyRoot); err != nil {
		return stateDocument{}, fmt.Errorf("resources: legacy generation is unsafe: %w", err)
	}
	stageDir, err := makePrivateDir(r.home, ".resource-stage-")
	if err != nil {
		return stateDocument{}, fmt.Errorf("resources: create migration staging directory: %w", err)
	}
	defer os.RemoveAll(stageDir)
	paths := make(map[string]string, len(previous.Resources))
	for id, state := range previous.Resources {
		resource := config.Resource{ID: id, Kind: state.Kind, Format: state.Format, RuleType: state.RuleType, Enabled: true}
		if err := ValidateDeclaration(resource); err != nil {
			return stateDocument{}, fmt.Errorf("resources: invalid legacy resource declaration: %w", err)
		}
		body, err := readManaged(filepath.Join(oldDir, filename(resource)), r.maxBytes)
		if err != nil || digest(body) != state.SHA256 || Validate(resource, body) != nil {
			return stateDocument{}, fmt.Errorf("resources: legacy resource %q is invalid or changed", id)
		}
		path := filepath.Join(stageDir, filename(resource))
		if err := writeStaged(path, stageDir, body); err != nil {
			return stateDocument{}, err
		}
		if err := os.Chmod(path, 0o400); err != nil {
			return stateDocument{}, err
		}
		paths[id] = path
	}
	transactionDir, err := r.promote(previous, candidate, paths, false)
	if err != nil {
		return stateDocument{}, err
	}
	if err := finalizeTransaction(r.home, transactionDir); err != nil {
		return stateDocument{}, err
	}
	if err := removeLegacyGenerations(legacyRoot); err != nil {
		return stateDocument{}, err
	}
	return candidate, nil
}

func (r *Registry) promote(previous, candidate stateDocument, staged map[string]string, legacy bool) (string, error) {
	if err := ensureRoot(r.home); err != nil {
		return "", err
	}
	if previous.Version != 1 && previous.Version != 2 || candidate.Version != 2 || candidate.Resources == nil || candidate.CommitID == "" {
		return "", fmt.Errorf("resources: invalid promotion manifest")
	}
	previousNames, err := manifestNames(previous)
	if err != nil {
		return "", err
	}
	candidateNames, err := manifestNames(candidate)
	if err != nil {
		return "", err
	}
	nameToPath := make(map[string]string, len(staged))
	for id, path := range staged {
		state, ok := candidate.Resources[id]
		if !ok {
			return "", fmt.Errorf("resources: staged unknown resource %q", id)
		}
		name := resourceFilename(id, state)
		if _, exists := nameToPath[name]; exists {
			return "", fmt.Errorf("resources: duplicate managed destination %q", name)
		}
		nameToPath[name] = path
	}
	for name := range candidateNames {
		if nameToPath[name] == "" {
			return "", fmt.Errorf("resources: candidate resource %q has no staged file", name)
		}
	}
	transactionDir, err := makePrivateDir(r.home, transactionDirPrefix)
	if err != nil {
		return "", fmt.Errorf("resources: create transaction directory: %w", err)
	}
	keepTransactionDir := false
	defer func() {
		if !keepTransactionDir {
			_ = os.RemoveAll(transactionDir)
		}
	}()
	journal := transactionJournal{
		Version: 1, Phase: "promoting", BackupDir: filepath.Base(transactionDir),
		Previous: cloneState(previous), Candidate: cloneState(candidate),
	}
	names := make(map[string]struct{}, len(previousNames)+len(candidateNames))
	for name := range previousNames {
		names[name] = struct{}{}
	}
	for name := range candidateNames {
		names[name] = struct{}{}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	for index, name := range ordered {
		file := transactionFile{Name: name}
		if id, ok := previousNames[name]; ok && previous.Version == 2 {
			state := previous.Resources[id]
			oldPath := filepath.Join(r.home, name)
			body, err := readManaged(oldPath, r.maxBytes)
			if err != nil || digest(body) != state.SHA256 {
				return "", fmt.Errorf("resources: previous resource %q is missing or changed", id)
			}
			file.HadPrevious = true
			file.PreviousSHA256 = state.SHA256
			file.Backup = fmt.Sprintf("file-%03d.bak", index)
			if err := writeStaged(filepath.Join(transactionDir, file.Backup), transactionDir, body); err != nil {
				return "", err
			}
		} else if !legacy {
			if _, err := os.Lstat(filepath.Join(r.home, name)); err == nil {
				return "", fmt.Errorf("resources: unmanaged destination already exists: %s", name)
			} else if !os.IsNotExist(err) {
				return "", err
			}
		}
		if path := nameToPath[name]; path != "" {
			stateID := candidateNames[name]
			state := candidate.Resources[stateID]
			body, err := readManaged(path, r.maxBytes)
			if err != nil || digest(body) != state.SHA256 || Validate(config.Resource{ID: stateID, Kind: state.Kind, Format: state.Format, RuleType: state.RuleType, Enabled: true}, body) != nil {
				return "", fmt.Errorf("resources: staged candidate %q is invalid or changed", stateID)
			}
			file.CandidateSHA256 = state.SHA256
		}
		journal.Files = append(journal.Files, file)
	}
	if err := syncDirectory(transactionDir); err != nil {
		return "", err
	}
	journalPath := filepath.Join(r.home, transactionFileName)
	if _, err := os.Lstat(journalPath); err == nil {
		return "", fmt.Errorf("resources: another resource transaction is pending")
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := writeJSONAtomic(journalPath, r.home, journal); err != nil {
		if _, statErr := os.Lstat(journalPath); statErr == nil {
			keepTransactionDir = true
			return "", r.failPromotion(journalPath, transactionDir, err)
		}
		return "", err
	}
	keepTransactionDir = true
	for _, file := range journal.Files {
		path := filepath.Join(r.home, file.Name)
		if stagedPath := nameToPath[file.Name]; stagedPath != "" {
			body, err := readManaged(stagedPath, r.maxBytes)
			if err != nil {
				return "", r.failPromotion(journalPath, transactionDir, err)
			}
			if err := writeManagedAtomic(path, r.home, body, 0o400); err != nil {
				return "", r.failPromotion(journalPath, transactionDir, err)
			}
		} else if file.HadPrevious {
			if err := removeManaged(path, r.home); err != nil {
				return "", r.failPromotion(journalPath, transactionDir, err)
			}
		}
	}
	if err := syncDirectory(r.home); err != nil {
		return "", r.failPromotion(journalPath, transactionDir, err)
	}
	if err := verifyManifestFiles(r.home, candidate, r.maxBytes); err != nil {
		return "", r.failPromotion(journalPath, transactionDir, err)
	}
	if err := writeStateAtomic(filepath.Join(r.home, stateFileName), r.home, candidate); err != nil {
		return "", r.failPromotion(journalPath, transactionDir, err)
	}
	return transactionDir, nil
}

func (r *Registry) failPromotion(journalPath, transactionDir string, cause error) error {
	journal, dir, err := readTransaction(r.home, journalPath)
	if err == nil {
		err = restoreTransaction(r.home, journalPath, dir, journal)
	}
	if err != nil {
		return fmt.Errorf("%w; resource rollback failed: %v", cause, err)
	}
	_ = os.RemoveAll(transactionDir)
	return cause
}

func readTransaction(home, journalPath string) (transactionJournal, string, error) {
	info, err := os.Lstat(journalPath)
	if err != nil {
		return transactionJournal{}, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxStateBytes {
		return transactionJournal{}, "", fmt.Errorf("resources: unsafe transaction journal")
	}
	if err := os.Chmod(journalPath, 0o600); err != nil {
		return transactionJournal{}, "", err
	}
	data, err := os.ReadFile(journalPath)
	if err != nil {
		return transactionJournal{}, "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var journal transactionJournal
	if err := decoder.Decode(&journal); err != nil {
		return transactionJournal{}, "", fmt.Errorf("resources: decode transaction journal: %w", err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return transactionJournal{}, "", fmt.Errorf("resources: invalid trailing transaction data")
	}
	if err := validateJournal(journal); err != nil {
		return transactionJournal{}, "", err
	}
	dir := filepath.Join(home, journal.BackupDir)
	if err := verifyRealDirectory(dir, home); err != nil {
		return transactionJournal{}, "", fmt.Errorf("resources: transaction backup directory is unsafe: %w", err)
	}
	return journal, dir, nil
}

func validateJournal(journal transactionJournal) error {
	if journal.Version != 1 || journal.Phase != "promoting" || !validPrivateDir(journal.BackupDir) || !strings.HasPrefix(journal.BackupDir, transactionDirPrefix) {
		return fmt.Errorf("resources: invalid transaction journal")
	}
	if err := validateStateDocument(journal.Previous); err != nil {
		return err
	}
	if err := validateStateDocument(journal.Candidate); err != nil || journal.Candidate.Version != 2 {
		return fmt.Errorf("resources: invalid candidate transaction manifest")
	}
	previous, _ := manifestNames(journal.Previous)
	candidate, _ := manifestNames(journal.Candidate)
	allowed := make(map[string]struct{}, len(previous)+len(candidate))
	for name := range previous {
		allowed[name] = struct{}{}
	}
	for name := range candidate {
		allowed[name] = struct{}{}
	}
	seen, backups := make(map[string]struct{}, len(journal.Files)), make(map[string]struct{}, len(journal.Files))
	for _, file := range journal.Files {
		if _, ok := allowed[file.Name]; !ok {
			return fmt.Errorf("resources: transaction contains unmanaged destination")
		}
		if _, ok := seen[file.Name]; ok {
			return fmt.Errorf("resources: duplicate transaction destination")
		}
		seen[file.Name] = struct{}{}
		previousID, hadStableFile := previous[file.Name]
		expectPrevious := hadStableFile && journal.Previous.Version == 2
		if file.HadPrevious != expectPrevious {
			return fmt.Errorf("resources: transaction prior-file identity mismatch")
		}
		if expectPrevious {
			state := journal.Previous.Resources[previousID]
			if file.PreviousSHA256 != state.SHA256 || !validBackupName(file.Backup) {
				return fmt.Errorf("resources: invalid transaction backup entry")
			}
			if _, ok := backups[file.Backup]; ok {
				return fmt.Errorf("resources: duplicate transaction backup")
			}
			backups[file.Backup] = struct{}{}
		} else if file.Backup != "" || file.PreviousSHA256 != "" {
			return fmt.Errorf("resources: unexpected transaction backup")
		}
		candidateID, hasCandidate := candidate[file.Name]
		if hasCandidate {
			if file.CandidateSHA256 != journal.Candidate.Resources[candidateID].SHA256 {
				return fmt.Errorf("resources: transaction candidate identity mismatch")
			}
		} else if file.CandidateSHA256 != "" {
			return fmt.Errorf("resources: unexpected candidate transaction hash")
		}
	}
	if len(seen) != len(allowed) {
		return fmt.Errorf("resources: incomplete transaction journal")
	}
	return nil
}

func validBackupName(name string) bool {
	return len(name) == len("file-000.bak") && strings.HasPrefix(name, "file-") && strings.HasSuffix(name, ".bak")
}

func restoreTransaction(home, journalPath, backupDir string, journal transactionJournal) error {
	for _, file := range journal.Files {
		path := filepath.Join(home, file.Name)
		if file.HadPrevious {
			backupPath := filepath.Join(backupDir, file.Backup)
			body, err := readManaged(backupPath, DefaultMaxBytes)
			if err != nil || digest(body) != file.PreviousSHA256 {
				return fmt.Errorf("resources: transaction backup is missing or changed")
			}
			if err := writeManagedAtomic(path, home, body, 0o400); err != nil {
				return err
			}
			continue
		}
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		body, err := readManaged(path, DefaultMaxBytes)
		if err != nil || file.CandidateSHA256 == "" || digest(body) != file.CandidateSHA256 {
			return fmt.Errorf("resources: refusing to remove changed transaction destination")
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	if err := syncDirectory(home); err != nil {
		return err
	}
	if err := writeStateAtomic(filepath.Join(home, stateFileName), home, journal.Previous); err != nil {
		return err
	}
	if err := os.Remove(journalPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := syncDirectory(home); err != nil {
		return err
	}
	return os.RemoveAll(backupDir)
}

func finalizeTransaction(home, transactionDir string) error {
	journalPath := filepath.Join(home, transactionFileName)
	if err := os.Remove(journalPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := syncDirectory(home); err != nil {
		return err
	}
	if transactionDir != "" {
		if err := verifyRealDirectory(transactionDir, home); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.RemoveAll(transactionDir); err != nil {
			return err
		}
	}
	return nil
}

func manifestNames(document stateDocument) (map[string]string, error) {
	result := make(map[string]string, len(document.Resources))
	for id, state := range document.Resources {
		resource := config.Resource{ID: id, Kind: state.Kind, Format: state.Format, RuleType: state.RuleType, Enabled: true}
		if err := ValidateDeclaration(resource); err != nil {
			return nil, fmt.Errorf("resources: invalid manifest resource: %w", err)
		}
		name := filename(resource)
		if _, exists := result[name]; exists {
			return nil, fmt.Errorf("resources: duplicate manifest destination")
		}
		result[name] = id
	}
	return result, nil
}

func resourceFilename(id string, state resourceState) string {
	return filename(config.Resource{ID: id, Kind: state.Kind, Format: state.Format, RuleType: state.RuleType})
}

func verifyManifestFiles(home string, document stateDocument, maxBytes int64) error {
	for id, state := range document.Resources {
		resource := config.Resource{ID: id, Kind: state.Kind, Format: state.Format, RuleType: state.RuleType, Enabled: true}
		data, err := readManaged(filepath.Join(home, filename(resource)), maxBytes)
		if err != nil || digest(data) != state.SHA256 || Validate(resource, data) != nil {
			return fmt.Errorf("resources: promoted resource %q is invalid or changed", id)
		}
	}
	return nil
}

func writeManagedAtomic(path, parent string, body []byte, mode os.FileMode) error {
	if filepath.Dir(path) != parent {
		return fmt.Errorf("resources: invalid managed destination")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("resources: refusing unsafe managed destination")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	temp, err := os.CreateTemp(parent, ".resource-promote-*.tmp")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(body); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return syncDirectory(parent)
}

func removeManaged(path, parent string) error {
	if filepath.Dir(path) != parent {
		return fmt.Errorf("resources: invalid managed removal")
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("resources: refusing unsafe managed removal")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncDirectory(parent)
}

func writeJSONAtomic(path, parent string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if int64(len(data)) > maxStateBytes {
		return fmt.Errorf("resources: transaction journal exceeds size limit")
	}
	return writeManagedAtomic(path, parent, data, 0o600)
}

func makePrivateDir(parent, prefix string) (string, error) {
	id, err := newCommitID()
	if err != nil {
		return "", err
	}
	path := filepath.Join(parent, prefix+id)
	if err := os.Mkdir(path, 0o700); err != nil {
		return "", err
	}
	return path, nil
}

func validPrivateDir(name string) bool {
	for _, prefix := range []string{transactionDirPrefix, ".resource-stage-"} {
		if strings.HasPrefix(name, prefix) && len(name) == len(prefix)+32 {
			_, err := hexDecode(strings.TrimPrefix(name, prefix))
			return err == nil
		}
	}
	return false
}

func ensureRoot(home string) error {
	info, err := os.Lstat(home)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("resources: managed root is unsafe")
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func removeLegacyGenerations(root string) error {
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return nil
	}
	if err := verifyRealDirectory(root, filepath.Dir(root)); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() && validGeneration(entry.Name()) {
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func hexDecode(value string) ([]byte, error) {
	return hex.DecodeString(value)
}
