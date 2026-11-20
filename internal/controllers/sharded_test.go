package controllers

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeObj is one entry: a version or a delete marker. version distinguishes
// multiple versions of the same key.
type fakeObj struct {
	key        string
	version    string
	size       int64
	isDelete   bool
	noncurrent bool // an older (non-latest) version
}

// fakeS3 implements S3ClientInterface with realistic (KeyMarker, VersionIdMarker)
// pagination and Prefix/Delimiter grouping into CommonPrefixes.
type fakeS3 struct {
	objs            []fakeObj // sorted by key ascending; versions of a key contiguous
	pageSize        int
	inclusiveMarker bool // model a backend that treats KeyMarker as inclusive
	inFlight        int32
	maxSeen         int32
	calls           int32 // ListObjectVersions calls

	unversioned     bool  // GetBucketVersioning reports "" (never enabled) instead of Enabled
	versioningErr   error // returned by GetBucketVersioning when set
	versioningCalls int32
	v2Calls         int32
	brokenToken     bool // ListObjectsV2 reports IsTruncated without a NextContinuationToken
}

// enter records one in-flight call for concurrency tracking; call the result on exit.
func (f *fakeS3) enter() func() {
	cur := atomic.AddInt32(&f.inFlight, 1)
	for {
		old := atomic.LoadInt32(&f.maxSeen)
		if cur <= old || atomic.CompareAndSwapInt32(&f.maxSeen, old, cur) {
			break
		}
	}
	return func() { atomic.AddInt32(&f.inFlight, -1) }
}

func (f *fakeS3) ListBuckets(_ context.Context, _ *s3.ListBucketsInput, _ ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	return &s3.ListBucketsOutput{}, nil
}

func (f *fakeS3) GetBucketVersioning(_ context.Context, _ *s3.GetBucketVersioningInput, _ ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error) {
	defer f.enter()()
	atomic.AddInt32(&f.versioningCalls, 1)
	if f.versioningErr != nil {
		return nil, f.versioningErr
	}
	if f.unversioned {
		return &s3.GetBucketVersioningOutput{}, nil
	}
	return &s3.GetBucketVersioningOutput{Status: types.BucketVersioningStatusEnabled}, nil
}

// ListObjectsV2 models an unversioned listing: delete markers are skipped,
// StartAfter is exclusive (inclusive with inclusiveMarker), and the
// continuation token is the last returned key (exclusive).
func (f *fakeS3) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	defer f.enter()()
	atomic.AddInt32(&f.v2Calls, 1)

	prefix := aws.ToString(in.Prefix)
	delim := aws.ToString(in.Delimiter)
	type ent struct {
		key      string
		isPrefix bool
		size     int64
	}
	var entries []ent
	seen := map[string]bool{}
	for _, o := range f.filtered(prefix) {
		if o.isDelete {
			continue
		}
		rest := o.key[len(prefix):]
		if delim != "" {
			if i := indexOf(rest, delim); i >= 0 {
				cp := prefix + rest[:i+len(delim)]
				if !seen[cp] {
					seen[cp] = true
					entries = append(entries, ent{key: cp, isPrefix: true})
				}
				continue
			}
		}
		entries = append(entries, ent{key: o.key, size: o.size})
	}

	start := 0
	switch tok, sa := aws.ToString(in.ContinuationToken), aws.ToString(in.StartAfter); {
	case tok != "":
		for start < len(entries) && entries[start].key <= tok {
			start++
		}
	case sa != "" && f.inclusiveMarker:
		for start < len(entries) && entries[start].key < sa {
			start++
		}
	case sa != "":
		for start < len(entries) && entries[start].key <= sa {
			start++
		}
	}
	end := start + f.pageSize
	if end > len(entries) {
		end = len(entries)
	}
	out := &s3.ListObjectsV2Output{}
	for _, e := range entries[start:end] {
		if e.isPrefix {
			out.CommonPrefixes = append(out.CommonPrefixes, types.CommonPrefix{Prefix: aws.String(e.key)})
		} else {
			out.Contents = append(out.Contents, types.Object{Key: aws.String(e.key), Size: aws.Int64(e.size)})
		}
	}
	truncated := end < len(entries)
	out.IsTruncated = aws.Bool(truncated)
	if truncated && !f.brokenToken {
		out.NextContinuationToken = aws.String(entries[end-1].key)
	}
	return out, nil
}

