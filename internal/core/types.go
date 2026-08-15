// Package core defines the durable knowledge state of Astra Harness.
//
// Astra is organized around Goal, Claim, Evidence, Unknown, Action and
// Execution instead of around long-lived agent threads. The agent is a
// disposable compute resource; the state is the durable intelligence.
package core

import "time"

// Status values shared by state entities.
const (
	StatusActive        = "ACTIVE"
	StatusDone          = "DONE"
	StatusPending       = "PENDING"
	StatusRunning       = "RUNNING"
	StatusSucceeded     = "SUCCEEDED"
	StatusFailed        = "FAILED"
	StatusBlocked       = "BLOCKED"
	StatusSkipped       = "SKIPPED"
	StatusCancelled     = "CANCELLED"
	StatusInvestigating = "INVESTIGATING"
	StatusResolved      = "RESOLVED"
)

// Claim lifecycle states from the design document.
const (
	ClaimUnknown      = "UNKNOWN"
	ClaimHypothesis   = "HYPOTHESIS"
	ClaimSupported    = "SUPPORTED"
	ClaimContradicted = "CONTRADICTED"
	ClaimVerified     = "VERIFIED"
	ClaimStale        = "STALE"
)

// Evidence kinds from the design document.
const (
	EvidenceSourceCode         = "SOURCE_CODE"
	EvidenceTestResult         = "TEST_RESULT"
	EvidenceBuildResult        = "BUILD_RESULT"
	EvidenceRuntimeResult      = "RUNTIME_RESULT"
	EvidenceStaticAnalysis     = "STATIC_ANALYSIS"
	EvidenceGitHistory         = "GIT_HISTORY"
	EvidenceDocumentation      = "DOCUMENTATION"
	EvidenceAgentReasoning     = "AGENT_REASONING"
	EvidenceHumanConfirmation  = "HUMAN_CONFIRMATION"
	EvidenceExperiment         = "EXPERIMENT"
	EvidenceBrowserObservation = "BROWSER_OBSERVATION"
)

// Action types from the design document.
const (
	ActionSearch     = "SEARCH"
	ActionRead       = "READ"
	ActionEdit       = "EDIT"
	ActionRunTest    = "RUN_TEST"
	ActionRunBuild   = "RUN_BUILD"
	ActionRunProgram = "RUN_PROGRAM"
	ActionDebug      = "DEBUG"
	ActionBrowser    = "BROWSER"
	ActionExperiment = "EXPERIMENT"
	ActionReview     = "REVIEW"
	ActionAskUser    = "ASK_USER"
	ActionSpawnAgent = "SPAWN_AGENT"
)

