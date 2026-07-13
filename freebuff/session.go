package freebuff

import (
	"context"
	"strings"
	"sync"
)

// SessionManager caches and switches Freebuff sessions per model for one token.
type SessionManager struct {
	client *Client
	mu     sync.Mutex
	cache  map[string]Session
}

// NewSessionManager creates a manager bound to one client/token.
func NewSessionManager(client *Client) *SessionManager {
	return &SessionManager{client: client, cache: map[string]Session{}}
}

// AcquireSession locks the manager, ensures a session for model, and returns a lease.
// Caller must Unlock via Lease.Release.
func (m *SessionManager) AcquireSession(ctx context.Context, model string, messages []map[string]any) (Session, func(), error) {
	m.mu.Lock()
	session, err := m.ensureLocked(ctx, model, messages)
	if err != nil {
		m.mu.Unlock()
		return Session{}, nil, err
	}
	release := func() { m.mu.Unlock() }
	return session, release, nil
}

func (m *SessionManager) ensureLocked(ctx context.Context, model string, messages []map[string]any) (Session, error) {
	if rl, ok := m.client.CachedRateLimit(model); ok {
		return Session{}, &Error{Message: rl.FormatError(), StatusCode: 429}
	}

	if cached, ok := m.cache[model]; ok && cached.IsFresh() {
		data, err := m.client.GetSession(ctx, cached.InstanceID)
		if err == nil {
			status := stringField(data, "status")
			upstreamModel := stringField(data, "model")
			if status == "active" && (upstreamModel == "" || upstreamModel == model) {
				if v, ok := data["remainingMs"].(float64); ok {
					ms := int(v)
					cached.RemainingMs = &ms
					m.cache[model] = cached
				}
				return cached, nil
			}
			if status == "active" {
				delete(m.cache, model)
			}
		} else {
			delete(m.cache, model)
		}
	}

	if active, ok := m.deleteLockedSession(ctx, model); ok {
		return active, nil
	}
	m.client.RequestAdChain(ctx, messages, "waiting_room", true)

	session, err := m.client.CreateSession(ctx, model)
	if err != nil {
		if strings.Contains(err.Error(), "model_locked") {
			_ = m.client.DeleteSession(ctx)
			m.cache = map[string]Session{}
			m.client.RequestAdChain(ctx, messages, "waiting_room", true)
			session, err = m.client.CreateSession(ctx, model)
		}
		if err != nil {
			return Session{}, err
		}
	}
	m.cache[model] = session
	return session, nil
}

func (m *SessionManager) deleteLockedSession(ctx context.Context, requested string) (Session, bool) {
	data, err := m.client.GetSession(ctx, "")
	if err != nil {
		return Session{}, false
	}
	if stringField(data, "status") != "active" {
		return Session{}, false
	}
	current := stringField(data, "model")
	instanceID := stringField(data, "instanceId")
	if current == requested && instanceID != "" {
		s := Session{
			InstanceID: instanceID,
			Model:      current,
			ExpiresAt:  stringField(data, "expiresAt"),
		}
		if v, ok := data["remainingMs"].(float64); ok {
			ms := int(v)
			s.RemainingMs = &ms
		}
		m.cache[requested] = s
		return s, true
	}
	if current == "" || current == requested {
		return Session{}, false
	}
	_ = m.client.DeleteSession(ctx)
	m.cache = map[string]Session{}
	return Session{}, false
}

// AccountPool leases one of N Freebuff tokens so concurrent model switches do not collide.
type AccountPool struct {
	accounts []*account
	mu       sync.Mutex
	cond     *sync.Cond
	next     int
}

type account struct {
	client   *Client
	sessions *SessionManager
	busy     bool
}

// AccountLease holds an account + session for the duration of one request.
type AccountLease struct {
	Client  *Client
	Session Session
	release func()
}

// Release unlocks the session manager and frees the account slot.
func (l *AccountLease) Release() {
	if l == nil || l.release == nil {
		return
	}
	l.release()
	l.release = nil
}

// NewAccountPool builds one account per token in storage.
func NewAccountPool(storage AuthStorage, hostProxy string) (*AccountPool, error) {
	tokens := storage.TokenList()
	if len(tokens) == 0 {
		return nil, &Error{Message: "freebuff auth has no token", StatusCode: 401}
	}
	p := &AccountPool{}
	p.cond = sync.NewCond(&p.mu)
	for _, token := range tokens {
		settings := storage.ToSettings(token, hostProxy)
		client := NewClient(settings)
		p.accounts = append(p.accounts, &account{
			client:   client,
			sessions: NewSessionManager(client),
		})
	}
	return p, nil
}