func (f *fakeS3) ListObjectVersions(_ context.Context, in *s3.ListObjectVersionsInput, _ ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error) {
	defer f.enter()()
	atomic.AddInt32(&f.calls, 1)

	view := f.filtered(aws.ToString(in.Prefix))
	if d := aws.ToString(in.Delimiter); d != "" {
		return f.listDelimited(view, aws.ToString(in.Prefix), d, aws.ToString(in.KeyMarker)), nil
	}
	return f.listFlat(view, aws.ToString(in.KeyMarker), aws.ToString(in.VersionIdMarker)), nil
}

func (f *fakeS3) filtered(prefix string) []fakeObj {
	if prefix == "" {
		return f.objs
	}
	var out []fakeObj
	for _, o := range f.objs {
		if len(o.key) >= len(prefix) && o.key[:len(prefix)] == prefix {
			out = append(out, o)
		}
	}
	return out
}

func (f *fakeS3) listFlat(view []fakeObj, km, vm string) *s3.ListObjectVersionsOutput {
	start := 0
	switch {
	case km == "":
		start = 0
	case vm != "":
		start = sort.Search(len(view), func(i int) bool { return view[i].key > km })
		for i, o := range view {
			if o.key == km && o.version == vm {
				start = i + 1
				break
			}
		}
	default:
		if f.inclusiveMarker {
			start = sort.Search(len(view), func(i int) bool { return view[i].key >= km })
		} else {
			start = sort.Search(len(view), func(i int) bool { return view[i].key > km })
		}
	}
	end := start + f.pageSize
	if end > len(view) {
		end = len(view)
	}
	out := &s3.ListObjectVersionsOutput{}
	for _, o := range view[start:end] {
		if o.isDelete {
			out.DeleteMarkers = append(out.DeleteMarkers, types.DeleteMarkerEntry{Key: aws.String(o.key), VersionId: aws.String(o.version), IsLatest: aws.Bool(true)})
		} else {
			out.Versions = append(out.Versions, types.ObjectVersion{Key: aws.String(o.key), VersionId: aws.String(o.version), Size: aws.Int64(o.size), IsLatest: aws.Bool(!o.noncurrent)})
		}
	}
	truncated := end < len(view)
	out.IsTruncated = aws.Bool(truncated)
	if truncated {
		out.NextKeyMarker = aws.String(view[end-1].key)
		out.NextVersionIdMarker = aws.String(view[end-1].version)
	}
	return out
}

func (f *fakeS3) listDelimited(view []fakeObj, prefix, delim, km string) *s3.ListObjectVersionsOutput {
	type ent struct {
		key      string
		isPrefix bool
		obj      fakeObj
	}
	var entries []ent
	seen := map[string]bool{}
	for _, o := range view {
		rest := o.key[len(prefix):]
		if i := indexOf(rest, delim); i >= 0 {
			cp := prefix + rest[:i+len(delim)]
			if !seen[cp] {
				seen[cp] = true
				entries = append(entries, ent{key: cp, isPrefix: true})
			}
		} else {
			entries = append(entries, ent{key: o.key, obj: o})
		}
	}
	start := 0
	if km != "" {
		for start < len(entries) && entries[start].key <= km {
			start++
		}
	}
	end := start + f.pageSize
	if end > len(entries) {
		end = len(entries)
	}
	out := &s3.ListObjectVersionsOutput{}
	for _, e := range entries[start:end] {
		switch {
		case e.isPrefix:
			out.CommonPrefixes = append(out.CommonPrefixes, types.CommonPrefix{Prefix: aws.String(e.key)})
		case e.obj.isDelete:
			out.DeleteMarkers = append(out.DeleteMarkers, types.DeleteMarkerEntry{Key: aws.String(e.obj.key), VersionId: aws.String(e.obj.version), IsLatest: aws.Bool(true)})
		default:
			out.Versions = append(out.Versions, types.ObjectVersion{Key: aws.String(e.obj.key), VersionId: aws.String(e.obj.version), Size: aws.Int64(e.obj.size), IsLatest: aws.Bool(!e.obj.noncurrent)})
		}
	}
	truncated := end < len(entries)
	out.IsTruncated = aws.Bool(truncated)
	if truncated {
		out.NextKeyMarker = aws.String(entries[end-1].key)
	}
	return out
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func rangeData() []fakeObj {
	return []fakeObj{
		{key: "a", version: "v1", size: 1},
		{key: "k", version: "v3", size: 10},
		{key: "k", version: "v2", size: 10},
		{key: "k", version: "v1", size: 10},
		{key: "m", version: "v1", size: 5, isDelete: true},
		{key: "z", version: "v1", size: 2},
	}
}

func TestLimitedClient_BoundsConcurrency(t *testing.T) {
	objs := make([]fakeObj, 200)
	for i := range objs {
		objs[i] = fakeObj{key: string(rune('a'+i%26)) + string(rune('0'+i/26)), version: "v1", size: 1}
	}
	sort.Slice(objs, func(i, j int) bool { return objs[i].key < objs[j].key })
	base := &fakeS3{objs: objs, pageSize: 1} // pageSize 1 → many overlapping calls
	limited := newLimitedClient(base, 4)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = listRange(context.Background(), limited, "bucket", "", "", modeVersions)
		}()
	}
	wg.Wait()

	assert.LessOrEqual(t, int(atomic.LoadInt32(&base.maxSeen)), 4,
		"concurrent ListObjectVersions must not exceed the limit")
}

