// SPDX-License-Identifier: AGPL-3.0-only

package operators

import (
	"context"
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/grafana/mimir/pkg/streamingpromql/types"
	"github.com/grafana/mimir/pkg/util/limiter"
)

func TestInstantVectorOperatorBuffer_BufferingSubsetOfInputSeries(t *testing.T) {
	series0Data := types.InstantVectorSeriesData{Floats: []promql.FPoint{{T: 0, F: 0}}}
	series2Data := types.InstantVectorSeriesData{Floats: []promql.FPoint{{T: 0, F: 2}}}
	series3Data := types.InstantVectorSeriesData{Floats: []promql.FPoint{{T: 0, F: 3}}}
	series4Data := types.InstantVectorSeriesData{Floats: []promql.FPoint{{T: 0, F: 4}}}
	series5Data := types.InstantVectorSeriesData{Floats: []promql.FPoint{{T: 0, F: 5}}}
	series6Data := types.InstantVectorSeriesData{Floats: []promql.FPoint{{T: 0, F: 6}}}

	inner := &TestOperator{
		Series: []labels.Labels{
			labels.FromStrings("series", "0"),
			labels.FromStrings("series", "1"),
			labels.FromStrings("series", "2"),
			labels.FromStrings("series", "3"),
			labels.FromStrings("series", "4"),
			labels.FromStrings("series", "5"),
			labels.FromStrings("series", "6"),
		},
		Data: []types.InstantVectorSeriesData{
			series0Data,
			{Floats: []promql.FPoint{{T: 0, F: 1}}},
			series2Data,
			series3Data,
			series4Data,
			series5Data,
			series6Data,
		},
	}

	seriesUsed := []bool{true, false, true, true, true}
	ctx := context.Background()
	memoryConsumptionTracker := limiter.NewMemoryConsumptionTracker(ctx, 0, nil, "")
	require.NoError(t, memoryConsumptionTracker.IncreaseMemoryConsumption(types.FPointSize*6, limiter.FPointSlices)) // We have 6 FPoints from the inner series.
	buffer := NewInstantVectorOperatorBuffer(inner, seriesUsed, 4, memoryConsumptionTracker)

	// Read first series.
	series, err := buffer.GetSeries(ctx, []int{0})
	require.NoError(t, err)
	require.Equal(t, []types.InstantVectorSeriesData{series0Data}, series)
	require.Empty(t, buffer.buffer) // Should not buffer series that was immediately returned.
	require.False(t, inner.Closed)

	// Read next desired series, skipping over series that won't be used.
	series, err = buffer.GetSeries(ctx, []int{2})
	require.NoError(t, err)
	require.Equal(t, []types.InstantVectorSeriesData{series2Data}, series)
	require.Empty(t, buffer.buffer) // Should not buffer series at index 1 that won't be used.
	require.False(t, inner.Closed)

	// Read another desired series, skipping over a series that will be used later.
	series, err = buffer.GetSeries(ctx, []int{4})
	require.NoError(t, err)
	require.Equal(t, []types.InstantVectorSeriesData{series4Data}, series)
	require.Len(t, buffer.buffer, 1) // Should only have buffered a single series (index 3).
	require.True(t, inner.Closed, "inner operator should be closed after reading last series that will be used")

	// Read the series we just read past from the buffer.
	series, err = buffer.GetSeries(ctx, []int{3})
	require.NoError(t, err)
	require.Equal(t, []types.InstantVectorSeriesData{series3Data}, series)
	require.Empty(t, buffer.buffer) // Series that has been returned should be removed from buffer once it's returned.
	require.True(t, inner.Closed)

	// Read multiple series.
	series, err = buffer.GetSeries(ctx, []int{5, 6})
	require.NoError(t, err)
	require.Equal(t, []types.InstantVectorSeriesData{series5Data, series6Data}, series)
	require.True(t, inner.Closed)
}

