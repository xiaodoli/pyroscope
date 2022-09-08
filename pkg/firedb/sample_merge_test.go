package firedb

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-kit/log"

	commonv1 "github.com/grafana/fire/pkg/gen/common/v1"
	"github.com/grafana/fire/pkg/objstore/providers/filesystem"
	pprofth "github.com/grafana/fire/pkg/pprof/testhelper"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/require"
)

func TestMergeSample(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in       func() []*pprofth.ProfileBuilder
		expected []StacktraceValue
	}{
		{
			name: "single profile",
			in: func() (ps []*pprofth.ProfileBuilder) {
				p := pprofth.NewProfileBuilder(int64(15 * time.Second)).CPUProfile()
				p.ForStacktrace("my", "other").AddSamples(1)
				p.ForStacktrace("my", "other").AddSamples(3)
				p.ForStacktrace("my", "other", "stack").AddSamples(3)
				ps = append(ps, p)
				return
			},
			expected: []StacktraceValue{
				{StacktraceID: 0, Value: 4},
				{StacktraceID: 1, Value: 3},
			},
		},
		{
			name: "multiple profiles",
			in: func() (ps []*pprofth.ProfileBuilder) {
				for i := 0; i < 3000; i++ {
					p := pprofth.NewProfileBuilder(int64(15*time.Second)).
						CPUProfile().WithLabels("series", fmt.Sprintf("%d", i))
					p.ForStacktrace("my", "other").AddSamples(1)
					p.ForStacktrace("my", "other").AddSamples(3)
					p.ForStacktrace("my", "other", "stack").AddSamples(3)
					ps = append(ps, p)
				}
				return
			},
			expected: []StacktraceValue{
				{StacktraceID: 0, Value: 12000},
				{StacktraceID: 1, Value: 9000},
			},
		},
		{
			name: "filtering multiple profiles",
			in: func() (ps []*pprofth.ProfileBuilder) {
				for i := 0; i < 3000; i++ {
					p := pprofth.NewProfileBuilder(int64(15*time.Second)).
						MemoryProfile().WithLabels("series", fmt.Sprintf("%d", i))
					p.ForStacktrace("my", "other").AddSamples(1, 2, 3, 4)
					p.ForStacktrace("my", "other").AddSamples(3, 2, 3, 4)
					p.ForStacktrace("my", "other", "stack").AddSamples(3, 3, 3, 3)
					ps = append(ps, p)
				}
				for i := 0; i < 3000; i++ {
					p := pprofth.NewProfileBuilder(int64(15*time.Second)).
						CPUProfile().WithLabels("series", fmt.Sprintf("%d", i))
					p.ForStacktrace("my", "other").AddSamples(1)
					p.ForStacktrace("my", "other").AddSamples(3)
					p.ForStacktrace("my", "other", "stack").AddSamples(3)
					ps = append(ps, p)
				}
				return
			},
			expected: []StacktraceValue{
				{StacktraceID: 0, Value: 12000},
				{StacktraceID: 1, Value: 9000},
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			testPath := t.TempDir()
			db, err := New(&Config{
				DataPath:      testPath,
				BlockDuration: time.Duration(100000) * time.Minute, // we will manually flush
			}, log.NewNopLogger(), nil)
			require.NoError(t, err)
			ctx := context.Background()

			for _, p := range tc.in() {
				require.NoError(t, db.Head().Ingest(ctx, p.Profile, p.UUID, p.Labels...))
			}

			require.NoError(t, db.Flush(context.Background()))

			b, err := filesystem.NewBucket(filepath.Join(testPath, pathLocal))
			require.NoError(t, err)

			// open resulting block
			q := NewBlockQuerier(log.NewNopLogger(), b)
			require.NoError(t, q.Sync(context.Background()))

			merger, err := q.queriers[0].SelectMerge(ctx, SelectMergeRequest{
				LabelSelector: `{}`,
				Type: &commonv1.ProfileType{
					Name:       "process_cpu",
					SampleType: "cpu",
					SampleUnit: "nanoseconds",
					PeriodType: "cpu",
					PeriodUnit: "nanoseconds",
				},
				Start: model.TimeFromUnixNano(0),
				End:   model.TimeFromUnixNano(int64(1 * time.Minute)),
			})
			require.NoError(t, err)
			profiles := merger.SelectedProfiles()
			stacktraceIter, err := merger.MergeByStacktraces(profiles)
			require.NoError(t, err)

			actual := []StacktraceValue{}
			for stacktraceIter.Next() {
				stacktrace := stacktraceIter.At()
				actual = append(actual, stacktrace)
			}
			require.NoError(t, stacktraceIter.Err())
			require.NoError(t, err)
			require.NoError(t, stacktraceIter.Close())
			require.Equal(t, tc.expected, actual)
		})
	}
}
