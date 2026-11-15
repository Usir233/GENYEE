package searchutil

import (
	"math"
	"sort"
)

// KeywordScoreCallbacks allows callers to hook into normalization telemetry.
type KeywordScoreCallbacks struct {
	OnNoVariance func(count int, score float64)
	OnNormalized func(count int, rawMin, rawMax, normalizeMin, normalizeMax float64)
}

// NormalizeKeywordScores normalizes keyword match scores in-place using robust percentile bounds.
func NormalizeKeywordScores[T any](
	results []T,
	isKeyword func(T) bool,
	getScore func(T) float64,
	setScore func(T, float64),
	callbacks KeywordScoreCallbacks,
) {
	keywordResults := make([]T, 0, len(results))
	for _, result := range results {
		if isKeyword(result) {
			keywordResults = append(keywordResults, result)
		}
	}

	if len(keywordResults) == 0 {
		return
	}

	if len(keywordResults) == 1 {
		raw := getScore(keywordResults[0])
		if math.IsNaN(raw) || math.IsInf(raw, -1) {
			setScore(keywordResults[0], 0.0)
			return
		}
		setScore(keywordResults[0], 1.0)
		return
	}

	finiteScores := make([]float64, 0, len(keywordResults))
	for _, r := range keywordResults {
		s := getScore(r)
		if !math.IsNaN(s) && !math.IsInf(s, 0) {
			finiteScores = append(finiteScores, s)
		}
	}

	if len(finiteScores) == 0 {
		for _, r := range keywordResults {
			s := getScore(r)
			if math.IsInf(s, 1) {
				setScore(r, 1.0)
			} else {
				setScore(r, 0.0)
			}
		}
		return
	}

	minS := finiteScores[0]
	maxS := minS
	for _, score := range finiteScores[1:] {
		if score < minS {
			minS = score
		}
		if score > maxS {
			maxS = score
		}
	}

	if maxS <= minS {
		for _, r := range keywordResults {
			s := getScore(r)
			if math.IsNaN(s) || math.IsInf(s, -1) {
				setScore(r, 0.0)
			} else {
				setScore(r, 1.0)
			}
		}
		if callbacks.OnNoVariance != nil {
			callbacks.OnNoVariance(len(keywordResults), minS)
		}
		return
	}

	normalizeMin := minS
	normalizeMax := maxS

	if len(finiteScores) >= 10 {
		scores := make([]float64, len(finiteScores))
		copy(scores, finiteScores)
		sort.Float64s(scores)
		p5Idx := len(scores) * 5 / 100
		p95Idx := len(scores) * 95 / 100
		if p5Idx < len(scores) {
			normalizeMin = scores[p5Idx]
		}
		if p95Idx < len(scores) {
			normalizeMax = scores[p95Idx]
		}
	}

	rangeSize := normalizeMax - normalizeMin
	if rangeSize > 0 && !math.IsNaN(rangeSize) && !math.IsInf(rangeSize, 0) {
		for _, r := range keywordResults {
			raw := getScore(r)
			if math.IsNaN(raw) || math.IsInf(raw, -1) {
				setScore(r, 0.0)
				continue
			}
			if math.IsInf(raw, 1) {
				setScore(r, 1.0)
				continue
			}
			clamped := raw
			if clamped < normalizeMin {
				clamped = normalizeMin
			} else if clamped > normalizeMax {
				clamped = normalizeMax
			}
			ns := (clamped - normalizeMin) / rangeSize
			if math.IsNaN(ns) || ns < 0 {
				ns = 0
			} else if ns > 1 {
				ns = 1
			}
			setScore(r, ns)
		}
		if callbacks.OnNormalized != nil {
			callbacks.OnNormalized(
				len(keywordResults),
				minS,
				maxS,
				normalizeMin,
				normalizeMax,
			)
		}
		return
	}

	// Fallback when percentile filtering collapses the range.
	for _, r := range keywordResults {
		raw := getScore(r)
		if math.IsNaN(raw) || math.IsInf(raw, -1) {
			setScore(r, 0.0)
		} else {
			setScore(r, 1.0)
		}
	}
}
