// Copyright (c) 2018 Cisco and/or its affiliates.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package utils

import (
	"sort"
)

// DependsOn returns true if k1 depends on k2 based on dependencies from <deps>.
func DependsOn(k1, k2 string, deps map[string]KeySet, visited KeySet) bool {
	if visited == nil {
		visited = NewKeySet()
	}

	// check direct dependencies
	k1Deps := deps[k1]
	if _, depends := k1Deps[k2]; depends {
		return true
	}

	// continue transitively
	visited.Add(k1)
	for dep := range k1Deps {
		if _, wasVisited := visited[dep]; wasVisited {
			continue
		}
		if DependsOn(dep, k2, deps, visited) {
			return true
		}
	}
	return false
}

// TopologicalOrder orders keys topologically by Kahn's algorithm to respect
// the given dependencies.
// deps = map{ key -> <set of keys the given key depends on> }
func TopologicalOrder(keys KeySet, deps map[string]KeySet, depFirst bool, handleCycle bool) (sorted []string) {
	// copy input arguments so that they are not returned to the caller changed
	remains := keys.DeepCopy()
	remainsDeps := make(map[string]KeySet)
	for key, keyDeps := range deps {
		if !keys.Has(key) {
			continue
		}
		remainsDeps[key] = keyDeps.Intersect(keys)
	}

	// Kahn's algorithm (except for the cycle handling part):
	for len(remains) > 0 {
		// find candidate keys - keys that could follow in the order
		var candidates []string
		for key := range remains {
			// if depFirst, select keys that do not depend on anything in the remaining set
			candidate := depFirst && len(remainsDeps[key]) == 0
			if !depFirst {
				candidate = true
				// is there any other key depending on this one?
				for _, key2Deps := range remainsDeps {
					if key2Deps.Has(key) {
						candidate = false
						break
					}
				}
			}
			if candidate {
				candidates = append(candidates, key)
			}
		}

		// handle cycles
		if len(candidates) == 0 {
			if !handleCycle {
				panic("Dependency cycle!")
			}
			// select one of the keys that depend on themselves
			for key := range remains {
				if DependsOn(key, key, deps, nil) {
					candidates = append(candidates, key)
				}
			}
		}

		// to make the algorithm deterministic (for simplified testing),
		// order the candidates and select the first one
		sort.Strings(candidates)
		key := candidates[0]
		sorted = append(sorted, key)

		// remove key from the set of remaining keys
		remains.Del(key)
		delete(remainsDeps, key)
		// remove dependency edges going to this key
		for _, key2Deps := range remainsDeps {
			key2Deps.Del(key)
		}
	}
	return sorted
}
