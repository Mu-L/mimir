// SPDX-License-Identifier: AGPL-3.0-only
// Provenance-includes-location: https://github.com/cortexproject/cortex/blob/master/pkg/querier/remote_read.go
// Provenance-includes-license: Apache-2.0
// Provenance-includes-copyright: The Cortex Authors.

package querier

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/gogo/protobuf/proto"
	"github.com/grafana/dskit/concurrency"
	"github.com/pkg/errors"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	prom_remote "github.com/prometheus/prometheus/storage/remote"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/prometheus/prometheus/tsdb/chunks"

	"github.com/grafana/mimir/pkg/querier/api"
	"github.com/grafana/mimir/pkg/util"
	util_log "github.com/grafana/mimir/pkg/util/log"
	"github.com/grafana/mimir/pkg/util/validation"
)

const (
	// Queries are a set of matchers with time ranges - should not get into megabytes
	MaxRemoteReadQuerySize = 1024 * 1024

	// Maximum number of bytes in frame when using streaming remote read.
	// Google's recommendation is to keep protobuf message not larger than 1MB.
	// https://developers.google.com/protocol-buffers/docs/techniques#large-data
	maxRemoteReadFrameBytes = 1024 * 1024

	statusClientClosedRequest = 499
)

// RemoteReadHandler handles Prometheus remote read requests.
func RemoteReadHandler(q storage.SampleAndChunkQueryable, logger log.Logger, cfg Config) http.Handler {
	return remoteReadHandler(q, maxRemoteReadFrameBytes, cfg.MaxConcurrentRemoteReadQueries, logger)
}

func remoteReadHandler(q storage.SampleAndChunkQueryable, maxBytesInFrame int, maxConcurrency int, lg log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var req prompb.ReadRequest
		logger := util_log.WithContext(r.Context(), lg)
		if _, err := util.ParseProtoReader(ctx, r.Body, int(r.ContentLength), MaxRemoteReadQuerySize, nil, &req, util.RawSnappy); err != nil {
			level.Error(logger).Log("msg", "failed to parse proto", "err", err.Error())
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		respType, err := negotiateResponseType(req.AcceptedResponseTypes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		switch respType {
		case prompb.ReadRequest_STREAMED_XOR_CHUNKS:
			remoteReadStreamedXORChunks(ctx, q, w, &req, maxBytesInFrame, maxConcurrency, logger)
		default:
			remoteReadSamples(ctx, q, w, &req, maxConcurrency, logger)
		}
	})
}

func remoteReadSamples(
	ctx context.Context,
	q storage.Queryable,
	w http.ResponseWriter,
	req *prompb.ReadRequest,
	maxConcurrency int,
	logger log.Logger,
) {
	resp := prompb.ReadResponse{
		Results: make([]*prompb.QueryResult, len(req.Queries)),
	}

	closers := make([]io.Closer, len(req.Queries))
	defer func() {
		for _, closer := range closers {
			if closer != nil {
				_ = closer.Close()
			}
		}
	}()

	run := func(_ context.Context, idx int) error {
		start, end, minT, maxT, matchers, hints, err := queryFromRemoteReadQuery(req.Queries[idx])
		if err != nil {
			return err
		}

		querier, err := q.Querier(int64(start), int64(end))
		if err != nil {
			return err
		}
		closers[idx] = querier

		seriesSet := querier.Select(ctx, false, hints, matchers...)

		// We can over-read when querying, but we don't need to return samples
		// outside the queried range, so can filter them out.
		resp.Results[idx], err = seriesSetToQueryResult(seriesSet, int64(minT), int64(maxT))
		return err
	}

	err := concurrency.ForEachJob(ctx, len(req.Queries), maxConcurrency, run)
	if err != nil {
		code := remoteReadErrorStatusCode(err)
		if code/100 != 4 {
			level.Error(logger).Log("msg", "error while processing remote read request", "err", err)
		}
		http.Error(w, err.Error(), code) // change the Content-Type to text/plain and return a human-readable error message
		return
	}
	w.Header().Add("Content-Type", "application/x-protobuf")
	w.Header().Set("Content-Encoding", "snappy")

	if err := util.SerializeProtoResponse(w, &resp, util.RawSnappy); err != nil {
		level.Error(logger).Log("msg", "error sending remote read response", "err", err)
	}
}

