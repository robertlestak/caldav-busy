package cache

import (
	"sync"
	"time"
)

type CacheEntry struct {
	data       string
	lastUpdate time.Time
}

type Cache struct {
	mu              sync.RWMutex
	entries         map[string]*CacheEntry
	refreshInterval time.Duration
}

func NewCache(refreshInterval time.Duration) *Cache {
	return &Cache{
		entries:         make(map[string]*CacheEntry),
		refreshInterval: refreshInterval,
	}
}

func (c *Cache) Get() (string, bool) {
	return c.GetWithKey("default")
}

func (c *Cache) GetWithKey(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	entry, exists := c.entries[key]
	if !exists || entry.data == "" {
		return "", false
	}
	
	if time.Since(entry.lastUpdate) > c.refreshInterval {
		return entry.data, false
	}
	
	return entry.data, true
}

func (c *Cache) Set(data string) {
	c.SetWithKey("default", data)
}

func (c *Cache) SetWithKey(key string, data string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	c.entries[key] = &CacheEntry{
		data:       data,
		lastUpdate: time.Now(),
	}
}

func (c *Cache) IsExpired() bool {
	return c.IsExpiredWithKey("default")
}

func (c *Cache) IsExpiredWithKey(key string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	entry, exists := c.entries[key]
	if !exists {
		return true
	}
	
	return time.Since(entry.lastUpdate) > c.refreshInterval
}

func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	c.entries = make(map[string]*CacheEntry)
}

func (c *Cache) ClearKey(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	delete(c.entries, key)
}