func TestLimitedClient_BoundsObjectsV2AndVersioningCalls(t *testing.T) {
	objs := make([]fakeObj, 200)
	for i := range objs {
		objs[i] = fakeObj{key: string(rune('a'+i%26)) + string(rune('0'+i/26)), version: "null", size: 1}
	}
	sort.Slice(objs, func(i, j int) bool { return objs[i].key < objs[j].key })
	base := &fakeS3{objs: objs, pageSize: 1, unversioned: true}
	limited := newLimitedClient(base, 4)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			for j := 0; j < 10; j++ {
				_, _ = limited.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String("b")})
				_, _ = limited.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String("b")})
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(200), atomic.LoadInt32(&base.versioningCalls))
	assert.Equal(t, int32(200), atomic.LoadInt32(&base.v2Calls))
	assert.LessOrEqual(t, int(atomic.LoadInt32(&base.maxSeen)), 4,
		"concurrent GetBucketVersioning/ListObjectsV2 calls must not exceed the limit")
}

func hierData() []fakeObj {
	return []fakeObj{
		{key: "c.txt", version: "v1", size: 1},
		{key: "demo/a/1", version: "v1", size: 10},
		{key: "demo/a/2", version: "v1", size: 10},
		{key: "demo/b/1", version: "v1", size: 10, isDelete: true},
		{key: "demo/b/2", version: "v1", size: 10},
		{key: "x/y/z/1", version: "v1", size: 5},
	}
}

func TestS3UsageInfo_ShardedMatchesData(t *testing.T) {
	client := &fakeS3{objs: hierData(), pageSize: 1} // tiny pages exercise pagination + ranges
	summary, err := S3UsageInfo(context.Background(), "us-east-1", client, "bucket", 4)
	require.NoError(t, err)

	require.Len(t, summary.S3Buckets, 1)
	std := summary.StorageClasses["STANDARD"]
	// hierData: 5 versions (c.txt, demo/a/1, demo/a/2, demo/b/2, x/y/z/1), 1 delete marker (demo/b/1).
	assert.Equal(t, float64(5), std.CurrentObjectNumber)
	assert.Equal(t, float64(1+10+10+10+5), std.CurrentSize)
	assert.Equal(t, float64(1), summary.DeleteMarkers)
	assert.True(t, summary.EndpointStatus)
	assert.Equal(t, 1, summary.BucketCount)
	assert.Equal(t, 0, summary.FailedBucketCount)
}

func TestDiscoverBoundaries(t *testing.T) {
	client := &fakeS3{objs: hierData(), pageSize: 10}
	b, err := discoverBoundaries(context.Background(), client, "bucket", 100, 5, modeVersions)
	require.NoError(t, err)
	assert.True(t, sort.StringsAreSorted(b))
	assert.Contains(t, b, "demo/")
	assert.Contains(t, b, "x/")
}

func TestCoalesceBoundaries(t *testing.T) {
	in := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	assert.Equal(t, []string{"c", "e", "g"}, coalesceBoundaries(in, 4))
	assert.Equal(t, []string{"a", "b"}, coalesceBoundaries([]string{"a", "b"}, 8))
	assert.Nil(t, coalesceBoundaries(nil, 8))
}

