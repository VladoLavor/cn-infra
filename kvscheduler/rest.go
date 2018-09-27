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
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/unrolled/render"

	"github.com/ligato/cn-infra/rpc/rest"
	. "github.com/ligato/cn-infra/kvscheduler/api"
	"github.com/ligato/cn-infra/kvscheduler/internal/graph"
)

const (
	// prefix used for REST urls of the scheduler.
	urlPrefix = "/scheduler/"

	// txnHistoryURL is URL used to obtain the transaction history.
	txnHistoryURL = urlPrefix + "txn-history"

	// sinceArg is the name of the argument used to define the start of the time
	// window for the transaction history to display.
	sinceArg = "since"

	// untilArg is the name of the argument used to define the end of the time
	// window for the transaction history to display.
	untilArg = "until"

	// seqNumArg is the name of the argument used to define the sequence number
	// of the transaction to display (txnHistoryURL).
	seqNumArg = "seq-num"

	// keyTimelineURL is URL used to obtain timeline of value changes for a given key.
	keyTimelineURL = urlPrefix + "key-timeline"

	// keyArg is the name of the argument used to define key for "key-timeline" API.
	keyArg = "key"

	// graphSnapshotURL is URL used to obtain graph snapshot from a given point in time.
	graphSnapshotURL = urlPrefix + "graph-snapshot"

	// flagStatsURL is URL used to obtain flag statistics.
	flagStatsURL = urlPrefix + "flag-stats"

	// flagArg is the name of the argument used to define flag for "flag-stats" API.
	flagArg = "flag"

	// prefixArg is the name of the argument used to define prefix to filter keys
	// for "flag-stats" API.
	prefixArg = "prefix"

	// time is the name of the argument used to define point in time for a graph snapshot
	// to retrieve.
	timeArg = "time"

	// downstreamResyncURL is URL used to trigger downstream-resync.
	downstreamResyncURL = urlPrefix + "downstream-resync"

	// dumpURL is URL used to dump either SB or scheduler's internal state of kv-pairs
	// under the given descriptor.
	dumpURL = urlPrefix + "dump"

	// descriptorArg is the name of the argument used to define descriptor for "dump" API.
	descriptorArg = "descriptor"

	// internalArg is the name of the argument used for "dump" API to tell whether
	// to dump SB or the scheduler's internal view of SB.
	internalArg = "internal"
)

// registerHandlers registers all supported REST APIs.
func (scheduler *Scheduler) registerHandlers(http rest.HTTPHandlers) {
	if http == nil {
		scheduler.Log.Warn("No http handler provided, skipping registration of KVScheduler REST handlers")
		return
	}
	http.RegisterHTTPHandler(txnHistoryURL, scheduler.txnHistoryGetHandler, "GET")
	http.RegisterHTTPHandler(keyTimelineURL, scheduler.keyTimelineGetHandler, "GET")
	http.RegisterHTTPHandler(graphSnapshotURL, scheduler.graphSnapshotGetHandler, "GET")
	http.RegisterHTTPHandler(flagStatsURL, scheduler.flagStatsGetHandler, "GET")
	http.RegisterHTTPHandler(downstreamResyncURL, scheduler.downstreamResyncPostHandler, "POST")
	http.RegisterHTTPHandler(dumpURL, scheduler.dumpGetHandler, "GET")
}

// txnHistoryGetHandler is the GET handler for "txn-history" API.
func (scheduler *Scheduler) txnHistoryGetHandler(formatter *render.Render) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var since, until time.Time
		var seqNum int
		args := req.URL.Query()

		// parse optional *seq-num* argument
		if seqNumStr, withSeqNum := args[seqNumArg]; withSeqNum && len(seqNumStr) == 1 {
			var err error
			seqNum, err = strconv.Atoi(seqNumStr[0])
			if err != nil {
				formatter.JSON(w, http.StatusInternalServerError, err)
				return
			}

			// sequence number takes precedence over the since-until time window
			txn := scheduler.getRecordedTransaction(uint(seqNum))
			if txn == nil {
				formatter.JSON(w, http.StatusNotFound, "transaction with such sequence is not recorded")
				return
			}

			formatter.Text(w, http.StatusOK, txn.StringWithOpts(false, 0))
			return
		}

		// parse optional *until* argument
		if untilStr, withUntil := args[untilArg]; withUntil && len(untilStr) == 1 {
			var err error
			until, err = stringToTime(untilStr[0])
			if err != nil {
				formatter.JSON(w, http.StatusInternalServerError, err)
				return
			}
		}

		// parse optional *since* argument
		if sinceStr, withSince := args[sinceArg]; withSince && len(sinceStr) == 1 {
			var err error
			since, err = stringToTime(sinceStr[0])
			if err != nil {
				formatter.JSON(w, http.StatusInternalServerError, err)
				return
			}
		}

		txnHistory := scheduler.getTransactionHistory(since, until)
		formatter.Text(w, http.StatusOK, txnHistory.StringWithOpts(false, 0))
	}
}

