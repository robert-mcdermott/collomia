package session

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Recovery records contain runtime obligations, never provider-authored notes.
// Receipts and permission grants are deliberately absent.
func validateRecoveryRecord(raw json.RawMessage, limit int) error {
	if len(raw) == 0 || len(raw) > limit {
		return errors.New("invalid recovery record size")
	}
	var header struct {
		Schema int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return err
	}
	if header.Schema != 1 {
		return errors.New("unsupported recovery record schema")
	}
	return nil
}
func (s *Session) LoadCompletion() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(json.RawMessage(nil), s.completionRaw...)
}
func (s *Session) SaveCompletion(raw json.RawMessage) error {
	if err := validateRecoveryRecord(raw, 128<<10); err != nil {
		return err
	}
	if bytes.Equal(s.LoadCompletion(), raw) {
		return s.Sync()
	}
	if err := s.append(Record{Type: "completion_state", Recovery: raw}); err != nil {
		return err
	}
	if err := s.Sync(); err != nil {
		return err
	}
	s.mu.Lock()
	s.completionRaw = append(json.RawMessage(nil), raw...)
	s.mu.Unlock()
	return nil
}
func (s *Session) LoadWorkspaceCheckpoint() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(json.RawMessage(nil), s.workspaceCheckpointRaw...)
}
func (s *Session) SaveWorkspaceCheckpoint(raw json.RawMessage) error {
	if err := validateRecoveryRecord(raw, 16<<20); err != nil {
		return err
	}
	delta, err := makeCheckpointDelta(s.LoadWorkspaceCheckpoint(), raw)
	if err != nil {
		return err
	}
	if err := s.append(Record{Type: "workspace_checkpoint_delta", Recovery: delta}); err != nil {
		return err
	}
	if err := s.Sync(); err != nil {
		return err
	}
	s.mu.Lock()
	s.workspaceCheckpointRaw = append(json.RawMessage(nil), raw...)
	s.mu.Unlock()
	return nil
}

// Checkpoint deltas avoid duplicating all retained file bytes for every edit.
// Append-only history remains auditable; the current projection is bounded.
type checkpointDelta struct {
	Schema  int                        `json:"schema_version"`
	Drop    int                        `json:"drop"`
	Keep    int                        `json:"keep"`
	Header  map[string]json.RawMessage `json:"header"`
	Entries []json.RawMessage          `json:"entries,omitempty"`
}

func checkpointParts(raw json.RawMessage) (map[string]json.RawMessage, []json.RawMessage, error) {
	header := map[string]json.RawMessage{}
	if len(raw) == 0 {
		return header, nil, nil
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, nil, err
	}
	var entries []json.RawMessage
	if data := header["entries"]; len(data) > 0 {
		if err := json.Unmarshal(data, &entries); err != nil {
			return nil, nil, err
		}
	}
	delete(header, "entries")
	return header, entries, nil
}
func makeCheckpointDelta(old, next json.RawMessage) (json.RawMessage, error) {
	_, prior, err := checkpointParts(old)
	if err != nil {
		return nil, err
	}
	header, entries, err := checkpointParts(next)
	if err != nil {
		return nil, err
	}
	drop, keep := 0, 0
	if len(entries) > 0 {
		for i := 0; i < len(prior); i++ {
			n := 0
			for i+n < len(prior) && n < len(entries) && bytes.Equal(prior[i+n], entries[n]) {
				n++
			}
			if n > keep {
				drop, keep = i, n
			}
		}
	}
	return json.Marshal(checkpointDelta{Schema: 1, Drop: drop, Keep: keep, Header: header, Entries: entries[keep:]})
}
func applyCheckpointDelta(old, raw json.RawMessage) (json.RawMessage, error) {
	if err := validateRecoveryRecord(raw, 16<<20); err != nil {
		return nil, err
	}
	var delta checkpointDelta
	if err := json.Unmarshal(raw, &delta); err != nil {
		return nil, err
	}
	_, prior, err := checkpointParts(old)
	if err != nil {
		return nil, err
	}
	if delta.Drop < 0 || delta.Keep < 0 || delta.Drop > len(prior) || delta.Keep > len(prior)-delta.Drop || delta.Keep+len(delta.Entries) > 256 || delta.Header == nil {
		return nil, errors.New("invalid checkpoint delta history")
	}
	entries := append(append([]json.RawMessage(nil), prior[delta.Drop:delta.Drop+delta.Keep]...), delta.Entries...)
	data, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	delta.Header["entries"] = data
	result, err := json.Marshal(delta.Header)
	if err != nil {
		return nil, err
	}
	return result, validateRecoveryRecord(result, 16<<20)
}
