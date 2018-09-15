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

package kvscheduler

import (
	"strconv"
	"time"

	. "github.com/ligato/cn-infra/kvscheduler/api"
	"github.com/ligato/cn-infra/kvscheduler/graph"
)

type keySet map[string]struct{}

func (ks keySet) add(key string) keySet {
	ks[key] = struct{}{}
	return ks
}

// subtract removes keys from <ks> that are in both key sets.
func (ks keySet) subtract(ks2 keySet) keySet {
	for key := range ks2 {
		delete(ks, key)
	}
	return ks
}

func stringToTime(s string) (time.Time, error) {
	sec, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, 0), nil
}

func nodesToKVPairs(nodes []graph.Node) (kvPairs []KeyValuePair) {
	for _, node := range nodes {
		kvPairs = append(kvPairs, KeyValuePair{
			Key:   node.GetKey(),
			Value: node.GetValue()})
	}
	return kvPairs
}

func nodesToKeysWithError(nodes []graph.Node) (kvPairs []KeyWithError) {
	for _, node := range nodes {
		kvPairs = append(kvPairs, KeyWithError{
			Key:   node.GetKey(),
			Error: getNodeError(node),
		})
	}
	return kvPairs
}

func nodesToKVPairsWithMetadata(nodes []graph.Node) (kvPairs []KVWithMetadata) {
	for _, node := range nodes {
		kvPairs = append(kvPairs, KVWithMetadata{
			Key:      node.GetKey(),
			Value:    node.GetValue(),
			Metadata: node.GetMetadata(),
			Origin:   getNodeOrigin(node),
		})
	}
	return kvPairs
}

// constructTargets builds targets for the graph based on derived values and dependencies.
func constructTargets(deps []Dependency, derives []KeyValuePair) (targets []graph.RelationTarget) {
	for _, dep := range deps {
		target := graph.RelationTarget{
			Relation: DependencyRelation,
			Label:    dep.Label,
			Key:      dep.Key,
			Selector: dep.AnyOf,
		}
		targets = append(targets, target)
	}

	for _, derived := range derives {
		target := graph.RelationTarget{
			Relation: DerivesRelation,
			Label:    derived.Value.Label(),
			Key:      derived.Key,
			Selector: nil,
		}
		targets = append(targets, target)
	}

	return targets
}

// dependsOn returns true if k1 depends on k2 based on dependencies from <deps>.
func dependsOn(k1, k2 string, deps map[string]keySet, visited keySet) bool {
	if visited == nil {
		visited = make(keySet)
	}

	// check direct dependencies
	k1Deps := deps[k1]
	if _, depends := k1Deps[k2]; depends {
		return true
	}

	// continue transitively
	visited.add(k1)
	for dep := range k1Deps {
		if _, wasVisited := visited[dep]; wasVisited {
			continue
		}
		if dependsOn(dep, k2, deps, visited) {
			return true
		}
	}
	return false
}

// getNodeOrigin returns node origin stored in Origin flag.
func getNodeOrigin(node graph.Node) ValueOrigin {
	flag := node.GetFlag(OriginFlagName)
	if flag != nil {
		return flag.(*OriginFlag).origin
	}
	return UnknownOrigin
}

// getNodeOrigin returns node error stored in Error flag.
func getNodeError(node graph.Node) error {
	errorFlag := node.GetFlag(ErrorFlagName)
	if errorFlag != nil {
		return errorFlag.(*ErrorFlag).err
	}
	return nil
}

// getNodeLastChange returns info about the last change for a given node, stored in LastChange flag.
func getNodeLastChange(node graph.Node) *LastChangeFlag {
	flag := node.GetFlag(LastChangeFlagName)
	if flag == nil {
		return nil
	}
	return flag.(*LastChangeFlag)
}

// getNodeLastUpdate returns info about the last update for a given node, stored in LastChange flag.
func getNodeLastUpdate(node graph.Node) *LastUpdateFlag {
	flag := node.GetFlag(LastUpdateFlagName)
	if flag == nil {
		return nil
	}
	return flag.(*LastUpdateFlag)
}

func isNodeDerived(node graph.Node) bool {
	return node.GetFlag(DerivedFlagName) != nil
}

func isNodePending(node graph.Node) bool {
	return node.GetFlag(PendingFlagName) != nil
}

// isNodeReady return true if the given node has all dependencies satisfied.
// Recursive calls are needed to handle circular dependencies - nodes of a strongly
// connected component are treated as if they were squashed into one.
func isNodeReady(node graph.Node) bool {
	if getNodeOrigin(node) == FromSB {
		// for SB values dependencies are not checked
		return true
	}
	return isNodeReadyRec(node, node, make(keySet))
}

// isNodeReadyRec is a recursive call from within isNodeReady.
func isNodeReadyRec(src, current graph.Node, visited keySet) bool {
	cycle := false
	visited.add(current.GetKey())
	defer delete(visited, current.GetKey())

	for _, targets := range current.GetTargets(DependencyRelation) {
		satisfied := false
		for _, target := range targets {
			if isNodeBeingRemoved(target) {
				// do not consider values that are about to be removed
				continue
			}
			if !isNodePending(target) {
				satisfied = true
				if current.GetKey() == src.GetKey() {
					break
				}
			}
			// test if this is a strongly-connected component that includes "src" (treated as one node)
			_, wasVisited := visited[target.GetKey()]
			if target.GetKey() == src.GetKey() || (!wasVisited && isNodeReadyRec(src, target, visited)) {
				cycle = true
				satisfied = true
				break
			}
		}
		if !satisfied {
			return false
		}
	}
	return current.GetKey() == src.GetKey() || cycle
}

// isNodeBeingRemoved returns true for a given node if it is being removed
// by a transaction or a notification (including failed removal attempt).
func isNodeBeingRemoved(node graph.Node) bool {
	base := node
	if isNodeDerived(node) {
		for {
			derivedFrom := base.GetSources(DerivesRelation)
			if len(derivedFrom) == 0 {
				break
			}
			base = derivedFrom[0]
			if isNodePending(base) {
				// one of the values from which this derives is pending
				return true
			}
		}
		if isNodeDerived(base) {
			// derived without base -> is is being removed by Modify()
			return true
		}
	}
	if getNodeLastChange(base) != nil && getNodeLastChange(base).value == nil {
		// about to be removed by transaction
		return true
	}
	return false
}

func canNodeHaveMetadata(node graph.Node) bool {
	return !isNodeDerived(node)
}

func getNodeBase(node graph.Node) graph.Node {
	derivedFrom := node.GetSources(DerivesRelation)
	if len(derivedFrom) == 0 {
		return node
	}
	return getNodeBase(derivedFrom[0])
}

func getDerivedNodes(node graph.Node) (derived []graph.Node) {
	for _, derivedNodes := range node.GetTargets(DerivesRelation) {
		for _, derivedNode := range derivedNodes {
			derived = append(derived, derivedNode)
		}
	}
	return derived
}

func getDerivedKeys(node graph.Node) keySet {
	set := make(keySet)
	for _, derived := range getDerivedNodes(node) {
		set.add(derived.GetKey())
	}
	return set
}