func TestInstantVectorOperatorBuffer_BufferingAllInputSeries(t *testing.T) {
	series0Data := types.InstantVectorSeriesData{Floats: []promql.FPoint{{T: 0, F: 0}}}
	series1Data := types.InstantVectorSeriesData{Floats: []promql.FPoint{{T: 0, F: 1}}}
	series2Data := types.InstantVectorSeriesData{Floats: []promql.FPoint{{T: 0, F: 2}}}
	series3Data := types.InstantVectorSeriesData{Floats: []promql.FPoint{{T: 0, F: 3}}}
	series4Data := types.InstantVectorSeriesData{Floats: []promql.FPoint{{T: 0, F: 4}}}
	series5Data := types.InstantVectorSeriesData{Floats: []promql.FPoint{{T: 0, F: 5}}}
	series6Data := types.InstantVectorSeriesData{Floats: []promql.FPoint{{T: 0, F: 6}}}

	inner := &TestOperator{
		Series: []labels.Labels{
			labels.FromStrings("series", "0"),
			labels.FromStrings("series", "1"),
			labels.FromStrings("series", "2"),
			labels.FromStrings("series", "3"),
			labels.FromStrings("series", "4"),
			labels.FromStrings("series", "5"),
			labels.FromStrings("series", "6"),
		},
		Data: []types.InstantVectorSeriesData{
			series0Data,
			series1Data,
			series2Data,
			series3Data,
			series4Data,
			series5Data,
			series6Data,
		},
	}

	ctx := context.Background()
	memoryConsumptionTracker := limiter.NewMemoryConsumptionTracker(ctx, 0, nil, "")
	require.NoError(t, memoryConsumptionTracker.IncreaseMemoryConsumption(types.FPointSize*6, limiter.FPointSlices)) // We have 6 FPoints from the inner series.
	buffer := NewInstantVectorOperatorBuffer(inner, nil, 6, memoryConsumptionTracker)

	// Read first series.
	series, err := buffer.GetSeries(ctx, []int{0})
	require.NoError(t, err)
	require.Equal(t, []types.InstantVectorSeriesData{series0Data}, series)
	require.Empty(t, buffer.buffer) // Should not buffer series that was immediately returned.
	require.False(t, inner.Closed)

	// Read next desired series, skipping over a series that won't be read right now.
	series, err = buffer.GetSeries(ctx, []int{2})
	require.NoError(t, err)
	require.Equal(t, []types.InstantVectorSeriesData{series2Data}, series)
	require.Len(t, buffer.buffer, 1) // Should only have buffered a single series (index 1).
	require.False(t, inner.Closed)

	// Read another desired series, skipping over another series that will be read later.
	series, err = buffer.GetSeries(ctx, []int{4})
	require.NoError(t, err)
	require.Equal(t, []types.InstantVectorSeriesData{series4Data}, series)
	require.Len(t, buffer.buffer, 2) // Should only have buffered two series (indices 1 and 3).
	require.False(t, inner.Closed)

	// Read the series we just read past from the buffer.
	series, err = buffer.GetSeries(ctx, []int{3})
	require.NoError(t, err)
	require.Equal(t, []types.InstantVectorSeriesData{series3Data}, series)
	require.Len(t, buffer.buffer, 1) // Series that has been returned should be removed from buffer once it's returned.
	require.False(t, inner.Closed)

	// Read the series we buffered earlier.
	series, err = buffer.GetSeries(ctx, []int{1})
	require.NoError(t, err)
	require.Equal(t, []types.InstantVectorSeriesData{series1Data}, series)
	require.Empty(t, buffer.buffer)
	require.False(t, inner.Closed)

	// Read multiple series.
	series, err = buffer.GetSeries(ctx, []int{5, 6})
	require.NoError(t, err)
	require.Equal(t, []types.InstantVectorSeriesData{series5Data, series6Data}, series)
	require.True(t, inner.Closed)
}

func TestInstantVectorOperatorBuffer_ReleasesBufferWhenClosedEarly(t *testing.T) {
	ctx := context.Background()
	memoryConsumptionTracker := limiter.NewMemoryConsumptionTracker(ctx, 0, nil, "")

	createTestData := func(val float64) types.InstantVectorSeriesData {
		floats, err := types.FPointSlicePool.Get(1, memoryConsumptionTracker)
		require.NoError(t, err)
		floats = append(floats, promql.FPoint{T: 0, F: val})
		return types.InstantVectorSeriesData{Floats: floats}
	}

	series0Data := createTestData(0)
	series1Data := createTestData(1)

	inner := &TestOperator{
		Series: []labels.Labels{
			labels.FromStrings("series", "0"),
			labels.FromStrings("series", "1"),
		},
		Data: []types.InstantVectorSeriesData{
			series0Data,
			series1Data,
		},
	}

	buffer := NewInstantVectorOperatorBuffer(inner, nil, 1, memoryConsumptionTracker)

	// Read the second series, causing the first to be buffered.
	series, err := buffer.GetSeries(ctx, []int{1})
	require.NoError(t, err)
	require.Equal(t, []types.InstantVectorSeriesData{series1Data}, series)
	types.PutInstantVectorSeriesData(series[0], memoryConsumptionTracker)
	require.Len(t, buffer.buffer, 1, "should have buffered first series")

	// Close the buffer, which should release the buffered series.
	buffer.Close()
	require.Equal(t, uint64(0), memoryConsumptionTracker.CurrentEstimatedMemoryConsumptionBytes())
}