// DefaultClient returns the first account's client.
func (p *AccountPool) DefaultClient() *Client {
	if p == nil || len(p.accounts) == 0 {
		return nil
	}
	return p.accounts[0].client
}

// AcquireSession reserves an idle account and its Freebuff session.
func (p *AccountPool) AcquireSession(ctx context.Context, model string, messages []map[string]any) (*AccountLease, error) {
	idx := p.reserve()
	acc := p.accounts[idx]
	session, releaseSession, err := acc.sessions.AcquireSession(ctx, model, messages)
	if err != nil {
		p.release(idx)
		return nil, err
	}
	return &AccountLease{
		Client:  acc.client,
		Session: session,
		release: func() {
			releaseSession()
			p.release(idx)
		},
	}, nil
}

func (p *AccountPool) reserve() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		if idx := p.nextAvailableLocked(); idx >= 0 {
			p.accounts[idx].busy = true
			p.next = (idx + 1) % len(p.accounts)
			return idx
		}
		p.cond.Wait()
	}
}

func (p *AccountPool) release(idx int) {
	p.mu.Lock()
	p.accounts[idx].busy = false
	p.cond.Signal()
	p.mu.Unlock()
}

func (p *AccountPool) nextAvailableLocked() int {
	n := len(p.accounts)
	for offset := 0; offset < n; offset++ {
		idx := (p.next + offset) % n
		if !p.accounts[idx].busy {
			return idx
		}
	}
	return -1
}

// StartRunChain starts the Freebuff agent-run / context-pruner chain for a model.
func StartRunChain(ctx context.Context, client *Client, model Model) (Run, error) {
	if model.ParentAgentID != "" {
		return startChildChatRunChain(ctx, client, model)
	}
	agentID := model.AgentID
	startedAt := UTCNowISO()
	runID, err := client.StartRun(ctx, agentID, nil)
	if err != nil {
		return Run{}, err
	}
	childStarted := UTCNowISO()
	childRunID, err := client.StartRun(ctx, ContextPrunerAgentID, []string{runID})
	if err != nil {
		return Run{}, err
	}
	if err := client.RecordRunStep(ctx, childRunID, 1, nil, childStarted, nil); err != nil {
		return Run{}, err
	}
	if err := client.FinishRun(ctx, childRunID, 2); err != nil {
		return Run{}, err
	}
	if err := client.RecordRunStep(ctx, runID, 1, nil, startedAt, []string{childRunID}); err != nil {
		return Run{}, err
	}
	return Run{
		RunID:      runID,
		AgentID:    agentID,
		StartedAt:  startedAt,
		ChildRunID: childRunID,
	}, nil
}

func startChildChatRunChain(ctx context.Context, client *Client, model Model) (Run, error) {
	startedAt := UTCNowISO()
	parentRunID, err := client.StartRun(ctx, model.ParentAgentID, nil)
	if err != nil {
		return Run{}, err
	}
	chatStarted := UTCNowISO()
	chatRunID, err := client.StartRun(ctx, model.AgentID, []string{parentRunID})
	if err != nil {
		return Run{}, err
	}
	return Run{
		RunID:         parentRunID,
		AgentID:       model.ParentAgentID,
		StartedAt:     startedAt,
		ChildRunID:    chatRunID,
		ChatRunID:     chatRunID,
		ChatStartedAt: chatStarted,
	}, nil
}

// FinalizeRun records steps and finishes the agent run chain.
func FinalizeRun(ctx context.Context, client *Client, run Run, messageID string) {
	var msgPtr *string
	if messageID != "" {
		msgPtr = &messageID
	}
	if run.ChatRunID != "" && run.ChatRunID != run.RunID {
		start := run.ChatStartedAt
		if start == "" {
			start = run.StartedAt
		}
		_ = client.RecordRunStep(ctx, run.ChatRunID, 1, msgPtr, start, nil)
		_ = client.FinishRun(ctx, run.ChatRunID, 2)
		_ = client.RecordRunStep(ctx, run.RunID, 1, nil, run.StartedAt, []string{run.ChatRunID})
		_ = client.FinishRun(ctx, run.RunID, 2)
		return
	}
	_ = client.RecordRunStep(ctx, run.RunID, 2, msgPtr, run.StartedAt, nil)
	_ = client.FinishRun(ctx, run.RunID, 3)
}
