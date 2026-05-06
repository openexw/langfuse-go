package metrics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/require"
)

func TestQuery_ToQueryString(t *testing.T) {
	query := &Query{
		View:       ViewTraces,
		Dimensions: []Dimension{{Field: "name"}},
		Metrics: []Metric{
			{Measure: MeasureCount, Aggregation: "count"},
		},
		Filters: []Filter{
			{Column: "name", Operator: "=", Value: "assistant"},
		},
		TimeDimension: &TimeDimension{Granularity: GranularityDay},
		FromTimestamp: mustParseTime("2024-01-01T00:00:00Z"),
		ToTimestamp:   mustParseTime("2024-01-02T00:00:00Z"),
		OrderBy: []OrderBy{
			{Field: "count", Direction: OrderDirectionDesc},
		},
		Config: &QueryConfig{Bins: 10, RowLimit: 100},
	}

	queryString, err := query.ToQueryString()
	require.NoError(t, err)

	values, err := url.ParseQuery(queryString)
	require.NoError(t, err)
	require.Contains(t, values, "query")

	var decoded Query
	err = json.Unmarshal([]byte(values.Get("query")), &decoded)
	require.NoError(t, err)

	require.Equal(t, query.View, decoded.View)
	require.Equal(t, query.Dimensions, decoded.Dimensions)
	require.Equal(t, query.Metrics, decoded.Metrics)
	require.Equal(t, query.TimeDimension, decoded.TimeDimension)
	require.Equal(t, query.FromTimestamp, decoded.FromTimestamp)
	require.Equal(t, query.ToTimestamp, decoded.ToTimestamp)
	require.Equal(t, query.OrderBy, decoded.OrderBy)
	require.NotNil(t, decoded.Config)
	require.Equal(t, query.Config.RowLimit, decoded.Config.RowLimit)
	require.Equal(t, "assistant", decoded.Filters[0].Value)
}

