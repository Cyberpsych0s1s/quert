package storage

import (
	"context"
	"errors"
)

var (
	ErrNotFound = errors.New("item not found")
)

// DeduplicationStore defines the behavior for URL and content deduplication
type DeduplicationStore interface {
	IsSeen(ctx context.Context, key string) (bool, error)

	MarkSeen(ctx context.Context, key string) error

	StoreHash(ctx context.Context, hashType string, hash string, url string) error

	GetOriginalURL(ctx context.Context, hashType string, hash string) (string, error)

	Close() error
}
