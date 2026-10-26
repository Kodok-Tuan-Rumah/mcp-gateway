package circuitbreaker

// Global manager instance, set during startup.
var globalManager *Manager

// SetGlobalManager sets the global circuit breaker manager.
func SetGlobalManager(m *Manager) {
	globalManager = m
}

// GlobalManager returns the global circuit breaker manager, or nil if not set.
func GlobalManager() *Manager {
	return globalManager
}

// GetBreaker returns the circuit breaker for the given service name.
// If no global manager is set, returns nil.
func GetBreaker(name string) *Breaker {
	if globalManager == nil {
		return nil
	}
	return globalManager.Get(name)
}