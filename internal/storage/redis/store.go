package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cyberpsych0s1s/quert/internal/storage"
	"github.com/redis/go-redis/v9"
)

type Store struct {
	client *redis.Client
	prefix string
	ttl    time.Duration
}

type Config struct {
	Addr     string
	Password string
	DB       int
	Prefix   string
	TTL      time.Duration
}

func New(cfg Config) (*Store, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	if cfg.Prefix == "" {
		cfg.Prefix = "quert:"
	}

	return &Store{
		client: client,
		prefix: cfg.Prefix,
		ttl:    cfg.TTL,
	}, nil
}

func (s *Store) seenKey(key string) string {
	return s.prefix + "seen:" + key
}

func (s *Store) hashKey(hashType, hash string) string {
	return s.prefix + "hash:" + hashType + ":" + hash
}

func (s *Store) IsSeen(ctx context.Context, key string) (bool, error) {
	n, err := s.client.Exists(ctx, s.seenKey(key)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) MarkSeen(ctx context.Context, key string) error {

	if s.ttl > 0 {
		return s.client.Set(ctx, s.seenKey(key), "1", s.ttl).Err()
	}
	return s.client.Set(ctx, s.seenKey(key), "1", 0).Err()
}

func (s *Store) StoreHash(ctx context.Context, hashType, hash, url string) error {
	key := s.hashKey(hashType, hash)
	if s.ttl > 0 {
		return s.client.Set(ctx, key, url, s.ttl).Err()
	}
	return s.client.Set(ctx, key, url, 0).Err()
}

func (s *Store) GetOriginalURL(ctx context.Context, hashType, hash string) (string, error) {
	val, err := s.client.Get(ctx, s.hashKey(hashType, hash)).Result()
	if errors.Is(err, redis.Nil) {
		return "", storage.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return val, nil
}

func (s *Store) Close() error {
	return s.client.Close()
}
