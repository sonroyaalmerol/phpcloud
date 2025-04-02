// Package hlc implements Hybrid Logical Clocks for causal ordering without coordination
// HLC combines physical clocks with Lamport logical clocks to provide:
// - Causal ordering (happens-before relationships)
// - Wall-clock timestamps for debugging
// - No dependency on synchronized clocks
package hlc

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Timestamp represents an HLC timestamp with physical and logical components
type Timestamp struct {
	Physical int64 `json:"pt"` // Physical time (Unix nanoseconds)
	Logical  int64 `json:"lt"` // Logical time (counter for same-physical-time events)
}

// String returns a string representation of the timestamp
func (t Timestamp) String() string {
	return fmt.Sprintf("PT:%d LT:%d", t.Physical, t.Logical)
}

// IsZero returns true if this is a zero timestamp
func (t Timestamp) IsZero() bool {
	return t.Physical == 0 && t.Logical == 0
}

// Equal returns true if two timestamps are equal
func (t Timestamp) Equal(other Timestamp) bool {
	return t.Physical == other.Physical && t.Logical == other.Logical
}

// Less returns true if t < other (happened before)
func (t Timestamp) Less(other Timestamp) bool {
	if t.Physical != other.Physical {
		return t.Physical < other.Physical
	}
	return t.Logical < other.Logical
}

// LessOrEqual returns true if t <= other
func (t Timestamp) LessOrEqual(other Timestamp) bool {
	return t.Less(other) || t.Equal(other)
}

// ConcurrentWith returns true if t and other are concurrent (neither happened before the other)
func (t Timestamp) ConcurrentWith(other Timestamp) bool {
	return !t.Less(other) && !other.Less(t)
}

// Clock is a Hybrid Logical Clock instance
// Each node has its own clock
type Clock struct {
	mu     sync.RWMutex
	latest Timestamp // Latest timestamp issued by this node
	nodeID string    // Unique node identifier
}

// NewClock creates a new HLC clock for a node
func NewClock(nodeID string) *Clock {
	return &Clock{
		latest: Timestamp{
			Physical: time.Now().UnixNano(),
			Logical:  0,
		},
		nodeID: nodeID,
	}
}

// Now returns the current HLC timestamp
// This is called when generating a new event locally
func (c *Clock) Now() Timestamp {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().UnixNano()

	if now > c.latest.Physical {
		// Physical clock moved forward
		c.latest = Timestamp{
			Physical: now,
			Logical:  0,
		}
	} else {
		// Physical clock hasn't moved (or went backwards)
		// Increment logical clock
		c.latest.Logical++
	}

	return c.latest
}

// Update updates the clock based on a received timestamp
// This is called when receiving a message from another node
// Returns the updated timestamp for the local event
func (c *Clock) Update(received Timestamp) Timestamp {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().UnixNano()

	// Find the maximum of all three: local physical, received physical, received logical
	maxPhysical := now
	if received.Physical > maxPhysical {
		maxPhysical = received.Physical
	}
	if c.latest.Physical > maxPhysical {
		maxPhysical = c.latest.Physical
	}

	// Calculate new logical clock
	var newLogical int64
	if maxPhysical == now && maxPhysical == c.latest.Physical && maxPhysical == received.Physical {
		// All three physical times are equal - take max of logical clocks + 1
		newLogical = c.latest.Logical
		if received.Logical > newLogical {
			newLogical = received.Logical
		}
		newLogical++
	} else if maxPhysical == c.latest.Physical && maxPhysical == now {
		// Local and current physical time are max - increment local logical
		newLogical = c.latest.Logical + 1
	} else if maxPhysical == received.Physical {
		// Received physical time is max - start from received logical + 1
		newLogical = received.Logical + 1
	} else {
		// Current physical time is strictly greater
		newLogical = 0
	}

	c.latest = Timestamp{
		Physical: maxPhysical,
		Logical:  newLogical,
	}

	return c.latest
}

// Witness witnesses a timestamp without generating a new one
// Used when receiving updates that don't cause local events
func (c *Clock) Witness(received Timestamp) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().UnixNano()

	// Update to max of received and local
	if received.Physical > c.latest.Physical ||
		(received.Physical == c.latest.Physical && received.Logical > c.latest.Logical) {
		c.latest = received
	}

	// Also ensure physical clock doesn't drift backwards
	if now > c.latest.Physical {
		c.latest.Physical = now
		c.latest.Logical = 0
	}
}

// GetLatest returns the latest timestamp seen by this clock
func (c *Clock) GetLatest() Timestamp {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.latest
}

// MarshalJSON implements json.Marshaler
func (t Timestamp) MarshalJSON() ([]byte, error) {
	type alias Timestamp
	return json.Marshal(alias(t))
}

// UnmarshalJSON implements json.Unmarshaler
func (t *Timestamp) UnmarshalJSON(data []byte) error {
	type alias Timestamp
	return json.Unmarshal(data, (*alias)(t))
}

// Bytes returns a byte representation for storage
func (t Timestamp) Bytes() []byte {
	return []byte(fmt.Sprintf("%d:%d", t.Physical, t.Logical))
}

// FromBytes parses a timestamp from bytes
func FromBytes(data []byte) (Timestamp, error) {
	t := Timestamp{}
	_, err := fmt.Sscanf(string(data), "%d:%d", &t.Physical, &t.Logical)
	return t, err
}

// Max returns the maximum of two timestamps
func Max(a, b Timestamp) Timestamp {
	if a.Less(b) {
		return b
	}
	return a
}

// Min returns the minimum of two timestamps
func Min(a, b Timestamp) Timestamp {
	if a.Less(b) {
		return a
	}
	return b
}

// Context manages HLC clocks for multi-node scenarios
type Context struct {
	mu     sync.RWMutex
	clocks map[string]*Clock // nodeID -> Clock
}

// NewContext creates a new HLC context
func NewContext() *Context {
	return &Context{
		clocks: make(map[string]*Clock),
	}
}

// GetClock gets or creates a clock for a node
func (ctx *Context) GetClock(nodeID string) *Clock {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	if clock, ok := ctx.clocks[nodeID]; ok {
		return clock
	}

	clock := NewClock(nodeID)
	ctx.clocks[nodeID] = clock
	return clock
}

// NowFor returns Now() for a specific node
func (ctx *Context) NowFor(nodeID string) Timestamp {
	return ctx.GetClock(nodeID).Now()
}

// UpdateFor calls Update() for a specific node
func (ctx *Context) UpdateFor(nodeID string, received Timestamp) Timestamp {
	return ctx.GetClock(nodeID).Update(received)
}
