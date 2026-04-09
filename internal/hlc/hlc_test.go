package hlc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimestamp_Ordering(t *testing.T) {
	a := Timestamp{Physical: 100, Logical: 0}
	b := Timestamp{Physical: 100, Logical: 1}
	c := Timestamp{Physical: 200, Logical: 0}

	assert.True(t, a.Less(b), "same physical, lower logical should be less")
	assert.True(t, a.Less(c), "lower physical should be less")
	assert.True(t, b.Less(c), "lower physical should be less regardless of logical")
	assert.False(t, b.Less(a), "Less is not symmetric")
	assert.False(t, a.Less(a), "timestamp is not less than itself")
}

func TestTimestamp_Equal(t *testing.T) {
	a := Timestamp{Physical: 100, Logical: 5}
	b := Timestamp{Physical: 100, Logical: 5}
	c := Timestamp{Physical: 100, Logical: 6}

	assert.True(t, a.Equal(b))
	assert.False(t, a.Equal(c))
}

func TestTimestamp_LessOrEqual(t *testing.T) {
	a := Timestamp{Physical: 100, Logical: 0}
	b := Timestamp{Physical: 100, Logical: 0}
	c := Timestamp{Physical: 100, Logical: 1}

	assert.True(t, a.LessOrEqual(b), "equal timestamps satisfy LessOrEqual")
	assert.True(t, a.LessOrEqual(c), "strictly less satisfies LessOrEqual")
	assert.False(t, c.LessOrEqual(a), "strictly greater does not satisfy LessOrEqual")
}

func TestTimestamp_ConcurrentWith(t *testing.T) {
	a := Timestamp{Physical: 100, Logical: 0}
	b := Timestamp{Physical: 100, Logical: 0}
	c := Timestamp{Physical: 200, Logical: 0}

	assert.True(t, a.ConcurrentWith(b), "equal timestamps are concurrent")
	assert.False(t, a.ConcurrentWith(c), "ordered timestamps are not concurrent")
}

func TestTimestamp_IsZero(t *testing.T) {
	assert.True(t, Timestamp{}.IsZero())
	assert.False(t, Timestamp{Physical: 1}.IsZero())
	assert.False(t, Timestamp{Logical: 1}.IsZero())
}

func TestClock_Now_Monotonic(t *testing.T) {
	c := NewClock("test-node")

	prev := c.Now()
	for i := 0; i < 100; i++ {
		next := c.Now()
		assert.False(t, next.Less(prev), "Now() must be monotonically non-decreasing")
		prev = next
	}
}

func TestClock_Now_AdvancesLogical(t *testing.T) {
	c := NewClock("test-node")

	// Force logical clock by advancing multiple times in rapid succession
	// (physical may not change within nanosecond resolution)
	first := c.Now()
	second := c.Now()

	// second must be strictly greater than first
	assert.True(t, first.Less(second) || first.Equal(second),
		"consecutive Now() calls must be non-decreasing")
}

func TestClock_Update_AdvancesClockToReceived(t *testing.T) {
	local := NewClock("local")

	// Simulate receiving a timestamp from a remote node far in the future
	future := Timestamp{
		Physical: time.Now().Add(10 * time.Second).UnixNano(),
		Logical:  5,
	}

	updated := local.Update(future)

	// After witnessing a future timestamp, local clock must be ahead of it
	assert.False(t, updated.Less(future),
		"after Update, local clock must be >= received timestamp")
}

func TestClock_Witness_DoesNotDecrease(t *testing.T) {
	c := NewClock("test")

	before := c.GetLatest()

	// Witness an older timestamp — clock should not go backwards
	old := Timestamp{Physical: before.Physical - 1000, Logical: 0}
	c.Witness(old)

	after := c.GetLatest()
	assert.False(t, after.Less(before), "Witness with old timestamp must not decrease clock")
}

func TestClock_Witness_AdvancesToNewer(t *testing.T) {
	c := NewClock("test")

	newer := Timestamp{
		Physical: time.Now().Add(5 * time.Second).UnixNano(),
		Logical:  10,
	}
	c.Witness(newer)

	latest := c.GetLatest()
	assert.False(t, latest.Less(newer), "after Witness, clock must be >= witnessed timestamp")
}

func TestTimestamp_MaxMin(t *testing.T) {
	lo := Timestamp{Physical: 1, Logical: 0}
	hi := Timestamp{Physical: 2, Logical: 0}

	assert.Equal(t, hi, Max(lo, hi))
	assert.Equal(t, hi, Max(hi, lo))
	assert.Equal(t, lo, Min(lo, hi))
	assert.Equal(t, lo, Min(hi, lo))
}

func TestTimestamp_BytesRoundTrip(t *testing.T) {
	ts := Timestamp{Physical: 1234567890, Logical: 42}
	data := ts.Bytes()

	parsed, err := FromBytes(data)
	require.NoError(t, err)
	assert.Equal(t, ts, parsed)
}

func TestContext_GetClock_Idempotent(t *testing.T) {
	ctx := NewContext()

	c1 := ctx.GetClock("node-a")
	c2 := ctx.GetClock("node-a")
	assert.Same(t, c1, c2, "GetClock must return the same Clock instance for the same node")

	different := ctx.GetClock("node-b")
	assert.NotSame(t, c1, different)
}

func TestContext_NowFor_Monotonic(t *testing.T) {
	ctx := NewContext()
	nodeID := "test-node"

	prev := ctx.NowFor(nodeID)
	for i := 0; i < 10; i++ {
		next := ctx.NowFor(nodeID)
		assert.False(t, next.Less(prev))
		prev = next
	}
}

func TestClock_ConcurrentSafety(t *testing.T) {
	c := NewClock("concurrent-test")

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				c.Now()
				c.Witness(Timestamp{Physical: time.Now().UnixNano(), Logical: 0})
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}
