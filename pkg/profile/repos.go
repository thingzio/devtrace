package profile

import (
	"sort"

	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/model"
)

const (
	// topRepoLimit caps the count of repos surfaced in the "top" list.
	topRepoLimit = 5
	// languageLimit caps the count of language buckets surfaced. Languages
	// beyond the top N are dropped (rather than rolled into "Other") to
	// keep the bucket counts faithful to actual repo classifications.
	languageLimit = 5
	// minStars excludes repos with no stargazers from the top-by-stars
	// list — a 0-star repo is rarely "their best work" worth surfacing.
	minStars = 1
)

// AggregateRepos turns a flat repo list into the OwnedRepos summary
// surfaced in enrichment. Forks and archived repos are excluded from
// every aggregate because they don't represent the contributor's own
// active work. Returns nil when no qualifying repos remain.
func AggregateRepos(repos []ghclient.Repo) *model.OwnedRepos {
	owned := make([]ghclient.Repo, 0, len(repos))
	for _, r := range repos {
		if r.Fork || r.Archived {
			continue
		}
		owned = append(owned, r)
	}
	if len(owned) == 0 {
		return nil
	}

	out := &model.OwnedRepos{
		TotalRepos: len(owned),
		Top:        topByStars(owned),
		Languages:  languageBuckets(owned),
	}
	for _, r := range owned {
		out.TotalStars += int64(r.Stars)
	}
	return out
}

func topByStars(repos []ghclient.Repo) []model.OwnedRepo {
	sorted := make([]ghclient.Repo, 0, len(repos))
	for _, r := range repos {
		if r.Stars >= minStars {
			sorted = append(sorted, r)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Stars != sorted[j].Stars {
			return sorted[i].Stars > sorted[j].Stars
		}
		// Deterministic tie-break on full name so identical-star repos
		// don't shuffle between cache writes.
		return sorted[i].FullName < sorted[j].FullName
	})

	limit := topRepoLimit
	if len(sorted) < limit {
		limit = len(sorted)
	}
	out := make([]model.OwnedRepo, 0, limit)
	for i := range limit {
		r := sorted[i]
		out = append(out, model.OwnedRepo{
			Name:        r.FullName,
			Stars:       r.Stars,
			Language:    r.Language,
			Description: r.Description,
		})
	}
	return out
}

func languageBuckets(repos []ghclient.Repo) []model.LanguageBucket {
	counts := make(map[string]int)
	classified := 0
	for _, r := range repos {
		if r.Language == "" {
			continue
		}
		counts[r.Language]++
		classified++
	}
	if classified == 0 {
		return nil
	}

	buckets := make([]model.LanguageBucket, 0, len(counts))
	for lang, n := range counts {
		buckets = append(buckets, model.LanguageBucket{
			Language: lang,
			Repos:    n,
			Share:    float64(n) / float64(classified),
		})
	}
	sort.SliceStable(buckets, func(i, j int) bool {
		if buckets[i].Repos != buckets[j].Repos {
			return buckets[i].Repos > buckets[j].Repos
		}
		return buckets[i].Language < buckets[j].Language
	})

	if len(buckets) > languageLimit {
		buckets = buckets[:languageLimit]
	}
	return buckets
}
