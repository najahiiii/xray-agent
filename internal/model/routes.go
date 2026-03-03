package model

import "slices"

// NormalizeRouteRules deduplicates route tags using last-write-wins semantics.
// This matches how the agent state store snapshots routes by tag.
func NormalizeRouteRules(routes []RouteRule) ([]RouteRule, []string) {
	if len(routes) == 0 {
		return nil, nil
	}

	lastIndex := make(map[string]int, len(routes))
	duplicateTags := make(map[string]struct{})
	for index, route := range routes {
		if _, exists := lastIndex[route.Tag]; exists {
			duplicateTags[route.Tag] = struct{}{}
		}
		lastIndex[route.Tag] = index
	}

	normalized := make([]RouteRule, 0, len(lastIndex))
	for index, route := range routes {
		if lastIndex[route.Tag] == index {
			normalized = append(normalized, route)
		}
	}

	if len(duplicateTags) == 0 {
		return normalized, nil
	}

	tags := make([]string, 0, len(duplicateTags))
	for tag := range duplicateTags {
		tags = append(tags, tag)
	}
	slices.Sort(tags)

	return normalized, tags
}
