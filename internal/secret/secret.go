// Package secret keeps the TorBox API key out of config files. The key lives
// in libsecret (org.freedesktop.secrets); TORBOX_API_KEY overrides it.
package secret

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

const EnvKey = "TORBOX_API_KEY"

var (
	ErrNotFound    = errors.New("api key not found")
	ErrUnavailable = errors.New("secret service unavailable")
	ErrInvalid     = errors.New("api key must be a single line")
)

const redacted = "********"

// Key is an API key that cannot be printed or marshaled by accident.
type Key struct{ v string }

func NewKey(s string) (Key, error) {
	s = strings.TrimSpace(s)
	if strings.ContainsAny(s, "\r\n \t") {
		return Key{}, ErrInvalid
	}
	return Key{v: s}, nil
}

func (k Key) Reveal() string               { return k.v }
func (k Key) Empty() bool                  { return k.v == "" }
func (k Key) String() string               { return redacted }
func (k Key) GoString() string             { return "secret.Key{}" }
func (k Key) Format(f fmt.State, _ rune)   { _, _ = io.WriteString(f, redacted) }
func (k Key) MarshalText() ([]byte, error) { return nil, errors.New("api key must not be marshaled") }

// Store is the keyring API.
type Store interface {
	Lookup() (Key, error)
	Save(Key) error
	Delete() error
}

// Source reports where Resolve found a key.
type Source string

const (
	SourceEnv     Source = "env"
	SourceKeyring Source = "keyring"
)

// Resolve prefers the environment, then the keyring.
func Resolve(getenv func(string) string, st Store) (Key, Source, error) {
	if v := getenv(EnvKey); strings.TrimSpace(v) != "" {
		k, err := NewKey(v)
		return k, SourceEnv, err
	}
	if st == nil {
		return Key{}, "", ErrNotFound
	}
	k, err := st.Lookup()
	return k, SourceKeyring, err
}

// Memory is an in-process store for tests.
type Memory struct {
	mu  sync.Mutex
	key *Key
}

func NewMemory() *Memory { return &Memory{} }

func (m *Memory) Lookup() (Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.key == nil {
		return Key{}, ErrNotFound
	}
	return *m.key, nil
}

func (m *Memory) Save(k Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.key = &k
	return nil
}

func (m *Memory) Delete() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.key == nil {
		return ErrNotFound
	}
	m.key = nil
	return nil
}
