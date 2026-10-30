// Package store implements a thread-safe in-memory storage for items.
package store

import (
	"errors"
	"sync"
)

// ErrNotFound is returned when an item with the requested id does not exist.
var ErrNotFound = errors.New("item not found")

// Item is a stored record.
type Item struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Store keeps items in a map guarded by a RWMutex.
type Store struct {
	mu     sync.RWMutex
	items  map[int64]Item
	nextID int64
}

// New creates an empty store.
func New() *Store {
	return &Store{items: make(map[int64]Item)}
}

// Create stores a new item, assigns it an id and returns the stored copy.
func (s *Store) Create(name, value string) Item {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	item := Item{ID: s.nextID, Name: name, Value: value}
	s.items[item.ID] = item
	return item
}

// Get returns the item with the given id or ErrNotFound.
func (s *Store) Get(id int64) (Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	item, ok := s.items[id]
	if !ok {
		return Item{}, ErrNotFound
	}
	return item, nil
}

// Update replaces the fields of an existing item or returns ErrNotFound.
func (s *Store) Update(id int64, name, value string) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.items[id]; !ok {
		return Item{}, ErrNotFound
	}
	item := Item{ID: id, Name: name, Value: value}
	s.items[id] = item
	return item, nil
}
