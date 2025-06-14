package types

import (
	"sync"
	"time"
)

// Session представляет сессию MCP
type Session struct {
	ID           string
	CreatedAt    time.Time
	LastActivity time.Time
	SSEChan      chan map[string]interface{}
	mu           sync.RWMutex
	eventCounter int64
	storedEvents []StoredEvent
	maxEvents    int
}

// StoredEvent представляет сохраненное событие для воспроизведения
type StoredEvent struct {
	ID   int64
	Data interface{}
}

// NewSession создает новую сессию
func NewSession(id string) *Session {
	return &Session{
		ID:           id,
		CreatedAt:    time.Now(),
		LastActivity: time.Now(),
		SSEChan:      make(chan map[string]interface{}, 100),
		maxEvents:    100,
	}
}

// UpdateActivity обновляет время последней активности
func (s *Session) UpdateActivity() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastActivity = time.Now()
}

// StoreEvent сохраняет событие и возвращает уникальный ID
func (s *Session) StoreEvent(data interface{}) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.eventCounter++
	event := StoredEvent{
		ID:   s.eventCounter,
		Data: data,
	}

	s.storedEvents = append(s.storedEvents, event)

	// Ограничиваем количество сохраненных событий
	if len(s.storedEvents) > s.maxEvents {
		s.storedEvents = s.storedEvents[len(s.storedEvents)-s.maxEvents:]
	}

	return s.eventCounter
}

// GetEventsAfter возвращает события после указанного ID
func (s *Session) GetEventsAfter(lastEventID int64) []StoredEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []StoredEvent
	for _, event := range s.storedEvents {
		if event.ID > lastEventID {
			result = append(result, event)
		}
	}
	return result
}

// GetCurrentEventID возвращает текущий ID события
func (s *Session) GetCurrentEventID() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.eventCounter
}

// Close закрывает сессию
func (s *Session) Close() {
	close(s.SSEChan)
}

// SessionManager управляет сессиями
type SessionManager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

// NewSessionManager создает новый менеджер сессий
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
	}
}

// CreateSession создает новую сессию
func (sm *SessionManager) CreateSession() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sessionID := generateSessionID()
	session := NewSession(sessionID)
	sm.sessions[sessionID] = session

	return sessionID
}

// GetSession получает сессию по ID
func (sm *SessionManager) GetSession(sessionID string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, exists := sm.sessions[sessionID]
	if exists {
		session.UpdateActivity()
	}
	return session, exists
}

// RemoveSession удаляет сессию
func (sm *SessionManager) RemoveSession(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if session, exists := sm.sessions[sessionID]; exists {
		session.Close()
		delete(sm.sessions, sessionID)
	}
}

// CleanupExpiredSessions удаляет истекшие сессии
func (sm *SessionManager) CleanupExpiredSessions(maxAge time.Duration) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	for sessionID, session := range sm.sessions {
		if now.Sub(session.LastActivity) > maxAge {
			session.Close()
			delete(sm.sessions, sessionID)
		}
	}
}

// generateSessionID генерирует уникальный ID сессии
func generateSessionID() string {
	return "session_" + time.Now().Format("20060102_150405_") + randomString(8)
}

// randomString генерирует случайную строку
func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}
