package memory

import (
	"context"
	"sync"

	"github.com/cyberpsych0s1s/quert/internal/storage"
)

type Store struct {
	seen   map[string]struct{}
	hashes map[string]map[string]string // map[type]map[hash]url
	mu     sync.RWMutex
}

func New() *Store {
	return &Store{
		seen:   make(map[string]struct{}),
		hashes: make(map[string]map[string]string),
	}
}

func (s *Store) IsSeen(ctx context.Context, key string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.seen[key]
	return ok, nil
}

func (s *Store) MarkSeen(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen[key] = struct{}{}
	return nil
}

func (s *Store) StoreHash(ctx context.Context, hashType, hash, url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.hashes[hashType]; !ok {
		s.hashes[hashType] = make(map[string]string)
	}
	s.hashes[hashType][hash] = url
	return nil
}

func (s *Store) GetOriginalURL(ctx context.Context, hashType, hash string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if typeMap, ok := s.hashes[hashType]; ok {
		if url, ok := typeMap[hash]; ok {
			return url, nil
		}
	}
	return "", storage.ErrNotFound
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = nil
	s.hashes = nil
	return nil
}