func TestListRange_CoversFullRangeOnce(t *testing.T) {
	// pageSize 2 forces "k"'s versions to straddle a page boundary.
	client := &fakeS3{objs: rangeData(), pageSize: 2}
	sc, dm, err := listRange(context.Background(), client, "bucket", "", "", modeVersions)
	require.NoError(t, err)
	std := sc["STANDARD"]
	assert.Equal(t, float64(5), std.CurrentObjectNumber, "all 5 versions counted (k's straddling versions not lost)")
	assert.Equal(t, float64(1+10+10+10+2), std.CurrentSize)
	assert.Equal(t, float64(1), dm)
}

func TestListRange_BoundaryKeyCountedOnce(t *testing.T) {
	data := rangeData()
	lowClient := &fakeS3{objs: data, pageSize: 10}
	highClient := &fakeS3{objs: data, pageSize: 10}

	// Range split at "k": (",k] and (k,""]. "k"'s versions belong to the lower range only.
	scLow, _, err := listRange(context.Background(), lowClient, "bucket", "", "k", modeVersions)
	require.NoError(t, err)
	scHigh, _, err := listRange(context.Background(), highClient, "bucket", "k", "", modeVersions)
	require.NoError(t, err)

	total := scLow["STANDARD"].CurrentObjectNumber + scHigh["STANDARD"].CurrentObjectNumber
	assert.Equal(t, float64(5), total, "boundary key 'k' counted exactly once across the two ranges")
}

// Some S3-compatible backends treat KeyMarker as inclusive (they return the
// marker key itself) rather than exclusive like AWS. listRange must still
// enforce the half-open (lo, hi] lower bound itself, so a boundary key is never
// double-counted across adjacent ranges on such a backend.
func TestListRange_LowerBoundExcludesMarkerKey(t *testing.T) {
	data := []fakeObj{
		{key: "k", version: "v1", size: 10},                // boundary key (version)
		{key: "k", version: "vd", size: 0, isDelete: true}, // boundary key (delete marker)
		{key: "z", version: "v1", size: 2},
	}
	client := &fakeS3{objs: data, pageSize: 10, inclusiveMarker: true}

	// Range (k, ""]: even though the backend returns "k", it must be excluded.
	sc, dm, err := listRange(context.Background(), client, "bucket", "k", "", modeVersions)
	require.NoError(t, err)
	assert.Equal(t, float64(1), sc["STANDARD"].CurrentObjectNumber, "boundary key 'k' excluded; only 'z' counted")
	assert.Equal(t, float64(2), sc["STANDARD"].CurrentSize)
	assert.Equal(t, float64(0), dm, "boundary key 'k' delete marker excluded")
}

// unversionedData is a never-versioned bucket: one "null" version per key.
func unversionedData() []fakeObj {
	return []fakeObj{
		{key: "a", version: "null", size: 1},
		{key: "k", version: "null", size: 10},
		{key: "m", version: "null", size: 5},
		{key: "z", version: "null", size: 2},
	}
}

func TestListRangeV2_CoversFullRangeOnce(t *testing.T) {
	client := &fakeS3{objs: unversionedData(), pageSize: 2, unversioned: true}
	sc, dm, err := listRange(context.Background(), client, "bucket", "", "", modeObjectsV2)
	require.NoError(t, err)
	std := sc["STANDARD"]
	assert.Equal(t, float64(4), std.CurrentObjectNumber)
	assert.Equal(t, float64(1+10+5+2), std.CurrentSize)
	assert.Equal(t, float64(0), std.NoncurrentObjectNumber)
	assert.Equal(t, float64(0), dm)
	assert.Equal(t, int32(0), atomic.LoadInt32(&client.calls), "ListObjectVersions must not be used")
}

func TestListRangeV2_BoundaryKeyCountedOnce(t *testing.T) {
	low := &fakeS3{objs: unversionedData(), pageSize: 10, unversioned: true}
	high := &fakeS3{objs: unversionedData(), pageSize: 10, unversioned: true}
	scLow, _, err := listRange(context.Background(), low, "bucket", "", "k", modeObjectsV2)
	require.NoError(t, err)
	scHigh, _, err := listRange(context.Background(), high, "bucket", "k", "", modeObjectsV2)
	require.NoError(t, err)
	assert.Equal(t, float64(2), scLow["STANDARD"].CurrentObjectNumber, "a, k")
	assert.Equal(t, float64(4), scLow["STANDARD"].CurrentObjectNumber+scHigh["STANDARD"].CurrentObjectNumber)
}