func remoteReadStreamedXORChunks(
	ctx context.Context,
	q storage.ChunkQueryable,
	w http.ResponseWriter,
	req *prompb.ReadRequest,
	maxBytesInFrame int,
	maxConcurrency int,
	logger log.Logger,
) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "internal http.ResponseWriter does not implement http.Flusher interface", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", api.ContentTypeRemoteReadStreamedChunks)

	// Process all queries concurrently and collect their ChunkSeriesSet results
	type queryResult struct {
		series  storage.ChunkSeriesSet
		querier io.Closer
	}

	results := make([]queryResult, len(req.Queries))

	// Ensure all queriers are closed when the function exits
	defer func() {
		for _, result := range results {
			if result.querier != nil {
				result.querier.Close()
			}
		}
	}()

	run := func(_ context.Context, idx int) error {
		qr := req.Queries[idx]
		start, end, _, _, matchers, hints, err := queryFromRemoteReadQuery(qr)
		if err != nil {
			return err
		}

		querier, err := q.ChunkQuerier(int64(start), int64(end))
		if err != nil {
			return err
		}

		// The streaming API has to provide the series sorted.
		// Use the original ctx instead of jobCtx because ForEachJob cancels jobCtx before returning,
		// but we need the SeriesSet to remain valid for streaming later.
		seriesSet := querier.Select(ctx, true, hints, matchers...)
		results[idx] = queryResult{series: seriesSet, querier: querier}
		return nil
	}

	err := concurrency.ForEachJob(ctx, len(req.Queries), maxConcurrency, run)
	// If any query failed, return the error
	if err != nil {
		code := remoteReadErrorStatusCode(err)
		if code/100 != 4 {
			level.Error(logger).Log("msg", "error while processing streaming remote read request", "err", err)
		}
		http.Error(w, err.Error(), code)
		return
	}

	// We can't send the header after we've streamed the responses.
	// We don't set the header because the http stdlib will automatically set it to 200 on the first Write().
	// In case of an error, we will break the stream below.

	for i, result := range results {
		if err := streamChunkedReadResponses(
			prom_remote.NewChunkedWriter(w, f),
			result.series,
			i,
			maxBytesInFrame,
		); err != nil {
			code := remoteReadErrorStatusCode(err)
			if code/100 != 4 {
				level.Error(logger).Log("msg", "error while streaming remote read response", "err", err)
			}

			// Unless we encountered the error before we called Write(), most of this http.Error() call won't have an effect.
			// But we do it on a best-effort basis, since the remote read protocol doesn't have native error handling.
			http.Error(w, err.Error(), code)
			return
		}
	}
}

