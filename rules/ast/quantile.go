// Copyright 2015 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ast

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"sort"

	clientmodel "github.com/prometheus/client_golang/model"
)

// Helpers to calculate quantiles.

type bucket struct {
	upperBound float64
	count      clientmodel.SampleValue
}

// buckets implements sort.Interface.
type buckets []bucket

func (b buckets) Len() int           { return len(b) }
func (b buckets) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }
func (b buckets) Less(i, j int) bool { return b[i].upperBound < b[j].upperBound }

type metricWithBuckets struct {
	metric  clientmodel.COWMetric
	buckets buckets
}

// quantile calculates the quantile 'q' based on the given buckets. The buckets
// will be sorted by upperBound by this function (i.e. no sorting needed before
// calling this function). The quantile value is interpolated assuming a linear
// distribution within a bucket. However, if the quantile falls into the highest
// bucket, the upper bound of the 2nd highest bucket is returned. A natural
// lower bound of 0 is assumed if the upper bound of the lowest bucket is
// greater 0. In that case, interpolation in the lowest bucket happens linearly
// between 0 and the upper bound of the lowest bucket. However, if the lowest
// bucket has an upper bound less or equal 0, this upper bound is returned if
// the quantile falls into the lowest bucket.
//
// There are a number of special cases (once we have a way to report errors
// happening during evaluations of AST functions, we should report those
// explicitly):
//
// If 'buckets' has fewer than 2 elements, NaN is returned.
//
// If the highest bucket is not +Inf, NaN is returned.
//
// If q<0, -Inf is returned.
//
// If q>1, +Inf is returned.
func quantile(q clientmodel.SampleValue, buckets buckets) float64 {
	if q < 0 {
		return math.Inf(-1)
	}
	if q > 1 {
		return math.Inf(+1)
	}
	if len(buckets) < 2 {
		return math.NaN()
	}
	sort.Sort(buckets)
	if !math.IsInf(buckets[len(buckets)-1].upperBound, +1) {
		return math.NaN()
	}

	rank := q * buckets[len(buckets)-1].count
	b := sort.Search(len(buckets)-1, func(i int) bool { return buckets[i].count >= rank })

	if b == len(buckets)-1 {
		return buckets[len(buckets)-2].upperBound
	}
	if b == 0 && buckets[0].upperBound <= 0 {
		return buckets[0].upperBound
	}
	var (
		bucketStart float64
		bucketEnd   = buckets[b].upperBound
		count       = buckets[b].count
	)
	if b > 0 {
		bucketStart = buckets[b-1].upperBound
		count -= buckets[b-1].count
		rank -= buckets[b-1].count
	}
	return bucketStart + (bucketEnd-bucketStart)*float64(rank/count)
}

// bucketFingerprint works like the Fingerprint method of Metric, but ignores
// the name and the bucket label.
func bucketFingerprint(m clientmodel.Metric) clientmodel.Fingerprint {
	numLabels := 0
	if len(m) > 2 {
		numLabels = len(m) - 2
	}
	labelNames := make([]string, 0, numLabels)
	maxLength := 0

	for labelName, labelValue := range m {
		if labelName == clientmodel.MetricNameLabel || labelName == clientmodel.BucketLabel {
			continue
		}
		labelNames = append(labelNames, string(labelName))
		if len(labelName) > maxLength {
			maxLength = len(labelName)
		}
		if len(labelValue) > maxLength {
			maxLength = len(labelValue)
		}
	}

	sort.Strings(labelNames)

	summer := fnv.New64a()
	buf := make([]byte, maxLength)

	for _, labelName := range labelNames {
		labelValue := m[clientmodel.LabelName(labelName)]

		copy(buf, labelName)
		summer.Write(buf[:len(labelName)])
		summer.Write([]byte{clientmodel.SeparatorByte})

		copy(buf, labelValue)
		summer.Write(buf[:len(labelValue)])
		summer.Write([]byte{clientmodel.SeparatorByte})
	}

	return clientmodel.Fingerprint(binary.LittleEndian.Uint64(summer.Sum(nil)))
}
