package server

import "sync"

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
	maxTotal    int
	total       int
}

func NewPubSub(maxTotal int) *PubSub {
	return &PubSub{byWorkspace: make(map[string]map[uint64]chan CursorHint), maxTotal: maxTotal}
}

func (p *PubSub) Subscribe(workspaceID string) (Subscription, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.total >= p.maxTotal {
		return Subscription{}, ErrRateLimited
	}
	p.nextID++
	id := p.nextID
	channel := make(chan CursorHint, 1)
	if p.byWorkspace[workspaceID] == nil {
		p.byWorkspace[workspaceID] = make(map[uint64]chan CursorHint)
	}
	p.byWorkspace[workspaceID][id] = channel
	p.total++
	return Subscription{C: channel, cancel: func() { p.unsubscribe(workspaceID, id) }}, nil
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

func (p *PubSub) unsubscribe(workspaceID string, id uint64) {
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
	p.total--
	close(channel)
}

func (p *PubSub) subscriberCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.total
}
