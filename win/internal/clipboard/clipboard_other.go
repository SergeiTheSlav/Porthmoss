//go:build !windows

package clipboard

import "sync"

// Memory is a clipboard that lives only in this process. It backs the agent on
// non-Windows hosts so the sharing logic can be developed and tested from the
// Mac side of the repo.
type Memory struct {
	mu       sync.Mutex
	text     string
	sequence uint32
}

func New() *Memory { return &Memory{} }

func (m *Memory) Text() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.text, nil
}

func (m *Memory) SetText(text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.text = text
	m.sequence++
	return nil
}

func (m *Memory) Sequence() uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sequence
}
