// SPDX-License-Identifier: AGPL-3.0-only
// Provenance-includes-location: https://github.com/cortexproject/cortex/blob/master/pkg/util/time_test.go
// Provenance-includes-license: Apache-2.0
// Provenance-includes-copyright: The Cortex Authors.

package util

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	v1 "github.com/prometheus/prometheus/web/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grafana/mimir/pkg/util/test"
)

const (
	nanosecondsInMillisecond = int64(time.Millisecond / time.Nanosecond)
)

func TestTimeFromMillis(t *testing.T) {
	var testExpr = []struct {
		input    int64
		expected time.Time
	}{
		{input: 1000, expected: time.Unix(1, 0).UTC()},
		{input: 1500, expected: time.Unix(1, 500*nanosecondsInMillisecond).UTC()},
	}

	for i, c := range testExpr {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			res := TimeFromMillis(c.input)
			require.Equal(t, c.expected, res)
		})
	}
}

func TestTimeRoundTripUsingPrometheusMinAndMaxTimestamps(t *testing.T) {
	refTime, _ := time.Parse(time.Layout, time.Layout)
	var testExpr = []struct {
		input time.Time
	}{
		{input: refTime},
		{input: v1.MinTime},
		{input: v1.MaxTime},
	}

	for i, c := range testExpr {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			ms := TimeToMillis(c.input)
			res := TimeFromMillis(ms)
			require.Equal(t, c.input.UTC().Format(time.RFC3339), res.Format(time.RFC3339))
		})
	}
}

func TestDurationWithJitter(t *testing.T) {
	const numRuns = 1000

	for i := 0; i < numRuns; i++ {
		actual := DurationWithJitter(time.Minute, 0.5)
		assert.GreaterOrEqual(t, int64(actual), int64(30*time.Second))
		assert.LessOrEqual(t, int64(actual), int64(90*time.Second))
	}
}

func TestDurationWithPositiveJitter(t *testing.T) {
	const numRuns = 1000

	for i := 0; i < numRuns; i++ {
		actual := DurationWithPositiveJitter(time.Minute, 0.5)
		assert.GreaterOrEqual(t, int64(actual), int64(60*time.Second))
		assert.LessOrEqual(t, int64(actual), int64(90*time.Second))
	}
}

func TestDurationWithNegativeJitter(t *testing.T) {
	const numRuns = 1000

	for i := 0; i < numRuns; i++ {
		actual := DurationWithNegativeJitter(time.Minute, 0.5)
		assert.GreaterOrEqual(t, int64(actual), int64(30*time.Second))
		assert.LessOrEqual(t, int64(actual), int64(60*time.Second))
	}
}

func TestDurationWithJitterFamily_ZeroVariance(t *testing.T) {
	// Make sure all jitter functions return the input when variance is 0.

	tests := []struct {
		input    time.Duration
		variance float64
	}{
		{time.Duration(0), 0.5},
		{time.Duration(7), 0.1},
		{time.Duration(7), -0.1},
		{10 * time.Minute, -0.1},
		{time.Second, 0.0},
	}

	for _, test := range tests {
		// All jitter functions should return input when variance <= 0
		assert.Equal(t, test.input, DurationWithJitter(test.input, test.variance))
		assert.Equal(t, test.input, DurationWithNegativeJitter(test.input, test.variance))
		assert.Equal(t, test.input, DurationWithPositiveJitter(test.input, test.variance))
	}
}

func TestParseTime(t *testing.T) {
	var tests = []struct {
		input  string
		fail   bool
		result time.Time
	}{
		{
			input: "",
			fail:  true,
		}, {
			input: "abc",
			fail:  true,
		}, {
			input: "30s",
			fail:  true,
		}, {
			input:  "123",
			result: time.Unix(123, 0),
		}, {
			input:  "123.123",
			result: time.Unix(123, 123000000),
		}, {
			input:  "2015-06-03T13:21:58.555Z",
			result: time.Unix(1433337718, 555*time.Millisecond.Nanoseconds()),
		}, {
			input:  "2015-06-03T14:21:58.555+01:00",
			result: time.Unix(1433337718, 555*time.Millisecond.Nanoseconds()),
		}, {
			// Test nanosecond rounding.
			input:  "2015-06-03T13:21:58.56789Z",
			result: time.Unix(1433337718, 567*1e6),
		}, {
			// Test float rounding.
			input:  "1543578564.705",
			result: time.Unix(1543578564, 705*1e6),
		},
	}

	for _, test := range tests {
		ts, err := ParseTime(test.input)
		if test.fail {
			require.Error(t, err)
			continue
		}

		require.NoError(t, err)
		assert.Equal(t, TimeToMillis(test.result), ts)
	}
}