func remoteReadErrorStatusCode(err error) int {
	switch {
	case errors.Is(err, context.Canceled):
		// The premise is that the client closed the connection and will not read the response.
		// We want to differentiate between the client aborting the request and the server aborting the request.
		return statusClientClosedRequest
	case validation.IsLimitError(err):
		return http.StatusBadRequest
	case errors.As(err, &promql.ErrStorage{}):
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

func seriesSetToQueryResult(s storage.SeriesSet, filterStartMs, filterEndMs int64) (*prompb.QueryResult, error) {
	result := &prompb.QueryResult{}

	var it chunkenc.Iterator
	for s.Next() {
		series := s.At()
		samples := []prompb.Sample{}
		histograms := []prompb.Histogram{}
		it = series.Iterator(it)
		for valType := it.Next(); valType != chunkenc.ValNone; valType = it.Next() {
			// Ensure the sample is within the filtered time range.
			if ts := it.AtT(); ts < filterStartMs || ts > filterEndMs {
				continue
			}

			switch valType {
			case chunkenc.ValFloat:
				t, v := it.At()
				samples = append(samples, prompb.Sample{
					Timestamp: t,
					Value:     v,
				})
			case chunkenc.ValHistogram:
				t, h := it.AtHistogram(nil) // Nil argument as we pass the data to the protobuf as-is without copy.
				histograms = append(histograms, prompb.FromIntHistogram(t, h))
			case chunkenc.ValFloatHistogram:
				t, h := it.AtFloatHistogram(nil) // Nil argument as we pass the data to the protobuf as-is without copy.
				histograms = append(histograms, prompb.FromFloatHistogram(t, h))
			default:
				return nil, fmt.Errorf("unsupported value type: %v", valType)
			}
		}

		if err := it.Err(); err != nil {
			return nil, err
		}

		ts := &prompb.TimeSeries{
			Labels:     prompb.FromLabels(series.Labels(), nil),
			Samples:    samples,
			Histograms: histograms,
		}

		result.Timeseries = append(result.Timeseries, ts)
	}

	return result, s.Err()
}

func negotiateResponseType(accepted []prompb.ReadRequest_ResponseType) (prompb.ReadRequest_ResponseType, error) {
	if len(accepted) == 0 {
		return prompb.ReadRequest_SAMPLES, nil
	}

	supported := map[prompb.ReadRequest_ResponseType]struct{}{
		prompb.ReadRequest_SAMPLES:             {},
		prompb.ReadRequest_STREAMED_XOR_CHUNKS: {},
	}

	for _, resType := range accepted {
		if _, ok := supported[resType]; ok {
			return resType, nil
		}
	}
	return 0, errors.Errorf("server does not support any of the requested response types: %v; supported: %v", accepted, supported)
}

func streamChunkedReadResponses(stream io.Writer, ss storage.ChunkSeriesSet, queryIndex, maxBytesInFrame int) error {
	var (
		chks []prompb.Chunk
		lbls []prompb.Label
	)

	var iter chunks.Iterator
	for ss.Next() {
		series := ss.At()
		iter = series.Iterator(iter)
		lbls = prompb.FromLabels(series.Labels(), nil)

		frameBytesRemaining := initializedFrameBytesRemaining(maxBytesInFrame, lbls)
		isNext := iter.Next()

		for isNext {
			chk := iter.At()

			if chk.Chunk == nil {
				return errors.Errorf("found not populated chunk returned by SeriesSet at ref: %v", chk.Ref)
			}

			// Cut the chunk.
			chks = append(chks, prompb.Chunk{
				MinTimeMs: chk.MinTime,
				MaxTimeMs: chk.MaxTime,
				Type:      prompb.Chunk_Encoding(chk.Chunk.Encoding()),
				Data:      chk.Chunk.Bytes(),
			})
			frameBytesRemaining -= chks[len(chks)-1].Size()

			// We are fine with minor inaccuracy of max bytes per frame. The inaccuracy will be max of full chunk size.
			isNext = iter.Next()
			if frameBytesRemaining > 0 && isNext {
				continue
			}

			b, err := proto.Marshal(&prompb.ChunkedReadResponse{
				ChunkedSeries: []*prompb.ChunkedSeries{
					{
						Labels: lbls,
						Chunks: chks,
					},
				},
				QueryIndex: int64(queryIndex),
			})
			if err != nil {
				return errors.Wrap(err, "marshal client.StreamReadResponse")
			}

			if _, err := stream.Write(b); err != nil {
				return errors.Wrap(err, "write to stream")
			}
			chks = chks[:0]
			frameBytesRemaining = initializedFrameBytesRemaining(maxBytesInFrame, lbls)
		}
		if err := iter.Err(); err != nil {
			return err
		}
	}
	return ss.Err()
}

func initializedFrameBytesRemaining(maxBytesInFrame int, lbls []prompb.Label) int {
	frameBytesLeft := maxBytesInFrame
	for _, lbl := range lbls {
		frameBytesLeft -= lbl.Size()
	}
	return frameBytesLeft
}

// queryFromRemoteReadQuery returns the queried time range and label matchers for the given remote
// read request query.
func queryFromRemoteReadQuery(query *prompb.Query) (start, end, minT, maxT model.Time, matchers []*labels.Matcher, hints *storage.SelectHints, err error) {
	matchers, err = prom_remote.FromLabelMatchers(query.Matchers)
	if err != nil {
		return
	}

	start = model.Time(query.StartTimestampMs)
	end = model.Time(query.EndTimestampMs)
	minT = start
	maxT = end

	hints = &storage.SelectHints{
		Start: query.StartTimestampMs,
		End:   query.EndTimestampMs,
	}

	// Honor the start/end timerange defined in the read hints, but protect from the case
	// the passed read hints are zero values (because unintentionally initialised but not set).
	if query.Hints != nil && query.Hints.StartMs > 0 {
		hints.Start = query.Hints.StartMs
		minT = model.Time(hints.Start)
	}
	if query.Hints != nil && query.Hints.EndMs > 0 {
		hints.End = query.Hints.EndMs
		maxT = model.Time(hints.End)
	}

	return
}