// Project is the repository-level metadata.
type Project struct {
	Name          string            `json:"name"`
	Root          string            `json:"root"`
	DefaultBranch string            `json:"default_branch,omitempty"`
	Languages     map[string]int    `json:"languages,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	InitializedAt time.Time         `json:"initialized_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// Goal is a durable objective with acceptance criteria.
type Goal struct {
	ID                 string    `json:"id"`
	Description        string    `json:"description"`
	Priority           int       `json:"priority"`
	Status             string    `json:"status"`
	AcceptanceCriteria []string  `json:"acceptance_criteria,omitempty"`
	Progress           float64   `json:"progress"` // heuristic 0..1
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// Claim is a knowledge statement with a lifecycle.
type Claim struct {
	ID          string    `json:"id"`
	Subject     string    `json:"subject"`
	Predicate   string    `json:"predicate"`
	Object      string    `json:"object"`
	ClaimType   string    `json:"claim_type,omitempty"`
	Status      string    `json:"status"`
	Confidence  float64   `json:"confidence"`
	EvidenceIDs []string  `json:"evidence_ids,omitempty"`
	Source      string    `json:"source,omitempty"`
	CodeState   string    `json:"code_state,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Evidence is a verifiable observation bound to a code state.
type Evidence struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Source     string            `json:"source"`
	Content    string            `json:"content"`
	Hash       string            `json:"hash"`
	Commit     string            `json:"commit,omitempty"`
	CodeState  string            `json:"code_state,omitempty"`
	Confidence float64           `json:"confidence"`
	ClaimIDs   []string          `json:"claim_ids,omitempty"`
	Status     string            `json:"status"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
}

// Unknown is an explicit statement of what is not known.
type Unknown struct {
	ID               string    `json:"id"`
	Description      string    `json:"description"`
	Impact           float64   `json:"impact"`            // 0..1
	Confidence       float64   `json:"confidence"`        // confidence the unknown matters; uncertainty = 1 - confidence
	ResolutionCost   float64   `json:"resolution_cost"`   // normalized 0..1
	DependencyWeight float64   `json:"dependency_weight"` // 0..1
	Dependencies     []string  `json:"dependencies,omitempty"`
	Status           string    `json:"status"`
	Priority         float64   `json:"priority"`
	Source           string    `json:"source,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Action records a decision made by the harness.
type Action struct {
	ID                   string         `json:"id"`
	Type                 string         `json:"type"`
	Description          string         `json:"description"`
	Tool                 string         `json:"tool,omitempty"`
	Input                map[string]any `json:"input,omitempty"`
	Cost                 float64        `json:"cost"`
	Risk                 float64        `json:"risk"`
	ExpectedInfoGain     float64        `json:"expected_info_gain"`
	ExpectedGoalProgress float64        `json:"expected_goal_progress"`
	Utility              float64        `json:"utility"`
	Status               string         `json:"status"`
	ResultSummary        string         `json:"result_summary,omitempty"`
	EvidenceIDs          []string       `json:"evidence_ids,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
	StartedAt            time.Time      `json:"started_at,omitempty"`
	FinishedAt           time.Time      `json:"finished_at,omitempty"`
}

// Event is an append-only, replayable state transition.
type Event struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	Data      map[string]any `json:"data,omitempty"`
}

// State is the materialized view produced by the event reducer.
type State struct {
	Version   int         `json:"version"`
	Project   *Project    `json:"project,omitempty"`
	Goals     []*Goal     `json:"goals,omitempty"`
	Claims    []*Claim    `json:"claims,omitempty"`
	Evidence  []*Evidence `json:"evidence,omitempty"`
	Unknowns  []*Unknown  `json:"unknowns,omitempty"`
	Actions   []*Action   `json:"actions,omitempty"`
	Events    []*Event    `json:"events,omitempty"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// Event types.
const (
	EvtProjectInitialized = "PROJECT_INITIALIZED"
	EvtGoalCreated        = "GOAL_CREATED"
	EvtGoalUpdated        = "GOAL_UPDATED"
	EvtClaimCreated       = "CLAIM_CREATED"
	EvtClaimUpdated       = "CLAIM_UPDATED"
	EvtEvidenceCreated    = "EVIDENCE_CREATED"
	EvtUnknownCreated     = "UNKNOWN_CREATED"
	EvtUnknownUpdated     = "UNKNOWN_UPDATED"
	EvtActionCreated      = "ACTION_CREATED"
	EvtActionUpdated      = "ACTION_UPDATED"
)

// Session is a persisted conversation/run transcript.
type Session struct {
	ID        string           `json:"id"`
	Root      string           `json:"root"`
	Provider  string           `json:"provider,omitempty"`
	Model     string           `json:"model,omitempty"`
	GoalID    string           `json:"goal_id,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
	Messages  []SessionMessage `json:"messages,omitempty"`
}

// SessionMessage mirrors the LLM message shape for replay.
type SessionMessage struct {
	Role       string    `json:"role"`
	Content    string    `json:"content,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
	ToolName   string    `json:"tool_name,omitempty"`
	IsFinal    bool      `json:"is_final,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}
