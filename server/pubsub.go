package server

import "sync"

// maxSubscriptionsPerUser bounds how many live cursor streams a single user
// may hold. Without it one account could occupy every slot in the global
// budget and silently disable live sync for everyone else.
const maxSubscriptionsPerUser = 8

type CursorHint struct {
	Protocol    string `json:"protocol"`
	WorkspaceID string `json:"workspace_id"`
	LatestSeq   int64  `json:"latest_seq"`
	CursorEpoch int64  `json:"cursor_epoch"`
}

type Subscription struct {
	C      <-chan CursorHint
	cancel func()
}

func (s Subscription) Close() {
	if s.cancel != nil {
		s.cancel()
	}
}

type PubSub struct {
	mu          sync.Mutex
	nextID      uint64
	byWorkspace map[string]map[uint64]chan CursorHint
	byUser      map[string]int
	maxTotal    int
	total       int
}

func NewPubSub(maxTotal int) *PubSub {
	return &PubSub{
		byWorkspace: make(map[string]map[uint64]chan CursorHint),
		byUser:      make(map[string]int),
		maxTotal:    maxTotal,
	}
}

// Subscribe registers one live cursor stream for userID on workspaceID. It
// enforces both the global budget and the per-user quota so a single account
// cannot starve the others.
func (p *PubSub) Subscribe(workspaceID, userID string) (Subscription, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.total >= p.maxTotal || p.byUser[userID] >= maxSubscriptionsPerUser {
		return Subscription{}, ErrRateLimited
	}
	p.nextID++
	id := p.nextID
	channel := make(chan CursorHint, 1)
	if p.byWorkspace[workspaceID] == nil {
		p.byWorkspace[workspaceID] = make(map[uint64]chan CursorHint)
	}
	p.byWorkspace[workspaceID][id] = channel
	p.byUser[userID]++
	p.total++
	return Subscription{C: channel, cancel: func() { p.unsubscribe(workspaceID, userID, id) }}, nil
}

func (p *PubSub) Publish(hint CursorHint) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, channel := range p.byWorkspace[hint.WorkspaceID] {
		select {
		case channel <- hint:
		default:
			select {
			case <-channel:
			default:
			}
			select {
			case channel <- hint:
			default:
			}
		}
	}
}

func (p *PubSub) unsubscribe(workspaceID, userID string, id uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	workspace := p.byWorkspace[workspaceID]
	channel, exists := workspace[id]
	if !exists {
		return
	}
	delete(workspace, id)
	if len(workspace) == 0 {
		delete(p.byWorkspace, workspaceID)
	}
	if p.byUser[userID]--; p.byUser[userID] <= 0 {
		delete(p.byUser, userID)
	}
	p.total--
	close(channel)
}

func (p *PubSub) subscriberCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.total
}
