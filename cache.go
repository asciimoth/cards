package main

import (
	"sync"

	"github.com/sirupsen/logrus"
)

type Cache struct {
	capacity int   // max number of entries
	memLimit int64 // max total bytes

	mu      sync.Mutex // protects all fields
	items   map[string]*entry
	ring    []*entry // circular buffer
	hand    int      // clock hand
	memUsed int64    // current memory used
	log     *logrus.Logger
}

type entry struct {
	key   string
	value []byte
	size  int64
	ref   bool // reference bit (for CLOCK)
}

// capacity: maximum number of objects
// memLimit: maximum total memory in bytes
func NewCache(capacity int, memLimit int64, log *logrus.Logger) *Cache {
	if capacity <= 0 {
		panic("capacity must be > 0")
	}
	c := &Cache{
		capacity: capacity,
		memLimit: memLimit,
		items:    make(map[string]*entry, capacity),
		ring:     make([]*entry, capacity),
		hand:     0,
		log:      log,
	}
	return c
}

// Get retrieves a value from the cache.
// Returns value and true if found, or nil and false otherwise.
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		e.ref = true
		c.log.Trace("Cache hit " + key)
		return e.value, true
	}
	c.log.Trace("Cache miss " + key)
	return nil, false
}

// Set adds or updates a key-value pair in the cache.
func (c *Cache) Set(key string, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.log.Trace("Cache set " + key)

	sz := int64(len(value))
	// If updating existing entry
	if e, ok := c.items[key]; ok {
		// adjust memory
		c.memUsed += sz - e.size
		c.log.Tracef("Cache memUsed %d", c.memUsed)
		e.value = value
		e.size = sz
		e.ref = true
		// enforce memory limit
		c.evictByMemory()
		return
	}

	// Create new entry
	e := &entry{key: key, value: value, size: sz, ref: true}

	// Evict until we have room: by count or memory
	for len(c.items) >= c.capacity || c.memUsed+sz > c.memLimit {
		c.evictOne()
	}

	// Find a free slot in ring for initial fill
	if len(c.items) < c.capacity {
		// first len(items) slots are used
		slot := len(c.items)
		c.ring[slot] = e
		e.ref = true
		c.items[key] = e
		c.memUsed += sz
		c.log.Tracef("Cache memUsed %d", c.memUsed)
		return
	}

	// Should not reach here; capacity check above handles full
}

// evictOne evicts a single entry using CLOCK algorithm
func (c *Cache) evictOne() {
	n := c.capacity
	for {
		e := c.ring[c.hand]
		if e == nil {
			// skip empty slots
			c.hand = (c.hand + 1) % n
			continue
		}
		if e.ref {
			e.ref = false
			c.hand = (c.hand + 1) % n
		} else {
			// evict this entry
			c.log.Trace("Cache evict " + e.key)
			delete(c.items, e.key)
			c.memUsed -= e.size
			c.log.Tracef("Cache memUsed %d", c.memUsed)
			// replace with nil; new insert will fill
			c.ring[c.hand] = nil
			c.hand = (c.hand + 1) % n
			return
		}
	}
}

// evictByMemory evicts entries until memUsed <= memLimit
func (c *Cache) evictByMemory() {
	for c.memUsed > c.memLimit && len(c.items) > 0 {
		c.evictOne()
	}
}