func TestNewDisableableTicker_Enabled(t *testing.T) {
	stop, ch := NewDisableableTicker(10 * time.Millisecond)
	defer stop()

	time.Sleep(100 * time.Millisecond)

	select {
	case <-ch:
		break
	default:
		t.Error("ticker should have ticked when enabled")
	}
}

func TestNewDisableableTicker_Disabled(t *testing.T) {
	stop, ch := NewDisableableTicker(0)
	defer stop()

	time.Sleep(100 * time.Millisecond)

	select {
	case <-ch:
		t.Error("ticker should not have ticked when disabled")
	default:
		break
	}
}

func TestUnixSecondsJSON(t *testing.T) {
	now := time.Now()

	type obj struct {
		Val UnixSeconds `json:"val,omitempty"`
	}

	type testCase struct {
		obj  obj
		json string
	}

	for _, tc := range []testCase{
		{obj: obj{Val: 0}, json: `{}`},
		{obj: obj{Val: UnixSecondsFromTime(now)}, json: fmt.Sprintf(`{"val":%d}`, now.Unix())},
	} {
		t.Run(tc.json, func(t *testing.T) {
			out, err := json.Marshal(tc.obj)
			require.NoError(t, err)
			require.JSONEq(t, tc.json, string(out))

			var newObj obj
			err = json.Unmarshal([]byte(tc.json), &newObj)
			require.NoError(t, err)
			require.Equal(t, tc.obj, newObj)
		})
	}
}

func TestVariableTicker(t *testing.T) {
	test.VerifyNoLeak(t)

	t.Run("should tick at configured durations", func(t *testing.T) {
		t.Parallel()

		startTime := time.Now()
		stop, tickerChan := NewVariableTicker(time.Second, 2*time.Second)
		t.Cleanup(stop)

		// Capture the timing of 3 ticks.
		var ticks []time.Time
		for len(ticks) < 3 {
			ticks = append(ticks, <-tickerChan)
		}

		tolerance := 250 * time.Millisecond
		assert.InDelta(t, ticks[0].Sub(startTime).Seconds(), 1*time.Second.Seconds(), float64(tolerance))
		assert.InDelta(t, ticks[1].Sub(startTime).Seconds(), 3*time.Second.Seconds(), float64(tolerance))
		assert.InDelta(t, ticks[2].Sub(startTime).Seconds(), 5*time.Second.Seconds(), float64(tolerance))
	})

	t.Run("should not close the channel on stop function called", func(t *testing.T) {
		t.Parallel()

		for _, durations := range [][]time.Duration{{time.Second}, {time.Second, 2 * time.Second}} {
			durations := durations

			t.Run(fmt.Sprintf("durations: %v", durations), func(t *testing.T) {
				t.Parallel()

				stop, tickerChan := NewVariableTicker(durations...)
				stop()

				select {
				case <-tickerChan:
					t.Error("should not close the channel and not send any further tick")
				case <-time.After(2 * time.Second):
					// All good.
				}
			})
		}
	})

	t.Run("stop function should be idempotent", func(t *testing.T) {
		t.Parallel()

		for _, durations := range [][]time.Duration{{time.Second}, {time.Second, 2 * time.Second}} {
			durations := durations

			t.Run(fmt.Sprintf("durations: %v", durations), func(t *testing.T) {
				t.Parallel()

				stop, tickerChan := NewVariableTicker(durations...)

				// Call stop() twice.
				stop()
				stop()

				select {
				case <-tickerChan:
					t.Error("should not close the channel and not send any further tick")
				case <-time.After(2 * time.Second):
					// All good.
				}
			})
		}
	})
}