// keyTimelineGetHandler is the GET handler for "key-timeline" API.
func (scheduler *Scheduler) keyTimelineGetHandler(formatter *render.Render) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		args := req.URL.Query()

		// parse mandatory *key* argument
		if keys, withKey := args[keyArg]; withKey && len(keys) == 1 {
			graphR := scheduler.graph.Read()
			defer graphR.Release()

			timeline := graphR.GetNodeTimeline(keys[0])
			formatter.JSON(w, http.StatusOK, timeline)
			return
		}

		err := errors.New("missing key argument")
		formatter.JSON(w, http.StatusInternalServerError, err)
		return
	}
}

// graphSnapshotGetHandler is the GET handler for "graph-snapshot" API.
func (scheduler *Scheduler) graphSnapshotGetHandler(formatter *render.Render) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		timeVal := time.Now()
		args := req.URL.Query()

		// parse optional *time* argument
		if timeStr, withTime := args[timeArg]; withTime && len(timeStr) == 1 {
			var err error
			timeVal, err = stringToTime(timeStr[0])
			if err != nil {
				formatter.JSON(w, http.StatusInternalServerError, err)
				return
			}
		}

		graphR := scheduler.graph.Read()
		defer graphR.Release()

		snapshot := graphR.GetSnapshot(timeVal)
		formatter.JSON(w, http.StatusOK, snapshot)
	}
}

// flagStatsGetHandler is the GET handler for "flag-stats" API.
func (scheduler *Scheduler) flagStatsGetHandler(formatter *render.Render) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		args := req.URL.Query()
		var prefixes []string

		// parse repeated *prefix* argument
		prefixes, _ = args[prefixArg]

		if flags, withFlag := args[flagArg]; withFlag && len(flags) == 1 {
			graphR := scheduler.graph.Read()
			defer graphR.Release()

			stats := graphR.GetFlagStats(flags[0], func(key string) bool {
				if len(prefixes) == 0 {
					return true
				}
				for _, prefix := range prefixes {
					if strings.HasPrefix(key, prefix) {
						return true
					}
				}
				return false
			})
			formatter.JSON(w, http.StatusOK, stats)
			return
		}

		err := errors.New("missing flag argument")
		formatter.JSON(w, http.StatusInternalServerError, err)
		return
	}
}

// downstreamResyncPostHandler is the POST handler for "downstream-resync" API.
func (scheduler *Scheduler) downstreamResyncPostHandler(formatter *render.Render) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := context.Background()
		ctx = WithDownstreamResync(ctx)
		kvErrors, txnError := scheduler.StartNBTransaction().Commit(ctx)
		if txnError != nil {
			formatter.JSON(w, http.StatusInternalServerError, txnError)
			return
		}
		if len(kvErrors) > 0 {
			formatter.JSON(w, http.StatusInternalServerError, kvErrors)
			return
		}
		formatter.Text(w, http.StatusOK, "SB was successfully synchronized with KVScheduler")
		return
	}
}

// dumpGetHandler is the GET handler for "dump" API.
func (scheduler *Scheduler) dumpGetHandler(formatter *render.Render) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		args := req.URL.Query()

		// parse mandatory *descriptor* argument
		descriptors, withDescriptor := args[descriptorArg]
		if !withDescriptor {
			err := errors.New("missing descriptor argument")
			formatter.JSON(w, http.StatusInternalServerError, err)
			return
		}
		if len(descriptors) != 1 {
			err := errors.New("descriptor argument listed more than once")
			formatter.JSON(w, http.StatusInternalServerError, err)
			return
		}
		descriptor := descriptors[0]

		// parse optional *internal* argument
		internalDump := false
		if internalStr, withInternal := args[internalArg]; withInternal && len(internalStr) == 1 {
			internalVal := internalStr[0]
			if internalVal == "true" || internalVal == "1" {
				internalDump = true
			}
		}

		// pause transaction processing
		if !internalDump {
			scheduler.txnLock.Lock()
			defer scheduler.txnLock.Unlock()
		}

		graphR := scheduler.graph.Read()
		defer graphR.Release()

		// dump from the in-memory graph first (for SB Dump it is used for correlation)
		inMemNodes := nodesToKVPairsWithMetadata(
			graphR.GetNodes(nil,
				graph.WithFlags(&DescriptorFlag{descriptor}),
				graph.WithoutFlags(&PendingFlag{}, &DerivedFlag{})))

		if internalDump {
			// return the scheduler's view of SB for the given descriptor
			formatter.JSON(w, http.StatusOK, inMemNodes)
			return
		}

		// obtain Dump handler from the descriptor
		kvDescriptor := scheduler.registry.GetDescriptor(descriptor)
		if kvDescriptor == nil {
			err := errors.New("descriptor is not registered")
			formatter.JSON(w, http.StatusInternalServerError, err)
			return
		}
		if kvDescriptor.Dump == nil {
			err := errors.New("descriptor does not support Dump operation")
			formatter.JSON(w, http.StatusInternalServerError, err)
			return
		}

		// dump the state directly from SB via descriptor
		dump, err := kvDescriptor.Dump(inMemNodes)
		if err != nil {
			formatter.JSON(w, http.StatusInternalServerError, err)
			return
		}
		formatter.JSON(w, http.StatusOK, dump)
		return
	}
}

// stringToTime converts Unix timestamp from string to time.Time.
func stringToTime(s string) (time.Time, error) {
	sec, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, 0), nil
}
