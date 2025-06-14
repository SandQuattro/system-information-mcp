package handlers

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

type Session struct {
	ID        string
	CreatedAt time.Time
	SSEChan   chan interface{}
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
	}
}

func (sm *SessionManager) CreateSession() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sessionID := uuid.New().String()
	sm.sessions[sessionID] = &Session{
		ID:        sessionID,
		CreatedAt: time.Now(),
		SSEChan:   make(chan interface{}, 100),
	}
	return sessionID
}

func (sm *SessionManager) GetSession(id string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	session, ok := sm.sessions[id]
	return session, ok
}

func (sm *SessionManager) RemoveSession(id string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if session, ok := sm.sessions[id]; ok {
		close(session.SSEChan)
		delete(sm.sessions, id)
	}
}
