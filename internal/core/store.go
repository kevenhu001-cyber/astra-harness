package core

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	stateVersion = 1
	stateFile    = "state.json"
	eventsFile   = "events.jsonl"
	sessionDir   = "sessions"
)

// Store is the durable State Core. All mutations are appended to the event
// log first, then reduced into the materialized state and persisted.
type Store struct {
	mu     sync.Mutex
	dir    string
	State  *State
	events *os.File
}

// NewStore opens (or creates) the Astra state directory under root/.astra.
func NewStore(root string) (*Store, error) {
	dir := filepath.Join(root, ".astra")
	if err := os.MkdirAll(filepath.Join(dir, sessionDir), 0o755); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	s := &Store{dir: dir}
	s.State = &State{Version: stateVersion, UpdatedAt: time.Now().UTC()}
	if err := s.load(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, eventsFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open event log: %w", err)
	}
	s.events = f
	return s, nil
}

func (s *Store) load() error {
	st := filepath.Join(s.dir, stateFile)
	data, err := os.ReadFile(st)
	if err == nil {
		var state State
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("parse %s: %w", st, err)
		}
		s.State = &state
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", st, err)
	}
	// Rebuild from the event log if the snapshot is missing.
	ev := filepath.Join(s.dir, eventsFile)
	f, err := os.Open(ev)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s.save()
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var evt Event
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			return fmt.Errorf("parse event log line: %w", err)
		}
		if err := s.apply(evt); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return s.save()
}

func (s *Store) save() error {
	s.State.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(s.State, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, stateFile+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.dir, stateFile))
}

// AppendEvent appends a transition to the log and materializes it.
func (s *Store) AppendEvent(evtType string, data map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	evt := Event{
		ID:        NewID("evt"),
		Type:      evtType,
		Timestamp: time.Now().UTC(),
		Data:      data,
	}
	line, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	if _, err := s.events.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	if err := s.apply(evt); err != nil {
		return err
	}
	return s.save()
}

// ApplyEvent applies an event to the in-memory state (used for replay).
func (s *Store) ApplyEvent(evt Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.apply(evt)
}

func (s *Store) apply(evt Event) error {
	st := s.State
	evtCopy := evt
	st.Events = append(st.Events, &evtCopy)
	switch evt.Type {
	case EvtProjectInitialized:
		var p Project
		if err := decode(evt.Data, &p); err != nil {
			return err
		}
		st.Project = &p
	case EvtGoalCreated:
		var g Goal
		if err := decode(evt.Data, &g); err != nil {
			return err
		}
		st.Goals = append(st.Goals, &g)
	case EvtGoalUpdated:
		var g Goal
		if err := decode(evt.Data, &g); err != nil {
			return err
		}
		upsert(&st.Goals, &g, func(x *Goal) bool { return x.ID == g.ID })
	case EvtClaimCreated:
		var c Claim
		if err := decode(evt.Data, &c); err != nil {
			return err
		}
		st.Claims = append(st.Claims, &c)
	case EvtClaimUpdated:
		var c Claim
		if err := decode(evt.Data, &c); err != nil {
			return err
		}
		upsert(&st.Claims, &c, func(x *Claim) bool { return x.ID == c.ID })
	case EvtEvidenceCreated:
		var e Evidence
		if err := decode(evt.Data, &e); err != nil {
			return err
		}
		st.Evidence = append(st.Evidence, &e)
	case EvtUnknownCreated:
		var u Unknown
		if err := decode(evt.Data, &u); err != nil {
			return err
		}
		st.Unknowns = append(st.Unknowns, &u)
	case EvtUnknownUpdated:
		var u Unknown
		if err := decode(evt.Data, &u); err != nil {
			return err
		}
		upsert(&st.Unknowns, &u, func(x *Unknown) bool { return x.ID == u.ID })
	case EvtActionCreated:
		var a Action
		if err := decode(evt.Data, &a); err != nil {
			return err
		}
		st.Actions = append(st.Actions, &a)
	case EvtActionUpdated:
		var a Action
		if err := decode(evt.Data, &a); err != nil {
			return err
		}
		upsert(&st.Actions, &a, func(x *Action) bool { return x.ID == a.ID })
	default:
		return nil
	}
	return nil
}

func decode(data map[string]any, out any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func upsert[T any](slice *[]*T, item *T, match func(*T) bool) {
	for i, x := range *slice {
		if match(x) {
			(*slice)[i] = item
			return
		}
	}
	*slice = append(*slice, item)
}

// Mutators ------------------------------------------------------------------

func (s *Store) SetProject(p *Project) error {
	return s.AppendEvent(EvtProjectInitialized, mustMap(p))
}

func (s *Store) AddGoal(g *Goal) error {
	return s.AppendEvent(EvtGoalCreated, mustMap(g))
}

func (s *Store) UpdateGoal(g *Goal) error {
	return s.AppendEvent(EvtGoalUpdated, mustMap(g))
}

func (s *Store) AddClaim(c *Claim) error {
	return s.AppendEvent(EvtClaimCreated, mustMap(c))
}

func (s *Store) UpdateClaim(c *Claim) error {
	return s.AppendEvent(EvtClaimUpdated, mustMap(c))
}

func (s *Store) AddEvidence(e *Evidence) error {
	return s.AppendEvent(EvtEvidenceCreated, mustMap(e))
}

func (s *Store) AddUnknown(u *Unknown) error {
	return s.AppendEvent(EvtUnknownCreated, mustMap(u))
}

func (s *Store) UpdateUnknown(u *Unknown) error {
	return s.AppendEvent(EvtUnknownUpdated, mustMap(u))
}

func (s *Store) AddAction(a *Action) error {
	return s.AppendEvent(EvtActionCreated, mustMap(a))
}

func (s *Store) UpdateAction(a *Action) error {
	return s.AppendEvent(EvtActionUpdated, mustMap(a))
}

func mustMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		panic(err)
	}
	return m
}

// Queries -------------------------------------------------------------------

func (s *Store) ActiveGoal() *Goal {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.State == nil {
		return nil
	}
	var best *Goal
	for _, g := range s.State.Goals {
		if g.Status != StatusActive {
			continue
		}
		if best == nil || g.Priority > best.Priority || g.UpdatedAt.After(best.UpdatedAt) {
			best = g
		}
	}
	return best
}

func (s *Store) ClaimsByStatus(status string) []*Claim {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Claim
	for _, c := range s.State.Claims {
		if status == "" || c.Status == status {
			out = append(out, c)
		}
	}
	return out
}

func (s *Store) UnknownsByStatus(status string) []*Unknown {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Unknown
	for _, u := range s.State.Unknowns {
		if status == "" || u.Status == status {
			out = append(out, u)
		}
	}
	return out
}

func (s *Store) EvidenceRecent(n int) []*Evidence {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]*Evidence(nil), s.State.Evidence...)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func (s *Store) ActionsRecent(n int) []*Action {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]*Action(nil), s.State.Actions...)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// Sessions ------------------------------------------------------------------

func (s *Store) SessionPath(id string) string {
	return filepath.Join(s.dir, sessionDir, id+".json")
}

func (s *Store) SaveSession(sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.SessionPath(sess.ID), data, 0o644)
}

func (s *Store) LoadSession(id string) (*Session, error) {
	data, err := os.ReadFile(s.SessionPath(id))
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *Store) ListSessions() ([]*Session, error) {
	dir := filepath.Join(s.dir, sessionDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		sess, err := s.LoadSession(strings.TrimSuffix(e.Name(), ".json"))
		if err == nil {
			out = append(out, sess)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.events != nil {
		return s.events.Close()
	}
	return nil
}
