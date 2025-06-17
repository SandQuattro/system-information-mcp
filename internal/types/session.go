package types

import (
	"crypto/rand"
	"sync"
	"time"

	"mcp-system-info/internal/logger"
)

// Session представляет сессию MCP
type Session struct {
	ID           string
	CreatedAt    time.Time
	LastActivity time.Time
	Initialized  bool // Флаг что клиент отправил notifications/initialized
	mu           sync.RWMutex
}

// NewSession создает новую сессию
func NewSession(id string) *Session {
	logger.Session.Debug().
		Str("session_id", id).
		Msg("Creating new session")

	return &Session{
		ID:           id,
		CreatedAt:    time.Now(),
		LastActivity: time.Now(),
	}
}

// UpdateActivity обновляет время последней активности
func (s *Session) UpdateActivity() {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldActivity := s.LastActivity
	s.LastActivity = time.Now()

	if time.Since(oldActivity) > 5*time.Minute {
		logger.Session.Debug().
			Str("session_id", s.ID).
			Time("last_activity", oldActivity).
			Msg("Session activity updated after long period")
	}
}

// SetInitialized устанавливает флаг что клиент завершил инициализацию
func (s *Session) SetInitialized() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Initialized = true
}

// IsInitialized проверяет завершена ли инициализация
func (s *Session) IsInitialized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Initialized
}

// Close закрывает сессию
func (s *Session) Close() {
	logger.Session.Info().
		Str("session_id", s.ID).
		Time("created_at", s.CreatedAt).
		Time("last_activity", s.LastActivity).
		Bool("was_initialized", s.Initialized).
		Dur("session_duration", time.Since(s.CreatedAt)).
		Msg("Closing session")
}

// SessionManager управляет сессиями
type SessionManager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

// NewSessionManager создает новый менеджер сессий
func NewSessionManager() *SessionManager {
	logger.Session.Info().Msg("Creating new session manager")

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

	logger.Session.Info().
		Str("session_id", sessionID).
		Int("total_sessions", len(sm.sessions)).
		Msg("Session created")

	return sessionID
}

// GetSession получает сессию по ID
func (sm *SessionManager) GetSession(sessionID string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, exists := sm.sessions[sessionID]
	if exists {
		session.UpdateActivity()
		logger.Session.Trace().
			Str("session_id", sessionID).
			Msg("Session accessed")
	} else {
		// Создаем список всех существующих сессий для диагностики
		var existingSessions []string
		for sid := range sm.sessions {
			existingSessions = append(existingSessions, sid)
		}

		logger.Session.Warn().
			Str("session_id", sessionID).
			Strs("existing_sessions", existingSessions).
			Int("total_sessions", len(sm.sessions)).
			Msg("Session not found")
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

		logger.Session.Info().
			Str("session_id", sessionID).
			Int("remaining_sessions", len(sm.sessions)).
			Msg("Session removed")
	} else {
		logger.Session.Warn().
			Str("session_id", sessionID).
			Msg("Attempted to remove non-existent session")
	}
}

// CleanupExpiredSessions удаляет истекшие сессии
func (sm *SessionManager) CleanupExpiredSessions(maxAge time.Duration) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	var expiredCount int

	for sessionID, session := range sm.sessions {
		if now.Sub(session.LastActivity) > maxAge {
			session.Close()
			delete(sm.sessions, sessionID)
			expiredCount++
		}
	}

	if expiredCount > 0 {
		logger.Session.Info().
			Int("expired_sessions", expiredCount).
			Int("remaining_sessions", len(sm.sessions)).
			Dur("max_age", maxAge).
			Msg("Cleaned up expired sessions")
	}
}

// generateSessionID генерирует уникальный ID сессии
func generateSessionID() string {
	return "session_" + time.Now().Format("20060102_150405_") + randomString(8)
}

// randomString генерирует случайную строку используя crypto/rand
func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)

	// Используем crypto/rand для безопасной генерации случайных чисел
	if _, err := rand.Read(b); err != nil {
		// Fallback к time-based generation в случае ошибки
		for i := range b {
			b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
		}
		return string(b)
	}

	for i := range b {
		b[i] = charset[b[i]%byte(len(charset))]
	}
	return string(b)
}
