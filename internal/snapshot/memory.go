package snapshot

import (
	"sort"
	"sync"

	"github.com/itsHabib/saas-reaper/internal/flags"
)

// Memory is a concurrency-safe in-process flag projection.
type Memory struct {
	mu    sync.RWMutex
	flags map[string]map[string]flags.Flag
}

// NewMemory returns an empty projection.
func NewMemory() *Memory {
	return &Memory{flags: make(map[string]map[string]flags.Flag)}
}

// Replace atomically rebuilds every environment from durable definitions.
func (m *Memory) Replace(loaded map[string][]flags.Flag) {
	next := make(map[string]map[string]flags.Flag, len(loaded))
	for environment, environmentFlags := range loaded {
		next[environment] = make(map[string]flags.Flag, len(environmentFlags))
		for _, flag := range environmentFlags {
			next[environment][flag.Key] = flag.Copy()
		}
	}
	m.mu.Lock()
	m.flags = next
	m.mu.Unlock()
}

// Put installs one committed definition.
func (m *Memory) Put(environment string, flag flags.Flag) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.flags[environment]
	if current == nil {
		current = make(map[string]flags.Flag)
		m.flags[environment] = current
	}
	current[flag.Key] = flag.Copy()
}

// Get returns an independent copy of one current definition.
func (m *Memory) Get(environment, key string) (flags.Flag, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	flag, ok := m.flags[environment][key]
	return flag.Copy(), ok
}

// List returns independent definitions ordered by key.
func (m *Memory) List(environment string) []flags.Flag {
	m.mu.RLock()
	defer m.mu.RUnlock()
	current := m.flags[environment]
	listed := make([]flags.Flag, 0, len(current))
	for _, flag := range current {
		listed = append(listed, flag.Copy())
	}
	sort.Slice(listed, func(i, j int) bool {
		return listed[i].Key < listed[j].Key
	})
	return listed
}