func TestListRangeV2_LowerBoundExcludesStartAfterKey(t *testing.T) {
	client := &fakeS3{objs: unversionedData(), pageSize: 10, unversioned: true, inclusiveMarker: true}
	sc, _, err := listRange(context.Background(), client, "bucket", "k", "", modeObjectsV2)
	require.NoError(t, err)
	assert.Equal(t, float64(2), sc["STANDARD"].CurrentObjectNumber, "only m and z")
	assert.Equal(t, float64(5+2), sc["STANDARD"].CurrentSize)
}

func TestListRangeV2_EmptyStorageClassDefaultsToStandard(t *testing.T) {
	client := &fakeS3{objs: unversionedData(), pageSize: 10, unversioned: true}
	sc, _, err := listRange(context.Background(), client, "bucket", "", "", modeObjectsV2)
	require.NoError(t, err)
	assert.Len(t, sc, 1)
	assert.Contains(t, sc, "STANDARD")
}

// A backend that reports IsTruncated without NextContinuationToken must not
// make the bucket look complete after one page: listing resumes after the
// last key seen.
func TestListRangeV2_TruncatedWithoutTokenResumesFromLastKey(t *testing.T) {
	client := &fakeS3{objs: unversionedData(), pageSize: 1, unversioned: true, brokenToken: true}
	type res struct {
		sc  map[string]StorageClassMetrics
		err error
	}
	done := make(chan res, 1)
	go func() {
		sc, _, err := listRange(context.Background(), client, "bucket", "", "", modeObjectsV2)
		done <- res{sc, err}
	}()
	select {
	case r := <-done:
		require.NoError(t, r.err)
		assert.Equal(t, float64(4), r.sc["STANDARD"].CurrentObjectNumber, "all keys counted despite missing tokens")
		assert.Equal(t, float64(1+10+5+2), r.sc["STANDARD"].CurrentSize)
	case <-time.After(time.Second):
		t.Fatal("listRange did not finish on IsTruncated without NextContinuationToken")
	}
}

// With neither a token nor any key to resume after, listing cannot make
// progress: the bucket must fail rather than report partial numbers as healthy.
func TestListRangeV2_TruncatedWithoutTokenOrProgressFails(t *testing.T) {
	m := new(MockS3Client)
	m.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).
		Return(&s3.ListObjectsV2Output{IsTruncated: aws.Bool(true)}, nil)

	_, _, err := listRange(context.Background(), m, "bucket", "", "", modeObjectsV2)
	assert.Error(t, err)
}

func TestDiscoverBoundariesV2(t *testing.T) {
	client := &fakeS3{objs: hierData(), pageSize: 10, unversioned: true}
	b, err := discoverBoundaries(context.Background(), client, "bucket", 100, 5, modeObjectsV2)
	require.NoError(t, err)
	assert.True(t, sort.StringsAreSorted(b))
	assert.Contains(t, b, "demo/")
	assert.Contains(t, b, "x/")
	assert.Greater(t, atomic.LoadInt32(&client.v2Calls), int32(0))
	assert.Equal(t, int32(0), atomic.LoadInt32(&client.calls))
}

// Missing tokens on a backend that also treats StartAfter as inclusive: the
// resumed page repeats the last key, which must not be counted twice.
func TestListRangeV2_TokenlessResumeOnInclusiveBackendCountsOnce(t *testing.T) {
	client := &fakeS3{objs: unversionedData(), pageSize: 2, unversioned: true, brokenToken: true, inclusiveMarker: true}
	sc, _, err := listRange(context.Background(), client, "bucket", "", "", modeObjectsV2)
	require.NoError(t, err)
	assert.Equal(t, float64(4), sc["STANDARD"].CurrentObjectNumber)
	assert.Equal(t, float64(1+10+5+2), sc["STANDARD"].CurrentSize)
}

// With page size 1 an inclusive backend returns only the resume key again: no
// progress is possible, so the range must fail fast instead of spinning until
// the scrape deadline.
func TestListRangeV2_TokenlessResumeWithoutProgressFails(t *testing.T) {
	client := &fakeS3{objs: unversionedData(), pageSize: 1, unversioned: true, brokenToken: true, inclusiveMarker: true}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, err := listRange(ctx, client, "bucket", "", "", modeObjectsV2)
	require.Error(t, err)
	assert.NotErrorIs(t, err, context.DeadlineExceeded, "must fail on no progress, not by timing out")
}