func TestQuery_ToQueryStringValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		query   *Query
		wantErr string
	}{
		{
			name:    "nil query",
			query:   nil,
			wantErr: "'query' is required",
		},
		{
			name: "missing view",
			query: &Query{
				Metrics:       []Metric{{Measure: "count", Aggregation: "count"}},
				FromTimestamp: mustParseTime("2024-01-01T00:00:00Z"),
				ToTimestamp:   mustParseTime("2024-01-02T00:00:00Z"),
			},
			wantErr: "'view' is required",
		},
		{
			name: "invalid metadata filter",
			query: &Query{
				View:          ViewTraces,
				Metrics:       []Metric{{Measure: MeasureCount, Aggregation: "count"}},
				Filters:       []Filter{{Column: "metadata", Operator: "=", Value: "x"}},
				FromTimestamp: mustParseTime("2024-01-01T00:00:00Z"),
				ToTimestamp:   mustParseTime("2024-01-02T00:00:00Z"),
			},
			wantErr: "'filters[0].key' is required when filtering on metadata",
		},
		{
			name: "invalid time dimension",
			query: &Query{
				View:          ViewTraces,
				Metrics:       []Metric{{Measure: MeasureCount, Aggregation: "count"}},
				TimeDimension: &TimeDimension{Granularity: Granularity("year")},
				FromTimestamp: mustParseTime("2024-01-01T00:00:00Z"),
				ToTimestamp:   mustParseTime("2024-01-02T00:00:00Z"),
			},
			wantErr: "invalid 'timeDimension.granularity'",
		},
		{
			name: "invalid order direction",
			query: &Query{
				View:          ViewTraces,
				Metrics:       []Metric{{Measure: MeasureCount, Aggregation: "count"}},
				OrderBy:       []OrderBy{{Field: "count", Direction: OrderDirection("down")}},
				FromTimestamp: mustParseTime("2024-01-01T00:00:00Z"),
				ToTimestamp:   mustParseTime("2024-01-02T00:00:00Z"),
			},
			wantErr: "invalid 'orderBy[0].direction'",
		},
		{
			name: "invalid config bounds",
			query: &Query{
				View:          ViewTraces,
				Metrics:       []Metric{{Measure: MeasureCount, Aggregation: "count"}},
				Config:        &QueryConfig{Bins: 101},
				FromTimestamp: mustParseTime("2024-01-01T00:00:00Z"),
				ToTimestamp:   mustParseTime("2024-01-02T00:00:00Z"),
			},
			wantErr: "'config.bins' must be between 1 and 100",
		},
		{
			name: "invalid measure for view",
			query: &Query{
				View:          ViewScoresCategorical,
				Metrics:       []Metric{{Measure: MeasureValue, Aggregation: "avg"}},
				FromTimestamp: mustParseTime("2024-01-01T00:00:00Z"),
				ToTimestamp:   mustParseTime("2024-01-02T00:00:00Z"),
			},
			wantErr: `invalid 'metrics[0].measure' "value" for view "scores-categorical"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.query.ToQueryString()
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestQuery_ToQueryStringViewSpecificMeasures(t *testing.T) {
	tests := []struct {
		name    string
		query   *Query
		wantErr string
	}{
		{
			name: "traces supports totalCost",
			query: &Query{
				View:          ViewTraces,
				Metrics:       []Metric{{Measure: MeasureTotalCost, Aggregation: "sum"}},
				FromTimestamp: mustParseTime("2024-01-01T00:00:00Z"),
				ToTimestamp:   mustParseTime("2024-01-02T00:00:00Z"),
			},
		},
		{
			name: "observations supports timeToFirstToken",
			query: &Query{
				View:          ViewObservations,
				Metrics:       []Metric{{Measure: MeasureTimeToFirstToken, Aggregation: "p95"}},
				FromTimestamp: mustParseTime("2024-01-01T00:00:00Z"),
				ToTimestamp:   mustParseTime("2024-01-02T00:00:00Z"),
			},
		},
		{
			name: "scores numeric supports value",
			query: &Query{
				View:          ViewScoresNumeric,
				Metrics:       []Metric{{Measure: MeasureValue, Aggregation: "avg"}},
				FromTimestamp: mustParseTime("2024-01-01T00:00:00Z"),
				ToTimestamp:   mustParseTime("2024-01-02T00:00:00Z"),
			},
		},
		{
			name: "scores categorical rejects latency",
			query: &Query{
				View:          ViewScoresCategorical,
				Metrics:       []Metric{{Measure: MeasureLatency, Aggregation: "avg"}},
				FromTimestamp: mustParseTime("2024-01-01T00:00:00Z"),
				ToTimestamp:   mustParseTime("2024-01-02T00:00:00Z"),
			},
			wantErr: `invalid 'metrics[0].measure' "latency" for view "scores-categorical"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.query.ToQueryString()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestQuery_ToQueryStringMarshalError(t *testing.T) {
	query := &Query{
		View:          ViewTraces,
		Metrics:       []Metric{{Measure: MeasureCount, Aggregation: "count"}},
		Filters:       []Filter{{Column: "name", Operator: "=", Value: func() {}}},
		FromTimestamp: mustParseTime("2024-01-01T00:00:00Z"),
		ToTimestamp:   mustParseTime("2024-01-02T00:00:00Z"),
	}

	_, err := query.ToQueryString()
	require.Error(t, err)
	require.Contains(t, err.Error(), "marshal query failed")
}

func TestRawRowHelpers(t *testing.T) {
	row := RawRow{
		"name":          json.RawMessage(`"assistant"`),
		"count_count":   json.RawMessage(`"10"`),
		"latency_p95":   json.RawMessage(`1820.5`),
		"fromTimestamp": json.RawMessage(`"2024-01-01T00:00:00Z"`),
		"histogram":     json.RawMessage(`[[0,10,4]]`),
	}

	name, err := row.String("name")
	require.NoError(t, err)
	require.Equal(t, "assistant", name)

	count, err := row.Int64("count_count")
	require.NoError(t, err)
	require.EqualValues(t, 10, count)

	latency, err := row.Float64("latency_p95")
	require.NoError(t, err)
	require.Equal(t, 1820.5, latency)

	timestamp, err := row.Time("fromTimestamp")
	require.NoError(t, err)
	require.Equal(t, mustParseTime("2024-01-01T00:00:00Z"), timestamp)

	rawHistogram, ok := row.Raw("histogram")
	require.True(t, ok)
	require.JSONEq(t, `[[0,10,4]]`, string(rawHistogram))

	var decoded struct {
		Name  string `json:"name"`
		Count string `json:"count_count"`
	}
	err = row.Decode(&decoded)
	require.NoError(t, err)
	require.Equal(t, "assistant", decoded.Name)
	require.Equal(t, "10", decoded.Count)

	typedRows, err := DecodeRows[struct {
		Name  string `json:"name"`
		Count string `json:"count_count"`
	}]([]RawRow{row})
	require.NoError(t, err)
	require.Len(t, typedRows, 1)
	require.Equal(t, "assistant", typedRows[0].Name)
	require.Equal(t, "10", typedRows[0].Count)
}

func TestRawRowHelpersErrors(t *testing.T) {
	row := RawRow{
		"name":        json.RawMessage(`123`),
		"count_count": json.RawMessage(`"10.5"`),
	}

	_, err := row.String("name")
	require.Error(t, err)
	require.Contains(t, err.Error(), `decode field "name" as string failed`)

	_, err = row.Int64("count_count")
	require.Error(t, err)
	require.Contains(t, err.Error(), `field "count_count" is not an integer`)

	_, ok := row.Raw("missing")
	require.False(t, ok)
}

func TestClient_Get(t *testing.T) {
	ctx := context.Background()

	t.Run("successful get metrics", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/metrics", r.URL.Path)
			require.Equal(t, "GET", r.Method)

			var query Query
			err := json.Unmarshal([]byte(r.URL.Query().Get("query")), &query)
			require.NoError(t, err)
			require.Equal(t, ViewTraces, query.View)
			require.Len(t, query.Metrics, 1)
			require.Equal(t, MeasureCount, query.Metrics[0].Measure)

			w.Header().Set("Content-Type", "application/json")
			_, err = w.Write([]byte(`{"data":[{"name":"assistant","count_count":"42"},{"histogram":[[0,10,4]]}]}`))
			require.NoError(t, err)
		}))
		defer server.Close()

		client := NewClient(resty.New().SetBaseURL(server.URL))
		response, err := client.Get(ctx, &Query{
			View:          ViewTraces,
			Metrics:       []Metric{{Measure: MeasureCount, Aggregation: "count"}},
			Dimensions:    []Dimension{{Field: "name"}},
			FromTimestamp: mustParseTime("2024-01-01T00:00:00Z"),
			ToTimestamp:   mustParseTime("2024-01-02T00:00:00Z"),
		})
		require.NoError(t, err)
		require.Len(t, response.Data, 2)

		name, err := response.Data[0].String("name")
		require.NoError(t, err)
		require.Equal(t, "assistant", name)

		count, err := response.Data[0].Int64("count_count")
		require.NoError(t, err)
		require.EqualValues(t, 42, count)

		rows, err := DecodeRows[struct {
			Name  string `json:"name"`
			Count string `json:"count_count"`
		}](response.Data)
		require.NoError(t, err)
		require.Equal(t, "assistant", rows[0].Name)
		require.Equal(t, "42", rows[0].Count)
	})

	t.Run("validation error does not send request", func(t *testing.T) {
		called := false
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := NewClient(resty.New().SetBaseURL(server.URL))
		_, err := client.Get(ctx, &Query{
			Metrics:       []Metric{{Measure: MeasureCount, Aggregation: "count"}},
			FromTimestamp: mustParseTime("2024-01-01T00:00:00Z"),
			ToTimestamp:   mustParseTime("2024-01-02T00:00:00Z"),
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "'view' is required")
		require.False(t, called)
	})
}

func mustParseTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t
}
