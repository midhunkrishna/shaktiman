package main

import (
	"fmt"
	"sync"
)

// User represents a stored user.
type User struct {
	ID    string
	Name  string
	Email string
}

// Store provides in-memory user storage.
type Store struct {
	mu    sync.RWMutex
	users map[string]*User
}

// NewStore creates an empty store.
func NewStore() *Store {
	return &Store{
		users: make(map[string]*User),
	}
}

// AddUser adds a user to the store.
func (s *Store) AddUser(user *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.users[user.ID]; exists {
		return fmt.Errorf("user %s already exists", user.ID)
	}
	s.users[user.ID] = user
	return nil
}

// GetUser retrieves a user by ID.
func (s *Store) GetUser(id string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.users[id]
	if !ok {
		return nil, fmt.Errorf("user %s not found", id)
	}
	return user, nil
}

// DeleteUser removes a user from the store.
func (s *Store) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.users[id]; !exists {
		return fmt.Errorf("user %s not found", id)
	}
	delete(s.users, id)
	return nil
}
