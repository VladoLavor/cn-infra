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
	"testing"
	"time"

	"github.com/gogo/protobuf/proto"
	. "github.com/onsi/gomega"

	. "github.com/ligato/cn-infra/kvscheduler/api"
	"github.com/ligato/cn-infra/kvscheduler/internal/test"
	"github.com/ligato/cn-infra/kvscheduler/internal/utils"
)

func TestDataChangeTransactions(t *testing.T) {
	RegisterTestingT(t)

	// prepare KV Scheduler
	scheduler := NewPlugin(UseDeps(func(deps *Deps) {
		deps.HTTPHandlers = nil
	}))
	err := scheduler.Init()
	Expect(err).To(BeNil())

	// prepare mocks
	mockSB := test.NewMockSouthbound()
	// -> descriptor1:
	descriptor1 := test.NewMockDescriptor(&KVDescriptor{
		Name:            descriptor1Name,
		NBKeyPrefix:     prefixA,
		KeySelector:     prefixSelector(prefixA),
		ValueTypeName:   test.ArrayValueTypeName,
		DerivedValues:   test.ArrayValueDerBuilder,
		WithMetadata:    true,
	}, mockSB, 0)
	// -> descriptor2:
	descriptor2 := test.NewMockDescriptor(&KVDescriptor{
		Name:            descriptor2Name,
		NBKeyPrefix:     prefixB,
		KeySelector:     prefixSelector(prefixB),
		ValueTypeName:   test.ArrayValueTypeName,
		DerivedValues:   test.ArrayValueDerBuilder,
		Dependencies: func(key string, value proto.Message) []Dependency {
			if key == prefixB+baseValue2+"/item1" {
				depKey := prefixA + baseValue1
				return []Dependency{
					{Label: depKey, Key: depKey},
				}
			}
			if key == prefixB+baseValue2+"/item2" {
				depKey := prefixA + baseValue1 + "/item1"
				return []Dependency{
					{Label: depKey, Key: depKey},
				}
			}
			return nil
		},
		WithMetadata:     true,
		DumpDependencies: []string{descriptor1Name},
	}, mockSB, 0)
	// -> descriptor3:
	descriptor3 := test.NewMockDescriptor(&KVDescriptor{
		Name:            descriptor3Name,
		NBKeyPrefix:     prefixC,
		KeySelector:     prefixSelector(prefixC),
		ValueTypeName:   test.ArrayValueTypeName,
		DerivedValues:   test.ArrayValueDerBuilder,
		ModifyWithRecreate: func(key string, oldValue, newValue proto.Message, metadata Metadata) bool {
			if key == prefixC+baseValue3 {
				return true
			}
			return false
		},
		WithMetadata:     true,
		DumpDependencies: []string{descriptor2Name},
	}, mockSB, 0)

	// register all 3 descriptors with the scheduler
	scheduler.RegisterKVDescriptor(descriptor1)
	scheduler.RegisterKVDescriptor(descriptor2)
	scheduler.RegisterKVDescriptor(descriptor3)

	// get metadata map created for each descriptor
	metadataMap := scheduler.GetMetadataMap(descriptor1.Name)
	nameToInteger1, withMetadataMap := metadataMap.(test.NameToInteger)
	Expect(withMetadataMap).To(BeTrue())
	metadataMap = scheduler.GetMetadataMap(descriptor2.Name)
	nameToInteger2, withMetadataMap := metadataMap.(test.NameToInteger)
	Expect(withMetadataMap).To(BeTrue())
	metadataMap = scheduler.GetMetadataMap(descriptor3.Name)
	nameToInteger3, withMetadataMap := metadataMap.(test.NameToInteger)
	Expect(withMetadataMap).To(BeTrue())

	// run non-resync transaction against empty SB
	startTime := time.Now()
	schedulerTxn := scheduler.StartNBTransaction()
	schedulerTxn.SetValue(prefixB+baseValue2, test.NewLazyArrayValue("item1", "item2"))
	schedulerTxn.SetValue(prefixA+baseValue1, test.NewLazyArrayValue("item2"))
	schedulerTxn.SetValue(prefixC+baseValue3, test.NewLazyArrayValue("item1", "item2"))
	kvErrors, txnError := schedulerTxn.Commit(context.Background())
	stopTime := time.Now()
	Expect(txnError).ShouldNot(HaveOccurred())
	Expect(kvErrors).To(BeEmpty())

	// check the state of SB
	Expect(mockSB.GetKeysWithInvalidData()).To(BeEmpty())
	// -> base value 1
	value := mockSB.GetValue(prefixA + baseValue1)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewArrayValue("item2"))).To(BeTrue())
	Expect(value.Metadata).ToNot(BeNil())
	Expect(value.Metadata.(test.MetaWithInteger).GetInteger()).To(BeEquivalentTo(0))
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> item1 derived from base value was not added
	value = mockSB.GetValue(prefixA + baseValue1 + "/item1")
	Expect(value).To(BeNil())
	// -> item2 derived from base value 1
	value = mockSB.GetValue(prefixA + baseValue1 + "/item2")
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("item2"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> base value 2
	value = mockSB.GetValue(prefixB + baseValue2)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewArrayValue("item1", "item2"))).To(BeTrue())
	Expect(value.Metadata).ToNot(BeNil())
	Expect(value.Metadata.(test.MetaWithInteger).GetInteger()).To(BeEquivalentTo(0))
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> item1 derived from base value 2
	value = mockSB.GetValue(prefixB + baseValue2 + "/item1")
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("item1"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> item2 derived from base value 2 is pending
	value = mockSB.GetValue(prefixB + baseValue2 + "/item2")
	Expect(value).To(BeNil())
	// -> base value 3
	value = mockSB.GetValue(prefixC + baseValue3)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewArrayValue("item1", "item2"))).To(BeTrue())
	Expect(value.Metadata).ToNot(BeNil())
	Expect(value.Metadata.(test.MetaWithInteger).GetInteger()).To(BeEquivalentTo(0))
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> item1 derived from base value 3
	value = mockSB.GetValue(prefixC + baseValue3 + "/item1")
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("item1"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> item2 derived from base value 3
	value = mockSB.GetValue(prefixC + baseValue3 + "/item2")
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("item2"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	Expect(mockSB.GetValues(nil)).To(HaveLen(7))

	// check pending values
	pendingValues := scheduler.GetPendingValues(nil)
	checkValues(pendingValues, []KeyValuePair{
		{Key: prefixB + baseValue2 + "/item2", Value: test.NewStringValue("item2")},
	})

	// check metadata
	metadata, exists := nameToInteger1.LookupByName(baseValue1)
	Expect(exists).To(BeTrue())
	Expect(metadata.GetInteger()).To(BeEquivalentTo(0))
	metadata, exists = nameToInteger2.LookupByName(baseValue2)
	Expect(exists).To(BeTrue())
	Expect(metadata.GetInteger()).To(BeEquivalentTo(0))
	metadata, exists = nameToInteger3.LookupByName(baseValue3)
	Expect(exists).To(BeTrue())
	Expect(metadata.GetInteger()).To(BeEquivalentTo(0))

	// check operations executed in SB
	opHistory := mockSB.PopHistoryOfOps()
	Expect(opHistory).To(HaveLen(7))
	operation := opHistory[0]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor1Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixA + baseValue1))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[1]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor1Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixA + baseValue1 + "/item2"))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[2]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor2Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixB + baseValue2))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[3]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor2Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixB + baseValue2 + "/item1"))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[4]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor3Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixC + baseValue3))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[5]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor3Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixC + baseValue3 + "/item1"))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[6]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor3Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixC + baseValue3 + "/item2"))
	Expect(operation.Err).To(BeNil())

	// check transaction operations
	txnHistory := scheduler.getTransactionHistory(time.Time{}, time.Now())
	Expect(txnHistory).To(HaveLen(1))
	txn := txnHistory[0]
	Expect(txn.preRecord).To(BeFalse())
	Expect(txn.start.After(startTime)).To(BeTrue())
	Expect(txn.start.Before(txn.stop)).To(BeTrue())
	Expect(txn.stop.Before(stopTime)).To(BeTrue())
	Expect(txn.seqNum).To(BeEquivalentTo(0))
	Expect(txn.txnType).To(BeEquivalentTo(nbTransaction))
	Expect(txn.isFullResync).To(BeFalse())
	Expect(txn.isHalfwayResync).To(BeFalse())
	checkRecordedValues(txn.values, []recordedKVPair{
		{key: prefixA + baseValue1, value: utils.ProtoToString(test.NewArrayValue("item2")), origin: FromNB},
		{key: prefixB + baseValue2, value: utils.ProtoToString(test.NewArrayValue("item1", "item2")), origin: FromNB},
		{key: prefixC + baseValue3, value: utils.ProtoToString(test.NewArrayValue("item1", "item2")), origin: FromNB},
	})
	Expect(txn.preErrors).To(BeEmpty())

	txnOps := recordedTxnOps{
		{
			operation:  add,
			key:        prefixA + baseValue1,
			newValue:   utils.ProtoToString(test.NewArrayValue("item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  add,
			key:        prefixA + baseValue1 + "/item2",
			derived:    true,
			newValue:   utils.ProtoToString(test.NewStringValue("item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  add,
			key:        prefixB + baseValue2,
			newValue:   utils.ProtoToString(test.NewArrayValue("item1", "item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  add,
			key:        prefixB + baseValue2 + "/item1",
			derived:    true,
			newValue:   utils.ProtoToString(test.NewStringValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  add,
			key:        prefixB + baseValue2 + "/item2",
			derived:    true,
			newValue:   utils.ProtoToString(test.NewStringValue("item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			isPending:  true,
		},
		{
			operation:  add,
			key:        prefixC + baseValue3,
			newValue:   utils.ProtoToString(test.NewArrayValue("item1", "item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  add,
			key:        prefixC + baseValue3 + "/item1",
			derived:    true,
			newValue:   utils.ProtoToString(test.NewStringValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  add,
			key:        prefixC + baseValue3 + "/item2",
			derived:    true,
			newValue:   utils.ProtoToString(test.NewStringValue("item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
	}
	checkTxnOperations(txn.planned, txnOps)
	checkTxnOperations(txn.executed, txnOps)

	// check flag stats
	graphR := scheduler.graph.Read()
	errorStats := graphR.GetFlagStats(ErrorFlagName, nil)
	Expect(errorStats.TotalCount).To(BeEquivalentTo(0))
	pendingStats := graphR.GetFlagStats(PendingFlagName, nil)
	Expect(pendingStats.TotalCount).To(BeEquivalentTo(1))
	derivedStats := graphR.GetFlagStats(DerivedFlagName, nil)
	Expect(derivedStats.TotalCount).To(BeEquivalentTo(5))
	lastUpdateStats := graphR.GetFlagStats(LastUpdateFlagName, nil)
	Expect(lastUpdateStats.TotalCount).To(BeEquivalentTo(8))
	lastChangeStats := graphR.GetFlagStats(LastChangeFlagName, nil)
	Expect(lastChangeStats.TotalCount).To(BeEquivalentTo(3))
	descriptorStats := graphR.GetFlagStats(DescriptorFlagName, nil)
	Expect(descriptorStats.TotalCount).To(BeEquivalentTo(8))
	Expect(descriptorStats.PerValueCount).To(HaveKey(descriptor1Name))
	Expect(descriptorStats.PerValueCount[descriptor1Name]).To(BeEquivalentTo(2))
	Expect(descriptorStats.PerValueCount).To(HaveKey(descriptor2Name))
	Expect(descriptorStats.PerValueCount[descriptor2Name]).To(BeEquivalentTo(3))
	Expect(descriptorStats.PerValueCount).To(HaveKey(descriptor3Name))
	Expect(descriptorStats.PerValueCount[descriptor3Name]).To(BeEquivalentTo(3))
	originStats := graphR.GetFlagStats(OriginFlagName, nil)
	Expect(originStats.TotalCount).To(BeEquivalentTo(8))
	Expect(originStats.PerValueCount).To(HaveKey(FromNB.String()))
	Expect(originStats.PerValueCount[FromNB.String()]).To(BeEquivalentTo(8))
	graphR.Release()

	// run 2nd non-resync transaction against empty SB
	startTime = time.Now()
	schedulerTxn2 := scheduler.StartNBTransaction()
	schedulerTxn2.SetValue(prefixC+baseValue3, test.NewLazyArrayValue("item1"))
	schedulerTxn2.SetValue(prefixA+baseValue1, test.NewLazyArrayValue("item1"))
	kvErrors, txnError = schedulerTxn2.Commit(context.Background())
	stopTime = time.Now()
	Expect(txnError).ShouldNot(HaveOccurred())
	Expect(kvErrors).To(BeEmpty())

	// check the state of SB
	Expect(mockSB.GetKeysWithInvalidData()).To(BeEmpty())
	// -> base value 1
	value = mockSB.GetValue(prefixA + baseValue1)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewArrayValue("item1"))).To(BeTrue())
	Expect(value.Metadata).ToNot(BeNil())
	Expect(value.Metadata.(test.MetaWithInteger).GetInteger()).To(BeEquivalentTo(0))
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> item1 derived from base value was added
	value = mockSB.GetValue(prefixA + baseValue1 + "/item1")
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("item1"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> item2 derived from base value 1 was deleted
	value = mockSB.GetValue(prefixA + baseValue1 + "/item2")
	Expect(value).To(BeNil())
	// -> base value 2
	value = mockSB.GetValue(prefixB + baseValue2)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewArrayValue("item1", "item2"))).To(BeTrue())
	Expect(value.Metadata).ToNot(BeNil())
	Expect(value.Metadata.(test.MetaWithInteger).GetInteger()).To(BeEquivalentTo(0))
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> item1 derived from base value 2
	value = mockSB.GetValue(prefixB + baseValue2 + "/item1")
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("item1"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> item2 derived from base value 2 is no longer pending
	value = mockSB.GetValue(prefixB + baseValue2 + "/item2")
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("item2"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> base value 3
	value = mockSB.GetValue(prefixC + baseValue3)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewArrayValue("item1"))).To(BeTrue())
	Expect(value.Metadata).ToNot(BeNil())
	Expect(value.Metadata.(test.MetaWithInteger).GetInteger()).To(BeEquivalentTo(1))
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> item1 derived from base value 3
	value = mockSB.GetValue(prefixC + baseValue3 + "/item1")
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("item1"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> item2 derived from base value 3 was deleted
	value = mockSB.GetValue(prefixC + baseValue3 + "/item2")
	Expect(value).To(BeNil())

	// check pending values
	Expect(scheduler.GetPendingValues(nil)).To(BeEmpty())

	// check metadata
	metadata, exists = nameToInteger1.LookupByName(baseValue1)
	Expect(exists).To(BeTrue())
	Expect(metadata.GetInteger()).To(BeEquivalentTo(0))
	metadata, exists = nameToInteger2.LookupByName(baseValue2)
	Expect(exists).To(BeTrue())
	Expect(metadata.GetInteger()).To(BeEquivalentTo(0))
	metadata, exists = nameToInteger3.LookupByName(baseValue3)
	Expect(exists).To(BeTrue())
	Expect(metadata.GetInteger()).To(BeEquivalentTo(1)) // re-created

	// check operations executed in SB
	opHistory = mockSB.PopHistoryOfOps()
	Expect(opHistory).To(HaveLen(10))
	operation = opHistory[0]
	Expect(operation.OpType).To(Equal(test.Delete))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor3Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixC + baseValue3 + "/item1"))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[1]
	Expect(operation.OpType).To(Equal(test.Delete))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor3Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixC + baseValue3 + "/item2"))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[2]
	Expect(operation.OpType).To(Equal(test.Delete))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor3Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixC + baseValue3))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[3]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor3Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixC + baseValue3))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[4]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor3Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixC + baseValue3 + "/item1"))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[5]
	Expect(operation.OpType).To(Equal(test.Delete))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor1Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixA + baseValue1 + "/item2"))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[6]
	Expect(operation.OpType).To(Equal(test.Modify))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor1Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixA + baseValue1))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[7]
	Expect(operation.OpType).To(Equal(test.Update))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor2Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixB + baseValue2 + "/item1"))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[8]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor1Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixA + baseValue1 + "/item1"))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[9]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor2Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixB + baseValue2 + "/item2"))
	Expect(operation.Err).To(BeNil())

	// check transaction operations
	txnHistory = scheduler.getTransactionHistory(startTime, stopTime) // first txn not included
	Expect(txnHistory).To(HaveLen(1))
	txn = txnHistory[0]
	Expect(txn.preRecord).To(BeFalse())
	Expect(txn.start.After(startTime)).To(BeTrue())
	Expect(txn.start.Before(txn.stop)).To(BeTrue())
	Expect(txn.stop.Before(stopTime)).To(BeTrue())
	Expect(txn.seqNum).To(BeEquivalentTo(1))
	Expect(txn.txnType).To(BeEquivalentTo(nbTransaction))
	Expect(txn.isFullResync).To(BeFalse())
	Expect(txn.isHalfwayResync).To(BeFalse())
	checkRecordedValues(txn.values, []recordedKVPair{
		{key: prefixA + baseValue1, value: utils.ProtoToString(test.NewArrayValue("item1")), origin: FromNB},
		{key: prefixC + baseValue3, value: utils.ProtoToString(test.NewArrayValue("item1")), origin: FromNB},
	})
	Expect(txn.preErrors).To(BeEmpty())

	txnOps = recordedTxnOps{
		{
			operation:  del,
			key:        prefixC + baseValue3 + "/item1",
			derived:    true,
			prevValue:  utils.ProtoToString(test.NewStringValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  del,
			key:        prefixC + baseValue3 + "/item2",
			derived:    true,
			prevValue:  utils.ProtoToString(test.NewStringValue("item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  del,
			key:        prefixC + baseValue3,
			prevValue:  utils.ProtoToString(test.NewArrayValue("item1", "item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			isPending:  true,
		},
		{
			operation:  add,
			key:        prefixC + baseValue3,
			newValue:   utils.ProtoToString(test.NewArrayValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			wasPending: true,
		},
		{
			operation:  add,
			key:        prefixC + baseValue3 + "/item1",
			derived:    true,
			newValue:   utils.ProtoToString(test.NewStringValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  del,
			key:        prefixA + baseValue1 + "/item2",
			derived:    true,
			prevValue:  utils.ProtoToString(test.NewStringValue("item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  modify,
			key:        prefixA + baseValue1,
			prevValue:  utils.ProtoToString(test.NewArrayValue("item2")),
			newValue:   utils.ProtoToString(test.NewArrayValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  update,
			key:        prefixB + baseValue2 + "/item1",
			derived:    true,
			prevValue:  utils.ProtoToString(test.NewStringValue("item1")),
			newValue:   utils.ProtoToString(test.NewStringValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  add,
			key:        prefixA + baseValue1 + "/item1",
			derived:    true,
			newValue:   utils.ProtoToString(test.NewStringValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  add,
			key:        prefixB + baseValue2 + "/item2",
			derived:    true,
			prevValue:  utils.ProtoToString(test.NewStringValue("item2")),
			newValue:   utils.ProtoToString(test.NewStringValue("item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			wasPending: true,
		},
	}
	checkTxnOperations(txn.planned, txnOps)
	checkTxnOperations(txn.executed, txnOps)

	// check flag stats
	graphR = scheduler.graph.Read()
	errorStats = graphR.GetFlagStats(ErrorFlagName, nil)
	Expect(errorStats.TotalCount).To(BeEquivalentTo(0))
	pendingStats = graphR.GetFlagStats(PendingFlagName, nil)
	Expect(pendingStats.TotalCount).To(BeEquivalentTo(1))
	derivedStats = graphR.GetFlagStats(DerivedFlagName, nil)
	Expect(derivedStats.TotalCount).To(BeEquivalentTo(9))
	lastUpdateStats = graphR.GetFlagStats(LastUpdateFlagName, nil)
	Expect(lastUpdateStats.TotalCount).To(BeEquivalentTo(14))
	lastChangeStats = graphR.GetFlagStats(LastChangeFlagName, nil)
	Expect(lastChangeStats.TotalCount).To(BeEquivalentTo(5))
	descriptorStats = graphR.GetFlagStats(DescriptorFlagName, nil)
	Expect(descriptorStats.TotalCount).To(BeEquivalentTo(14))
	Expect(descriptorStats.PerValueCount).To(HaveKey(descriptor1Name))
	Expect(descriptorStats.PerValueCount[descriptor1Name]).To(BeEquivalentTo(4))
	Expect(descriptorStats.PerValueCount).To(HaveKey(descriptor2Name))
	Expect(descriptorStats.PerValueCount[descriptor2Name]).To(BeEquivalentTo(5))
	Expect(descriptorStats.PerValueCount).To(HaveKey(descriptor3Name))
	Expect(descriptorStats.PerValueCount[descriptor3Name]).To(BeEquivalentTo(5))
	originStats = graphR.GetFlagStats(OriginFlagName, nil)
	Expect(originStats.TotalCount).To(BeEquivalentTo(14))
	Expect(originStats.PerValueCount).To(HaveKey(FromNB.String()))
	Expect(originStats.PerValueCount[FromNB.String()]).To(BeEquivalentTo(14))
	graphR.Release()

	// close scheduler
	err = scheduler.Close()
	Expect(err).To(BeNil())
}

func TestDataChangeTransactionWithRevert(t *testing.T) {
	RegisterTestingT(t)

	// prepare KV Scheduler
	scheduler := NewPlugin(UseDeps(func(deps *Deps) {
		deps.HTTPHandlers = nil
	}))
	err := scheduler.Init()
	Expect(err).To(BeNil())

	// prepare mocks
	mockSB := test.NewMockSouthbound()
	// -> descriptor1:
	descriptor1 := test.NewMockDescriptor(&KVDescriptor{
		Name:            descriptor1Name,
		NBKeyPrefix:     prefixA,
		KeySelector:     prefixSelector(prefixA),
		ValueTypeName:   test.ArrayValueTypeName,
		DerivedValues:   test.ArrayValueDerBuilder,
		WithMetadata:    true,
	}, mockSB, 0)
	// -> descriptor2:
	descriptor2 := test.NewMockDescriptor(&KVDescriptor{
		Name:            descriptor2Name,
		NBKeyPrefix:     prefixB,
		KeySelector:     prefixSelector(prefixB),
		ValueTypeName:   test.ArrayValueTypeName,
		DerivedValues:   test.ArrayValueDerBuilder,
		Dependencies: func(key string, value proto.Message) []Dependency {
			if key == prefixB+baseValue2+"/item1" {
				depKey := prefixA + baseValue1
				return []Dependency{
					{Label: depKey, Key: depKey},
				}
			}
			if key == prefixB+baseValue2+"/item2" {
				depKey := prefixA + baseValue1 + "/item1"
				return []Dependency{
					{Label: depKey, Key: depKey},
				}
			}
			return nil
		},
		WithMetadata:     true,
		DumpDependencies: []string{descriptor1Name},
	}, mockSB, 0)
	// -> descriptor3:
	descriptor3 := test.NewMockDescriptor(&KVDescriptor{
		Name:            descriptor3Name,
		NBKeyPrefix:     prefixC,
		KeySelector:     prefixSelector(prefixC),
		ValueTypeName:   test.ArrayValueTypeName,
		DerivedValues:   test.ArrayValueDerBuilder,
		ModifyWithRecreate: func(key string, oldValue, newValue proto.Message, metadata Metadata) bool {
			if key == prefixC+baseValue3 {
				return true
			}
			return false
		},
		WithMetadata:     true,
		DumpDependencies: []string{descriptor2Name},
	}, mockSB, 0)

	// register all 3 descriptors with the scheduler
	scheduler.RegisterKVDescriptor(descriptor1)
	scheduler.RegisterKVDescriptor(descriptor2)
	scheduler.RegisterKVDescriptor(descriptor3)

	// get metadata map created for each descriptor
	metadataMap := scheduler.GetMetadataMap(descriptor1.Name)
	nameToInteger1, withMetadataMap := metadataMap.(test.NameToInteger)
	Expect(withMetadataMap).To(BeTrue())
	metadataMap = scheduler.GetMetadataMap(descriptor2.Name)
	nameToInteger2, withMetadataMap := metadataMap.(test.NameToInteger)
	Expect(withMetadataMap).To(BeTrue())
	metadataMap = scheduler.GetMetadataMap(descriptor3.Name)
	nameToInteger3, withMetadataMap := metadataMap.(test.NameToInteger)
	Expect(withMetadataMap).To(BeTrue())

	// run 1st non-resync transaction against empty SB
	schedulerTxn := scheduler.StartNBTransaction()
	schedulerTxn.SetValue(prefixB+baseValue2, test.NewLazyArrayValue("item1", "item2"))
	schedulerTxn.SetValue(prefixA+baseValue1, test.NewLazyArrayValue("item2"))
	schedulerTxn.SetValue(prefixC+baseValue3, test.NewLazyArrayValue("item1", "item2"))
	kvErrors, txnError := schedulerTxn.Commit(context.Background())
	Expect(txnError).ShouldNot(HaveOccurred())
	Expect(kvErrors).To(BeEmpty())
	mockSB.PopHistoryOfOps()

	// plan error before 2nd txn
	failedModifyClb := func() {
		mockSB.SetValue(prefixA+baseValue1, test.NewArrayValue(),
			&test.OnlyInteger{Integer: 0}, FromNB, false)
	}
	mockSB.PlanError(prefixA+baseValue1, errors.New("failed to modify value"), failedModifyClb)

	// subscribe to receive notifications about errors
	errorChan := make(chan KeyWithError, 5)
	scheduler.SubscribeForErrors(errorChan, prefixSelector(prefixA))

	// run 2nd non-resync transaction against empty SB that will fail and will be reverted
	startTime := time.Now()
	schedulerTxn2 := scheduler.StartNBTransaction()
	schedulerTxn2.SetValue(prefixC+baseValue3, test.NewLazyArrayValue("item1"))
	schedulerTxn2.SetValue(prefixA+baseValue1, test.NewLazyArrayValue("item1"))
	kvErrors, txnError = schedulerTxn2.Commit(WithRevert(context.Background()))
	stopTime := time.Now()
	Expect(txnError).ShouldNot(HaveOccurred())
	Expect(kvErrors).To(HaveLen(1))
	Expect(kvErrors[0].Key).To(BeEquivalentTo(prefixA + baseValue1))
	Expect(kvErrors[0].Error.Error()).To(BeEquivalentTo("failed to modify value"))

	// receive the error notification
	var errorNotif KeyWithError
	Eventually(errorChan, time.Second).Should(Receive(&errorNotif))
	Expect(errorNotif.Key).To(Equal(prefixA + baseValue1))
	Expect(errorNotif.Error).ToNot(BeNil())
	Expect(errorNotif.Error.Error()).To(BeEquivalentTo("failed to modify value"))

	// check the state of SB
	Expect(mockSB.GetKeysWithInvalidData()).To(BeEmpty())
	// -> base value 1
	value := mockSB.GetValue(prefixA + baseValue1)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewArrayValue("item2"))).To(BeTrue())
	Expect(value.Metadata).ToNot(BeNil())
	Expect(value.Metadata.(test.MetaWithInteger).GetInteger()).To(BeEquivalentTo(0))
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> item1 derived from base value was NOT added
	value = mockSB.GetValue(prefixA + baseValue1 + "/item1")
	Expect(value).To(BeNil())
	// -> item2 derived from base value 1 was first deleted by then added back
	value = mockSB.GetValue(prefixA + baseValue1 + "/item2")
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("item2"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> base value 2
	value = mockSB.GetValue(prefixB + baseValue2)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewArrayValue("item1", "item2"))).To(BeTrue())
	Expect(value.Metadata).ToNot(BeNil())
	Expect(value.Metadata.(test.MetaWithInteger).GetInteger()).To(BeEquivalentTo(0))
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> item1 derived from base value 2
	value = mockSB.GetValue(prefixB + baseValue2 + "/item1")
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("item1"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> item2 derived from base value 2 is still pending
	value = mockSB.GetValue(prefixB + baseValue2 + "/item2")
	Expect(value).To(BeNil())
	// -> base value 3 was reverted back to state after 1st txn
	value = mockSB.GetValue(prefixC + baseValue3)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewArrayValue("item1", "item2"))).To(BeTrue())
	Expect(value.Metadata).ToNot(BeNil())
	Expect(value.Metadata.(test.MetaWithInteger).GetInteger()).To(BeEquivalentTo(2))
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> item1 derived from base value 3
	value = mockSB.GetValue(prefixC + baseValue3 + "/item1")
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("item1"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> item2 derived from base value 3
	value = mockSB.GetValue(prefixC + baseValue3 + "/item2")
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("item2"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))

	// check metadata
	metadata, exists := nameToInteger1.LookupByName(baseValue1)
	Expect(exists).To(BeTrue())
	Expect(metadata.GetInteger()).To(BeEquivalentTo(0))
	metadata, exists = nameToInteger2.LookupByName(baseValue2)
	Expect(exists).To(BeTrue())
	Expect(metadata.GetInteger()).To(BeEquivalentTo(0))
	metadata, exists = nameToInteger3.LookupByName(baseValue3)
	Expect(exists).To(BeTrue())
	Expect(metadata.GetInteger()).To(BeEquivalentTo(2)) // re-created twice

	// check operations executed in SB during 2nd txn
	opHistory := mockSB.PopHistoryOfOps()
	Expect(opHistory).To(HaveLen(16))
	operation := opHistory[0]
	Expect(operation.OpType).To(Equal(test.Delete))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor3Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixC + baseValue3 + "/item1"))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[1]
	Expect(operation.OpType).To(Equal(test.Delete))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor3Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixC + baseValue3 + "/item2"))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[2]
	Expect(operation.OpType).To(Equal(test.Delete))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor3Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixC + baseValue3))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[3]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor3Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixC + baseValue3))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[4]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor3Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixC + baseValue3 + "/item1"))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[5]
	Expect(operation.OpType).To(Equal(test.Delete))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor1Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixA + baseValue1 + "/item2"))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[6]
	Expect(operation.OpType).To(Equal(test.Modify))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor1Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixA + baseValue1))
	Expect(operation.Err).ToNot(BeNil())
	Expect(operation.Err.Error()).To(BeEquivalentTo("failed to modify value"))
	// reverting:
	operation = opHistory[7] // refresh failed value
	Expect(operation.OpType).To(Equal(test.Dump))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor1Name))
	checkValuesForCorrelation(operation.CorrelateDump, []KVWithMetadata{
		{
			Key:      prefixA + baseValue1,
			Value:    test.NewArrayValue("item1"),
			Metadata: &test.OnlyInteger{Integer: 0},
			Origin:   FromNB,
		},
	})
	operation = opHistory[8]
	Expect(operation.OpType).To(Equal(test.Modify))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor1Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixA + baseValue1))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[9]
	Expect(operation.OpType).To(Equal(test.Update))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor2Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixB + baseValue2 + "/item1"))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[10]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor1Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixA + baseValue1 + "/item2"))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[11]
	Expect(operation.OpType).To(Equal(test.Delete))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor3Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixC + baseValue3 + "/item1"))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[12]
	Expect(operation.OpType).To(Equal(test.Delete))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor3Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixC + baseValue3))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[13]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor3Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixC + baseValue3))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[14]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor3Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixC + baseValue3 + "/item1"))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[15]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor3Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixC + baseValue3 + "/item2"))
	Expect(operation.Err).To(BeNil())

	// check transaction operations
	txnHistory := scheduler.getTransactionHistory(startTime, time.Now())
	Expect(txnHistory).To(HaveLen(1))
	txn := txnHistory[0]
	Expect(txn.preRecord).To(BeFalse())
	Expect(txn.start.After(startTime)).To(BeTrue())
	Expect(txn.start.Before(txn.stop)).To(BeTrue())
	Expect(txn.stop.Before(stopTime)).To(BeTrue())
	Expect(txn.seqNum).To(BeEquivalentTo(1))
	Expect(txn.txnType).To(BeEquivalentTo(nbTransaction))
	Expect(txn.isFullResync).To(BeFalse())
	Expect(txn.isHalfwayResync).To(BeFalse())
	checkRecordedValues(txn.values, []recordedKVPair{
		{key: prefixA + baseValue1, value: utils.ProtoToString(test.NewArrayValue("item1")), origin: FromNB},
		{key: prefixC + baseValue3, value: utils.ProtoToString(test.NewArrayValue("item1")), origin: FromNB},
	})
	Expect(txn.preErrors).To(BeEmpty())

	// planned operations
	txnOps := recordedTxnOps{
		{
			operation:  del,
			key:        prefixC + baseValue3 + "/item1",
			derived:    true,
			prevValue:  utils.ProtoToString(test.NewStringValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  del,
			key:        prefixC + baseValue3 + "/item2",
			derived:    true,
			prevValue:  utils.ProtoToString(test.NewStringValue("item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  del,
			key:        prefixC + baseValue3,
			prevValue:  utils.ProtoToString(test.NewArrayValue("item1", "item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			isPending:  true,
		},
		{
			operation:  add,
			key:        prefixC + baseValue3,
			newValue:   utils.ProtoToString(test.NewArrayValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			wasPending: true,
		},
		{
			operation:  add,
			key:        prefixC + baseValue3 + "/item1",
			derived:    true,
			newValue:   utils.ProtoToString(test.NewStringValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  del,
			key:        prefixA + baseValue1 + "/item2",
			derived:    true,
			prevValue:  utils.ProtoToString(test.NewStringValue("item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  modify,
			key:        prefixA + baseValue1,
			prevValue:  utils.ProtoToString(test.NewArrayValue("item2")),
			newValue:   utils.ProtoToString(test.NewArrayValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  update,
			key:        prefixB + baseValue2 + "/item1",
			derived:    true,
			prevValue:  utils.ProtoToString(test.NewStringValue("item1")),
			newValue:   utils.ProtoToString(test.NewStringValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  add,
			key:        prefixA + baseValue1 + "/item1",
			derived:    true,
			newValue:   utils.ProtoToString(test.NewStringValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  add,
			key:        prefixB + baseValue2 + "/item2",
			derived:    true,
			prevValue:  utils.ProtoToString(test.NewStringValue("item2")),
			newValue:   utils.ProtoToString(test.NewStringValue("item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			wasPending: true,
		},
	}
	checkTxnOperations(txn.planned, txnOps)

	// executed operations
	txnOps = recordedTxnOps{
		{
			operation:  del,
			key:        prefixC + baseValue3 + "/item1",
			derived:    true,
			prevValue:  utils.ProtoToString(test.NewStringValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  del,
			key:        prefixC + baseValue3 + "/item2",
			derived:    true,
			prevValue:  utils.ProtoToString(test.NewStringValue("item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  del,
			key:        prefixC + baseValue3,
			prevValue:  utils.ProtoToString(test.NewArrayValue("item1", "item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			isPending:  true,
		},
		{
			operation:  add,
			key:        prefixC + baseValue3,
			newValue:   utils.ProtoToString(test.NewArrayValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			wasPending: true,
		},
		{
			operation:  add,
			key:        prefixC + baseValue3 + "/item1",
			derived:    true,
			newValue:   utils.ProtoToString(test.NewStringValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  del,
			key:        prefixA + baseValue1 + "/item2",
			derived:    true,
			prevValue:  utils.ProtoToString(test.NewStringValue("item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  modify,
			key:        prefixA + baseValue1,
			prevValue:  utils.ProtoToString(test.NewArrayValue("item2")),
			newValue:   utils.ProtoToString(test.NewArrayValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			newErr:     errors.New("failed to modify value"),
		},
		// reverting:
		{
			operation:  modify,
			key:        prefixA + baseValue1,
			prevValue:  utils.ProtoToString(test.NewArrayValue()),
			newValue:   utils.ProtoToString(test.NewArrayValue("item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			prevErr:    errors.New("failed to modify value"),
			isRevert:   true,
		},
		{
			operation:  update,
			key:        prefixB + baseValue2 + "/item1",
			derived:    true,
			prevValue:  utils.ProtoToString(test.NewStringValue("item1")),
			newValue:   utils.ProtoToString(test.NewStringValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			isRevert:   true,
		},
		{
			operation:  add,
			key:        prefixA + baseValue1 + "/item2",
			derived:    true,
			newValue:   utils.ProtoToString(test.NewStringValue("item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			isRevert:   true,
		},
		{
			operation:  del,
			key:        prefixC + baseValue3 + "/item1",
			derived:    true,
			prevValue:  utils.ProtoToString(test.NewStringValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			isRevert:   true,
		},
		{
			operation:  del,
			key:        prefixC + baseValue3,
			prevValue:  utils.ProtoToString(test.NewArrayValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			isPending:  true,
			isRevert:   true,
		},
		{
			operation:  add,
			key:        prefixC + baseValue3,
			newValue:   utils.ProtoToString(test.NewArrayValue("item1", "item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			isRevert:   true,
			wasPending: true,
		},
		{
			operation:  add,
			key:        prefixC + baseValue3 + "/item1",
			derived:    true,
			newValue:   utils.ProtoToString(test.NewStringValue("item1")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			isRevert:   true,
		},
		{
			operation:  add,
			key:        prefixC + baseValue3 + "/item2",
			derived:    true,
			newValue:   utils.ProtoToString(test.NewStringValue("item2")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			isRevert:   true,
		},
	}
	checkTxnOperations(txn.executed, txnOps)

	// check flag stats
	graphR := scheduler.graph.Read()
	errorStats := graphR.GetFlagStats(ErrorFlagName, nil)
	Expect(errorStats.TotalCount).To(BeEquivalentTo(1))
	pendingStats := graphR.GetFlagStats(PendingFlagName, nil)
	Expect(pendingStats.TotalCount).To(BeEquivalentTo(1))
	derivedStats := graphR.GetFlagStats(DerivedFlagName, nil)
	Expect(derivedStats.TotalCount).To(BeEquivalentTo(10))
	lastUpdateStats := graphR.GetFlagStats(LastUpdateFlagName, nil)
	Expect(lastUpdateStats.TotalCount).To(BeEquivalentTo(17))
	lastChangeStats := graphR.GetFlagStats(LastChangeFlagName, nil)
	Expect(lastChangeStats.TotalCount).To(BeEquivalentTo(7))
	descriptorStats := graphR.GetFlagStats(DescriptorFlagName, nil)
	Expect(descriptorStats.TotalCount).To(BeEquivalentTo(17))
	Expect(descriptorStats.PerValueCount).To(HaveKey(descriptor1Name))
	Expect(descriptorStats.PerValueCount[descriptor1Name]).To(BeEquivalentTo(5))
	Expect(descriptorStats.PerValueCount).To(HaveKey(descriptor2Name))
	Expect(descriptorStats.PerValueCount[descriptor2Name]).To(BeEquivalentTo(4))
	Expect(descriptorStats.PerValueCount).To(HaveKey(descriptor3Name))
	Expect(descriptorStats.PerValueCount[descriptor3Name]).To(BeEquivalentTo(8))
	originStats := graphR.GetFlagStats(OriginFlagName, nil)
	Expect(originStats.TotalCount).To(BeEquivalentTo(17))
	Expect(originStats.PerValueCount).To(HaveKey(FromNB.String()))
	Expect(originStats.PerValueCount[FromNB.String()]).To(BeEquivalentTo(17))
	graphR.Release()

	// close scheduler
	err = scheduler.Close()
	Expect(err).To(BeNil())
}

func TestDependencyCycles(t *testing.T) {
	RegisterTestingT(t)

	// prepare KV Scheduler
	scheduler := NewPlugin(UseDeps(func(deps *Deps) {
		deps.HTTPHandlers = nil
	}))
	err := scheduler.Init()
	Expect(err).To(BeNil())

	// prepare mocks
	mockSB := test.NewMockSouthbound()
	// -> descriptor:
	descriptor := test.NewMockDescriptor(&KVDescriptor{
		Name:            descriptor1Name,
		KeySelector:     prefixSelector(prefixA),
		NBKeyPrefix:     prefixA,
		ValueTypeName:   test.StringValueTypeName,
		ValueComparator: test.StringValueComparator,
		Dependencies: func(key string, value proto.Message) []Dependency {
			if key == prefixA+baseValue1 {
				depKey := prefixA + baseValue2
				return []Dependency{
					{Label: depKey, Key: depKey},
				}
			}
			if key == prefixA+baseValue2 {
				depKey := prefixA + baseValue3
				return []Dependency{
					{Label: depKey, Key: depKey},
				}
			}
			if key == prefixA+baseValue3 {
				depKey1 := prefixA + baseValue1
				depKey2 := prefixA + baseValue4
				return []Dependency{
					{Label: depKey1, Key: depKey1},
					{Label: depKey2, Key: depKey2},
				}
			}
			return nil
		},
		WithMetadata: false,
	}, mockSB, 0, test.WithoutDump)

	// register the descriptor
	scheduler.RegisterKVDescriptor(descriptor)

	// run non-resync transaction against empty SB
	startTime := time.Now()
	schedulerTxn := scheduler.StartNBTransaction()
	schedulerTxn.SetValue(prefixA+baseValue1, test.NewLazyStringValue("base-value1-data"))
	schedulerTxn.SetValue(prefixA+baseValue2, test.NewLazyStringValue("base-value2-data"))
	schedulerTxn.SetValue(prefixA+baseValue3, test.NewLazyStringValue("base-value3-data"))
	kvErrors, txnError := schedulerTxn.Commit(context.Background())
	stopTime := time.Now()
	Expect(txnError).ShouldNot(HaveOccurred())
	Expect(kvErrors).To(BeEmpty())

	// check the state of SB
	Expect(mockSB.GetKeysWithInvalidData()).To(BeEmpty())
	Expect(mockSB.GetValues(nil)).To(HaveLen(0))

	// check pending values
	pendingValues := scheduler.GetPendingValues(nil)
	checkValues(pendingValues, []KeyValuePair{
		{Key: prefixA + baseValue1, Value: test.NewStringValue("base-value1-data")},
		{Key: prefixA + baseValue2, Value: test.NewStringValue("base-value2-data")},
		{Key: prefixA + baseValue3, Value: test.NewStringValue("base-value3-data")},
	})

	// check operations executed in SB
	opHistory := mockSB.PopHistoryOfOps()
	Expect(opHistory).To(HaveLen(0))

	// check transaction operations
	txnHistory := scheduler.getTransactionHistory(time.Time{}, time.Now())
	Expect(txnHistory).To(HaveLen(1))
	txn := txnHistory[0]
	Expect(txn.preRecord).To(BeFalse())
	Expect(txn.start.After(startTime)).To(BeTrue())
	Expect(txn.start.Before(txn.stop)).To(BeTrue())
	Expect(txn.stop.Before(stopTime)).To(BeTrue())
	Expect(txn.seqNum).To(BeEquivalentTo(0))
	Expect(txn.txnType).To(BeEquivalentTo(nbTransaction))
	Expect(txn.isFullResync).To(BeFalse())
	Expect(txn.isHalfwayResync).To(BeFalse())
	checkRecordedValues(txn.values, []recordedKVPair{
		{key: prefixA + baseValue1, value: utils.ProtoToString(test.NewStringValue("base-value1-data")), origin: FromNB},
		{key: prefixA + baseValue2, value: utils.ProtoToString(test.NewStringValue("base-value2-data")), origin: FromNB},
		{key: prefixA + baseValue3, value: utils.ProtoToString(test.NewStringValue("base-value3-data")), origin: FromNB},
	})
	Expect(txn.preErrors).To(BeEmpty())

	txnOps := recordedTxnOps{
		{
			operation:  add,
			key:        prefixA + baseValue1,
			newValue:   utils.ProtoToString(test.NewStringValue("base-value1-data")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			isPending:  true,
		},
		{
			operation:  add,
			key:        prefixA + baseValue3,
			newValue:   utils.ProtoToString(test.NewStringValue("base-value3-data")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			isPending:  true,
		},
		{
			operation:  add,
			key:        prefixA + baseValue2,
			newValue:   utils.ProtoToString(test.NewStringValue("base-value2-data")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			isPending:  true,
		},
	}
	checkTxnOperations(txn.planned, txnOps)
	checkTxnOperations(txn.executed, txnOps)

	// check flag stats
	graphR := scheduler.graph.Read()
	errorStats := graphR.GetFlagStats(ErrorFlagName, nil)
	Expect(errorStats.TotalCount).To(BeEquivalentTo(0))
	pendingStats := graphR.GetFlagStats(PendingFlagName, nil)
	Expect(pendingStats.TotalCount).To(BeEquivalentTo(3))
	derivedStats := graphR.GetFlagStats(DerivedFlagName, nil)
	Expect(derivedStats.TotalCount).To(BeEquivalentTo(0))
	lastUpdateStats := graphR.GetFlagStats(LastUpdateFlagName, nil)
	Expect(lastUpdateStats.TotalCount).To(BeEquivalentTo(3))
	lastChangeStats := graphR.GetFlagStats(LastChangeFlagName, nil)
	Expect(lastChangeStats.TotalCount).To(BeEquivalentTo(3))
	descriptorStats := graphR.GetFlagStats(DescriptorFlagName, nil)
	Expect(descriptorStats.TotalCount).To(BeEquivalentTo(3))
	Expect(descriptorStats.PerValueCount).To(HaveKey(descriptor1Name))
	Expect(descriptorStats.PerValueCount[descriptor1Name]).To(BeEquivalentTo(3))
	originStats := graphR.GetFlagStats(OriginFlagName, nil)
	Expect(originStats.TotalCount).To(BeEquivalentTo(3))
	Expect(originStats.PerValueCount).To(HaveKey(FromNB.String()))
	Expect(originStats.PerValueCount[FromNB.String()]).To(BeEquivalentTo(3))
	graphR.Release()

	// run second transaction that will make the cycle of values ready to be added
	startTime = time.Now()
	schedulerTxn = scheduler.StartNBTransaction()
	schedulerTxn.SetValue(prefixA+baseValue4, test.NewLazyStringValue("base-value4-data"))
	kvErrors, txnError = schedulerTxn.Commit(context.Background())
	stopTime = time.Now()
	Expect(txnError).ShouldNot(HaveOccurred())
	Expect(kvErrors).To(BeEmpty())

	// check the state of SB
	Expect(mockSB.GetKeysWithInvalidData()).To(BeEmpty())
	Expect(mockSB.GetValues(nil)).To(HaveLen(4))
	// -> base value 1
	value := mockSB.GetValue(prefixA + baseValue1)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("base-value1-data"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> base value 2
	value = mockSB.GetValue(prefixA + baseValue2)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("base-value2-data"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> base value 3
	value = mockSB.GetValue(prefixA + baseValue3)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("base-value3-data"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> base value 4
	value = mockSB.GetValue(prefixA + baseValue4)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("base-value4-data"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))

	// check pending values
	pendingValues = scheduler.GetPendingValues(nil)
	Expect(pendingValues).To(BeEmpty())

	// check operations executed in SB
	opHistory = mockSB.PopHistoryOfOps()
	Expect(opHistory).To(HaveLen(4))
	operation := opHistory[0]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor1Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixA + baseValue4))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[1]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor1Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixA + baseValue3))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[2]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor1Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixA + baseValue2))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[3]
	Expect(operation.OpType).To(Equal(test.Add))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor1Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixA + baseValue1))
	Expect(operation.Err).To(BeNil())

	// check transaction operations
	txnHistory = scheduler.getTransactionHistory(time.Time{}, time.Now())
	Expect(txnHistory).To(HaveLen(2))
	txn = txnHistory[1]
	Expect(txn.preRecord).To(BeFalse())
	Expect(txn.start.After(startTime)).To(BeTrue())
	Expect(txn.start.Before(txn.stop)).To(BeTrue())
	Expect(txn.stop.Before(stopTime)).To(BeTrue())
	Expect(txn.seqNum).To(BeEquivalentTo(1))
	Expect(txn.txnType).To(BeEquivalentTo(nbTransaction))
	Expect(txn.isFullResync).To(BeFalse())
	Expect(txn.isHalfwayResync).To(BeFalse())
	checkRecordedValues(txn.values, []recordedKVPair{
		{key: prefixA + baseValue4, value: utils.ProtoToString(test.NewStringValue("base-value4-data")), origin: FromNB},
	})
	Expect(txn.preErrors).To(BeEmpty())

	txnOps = recordedTxnOps{
		{
			operation:  add,
			key:        prefixA + baseValue4,
			newValue:   utils.ProtoToString(test.NewStringValue("base-value4-data")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
		{
			operation:  add,
			key:        prefixA + baseValue3,
			prevValue:  utils.ProtoToString(test.NewStringValue("base-value3-data")),
			newValue:   utils.ProtoToString(test.NewStringValue("base-value3-data")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			wasPending: true,
		},
		{
			operation:  add,
			key:        prefixA + baseValue2,
			prevValue:  utils.ProtoToString(test.NewStringValue("base-value2-data")),
			newValue:   utils.ProtoToString(test.NewStringValue("base-value2-data")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			wasPending: true,
		},
		{
			operation:  add,
			key:        prefixA + baseValue1,
			prevValue:  utils.ProtoToString(test.NewStringValue("base-value1-data")),
			newValue:   utils.ProtoToString(test.NewStringValue("base-value1-data")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			wasPending: true,
		},
	}
	checkTxnOperations(txn.planned, txnOps)
	checkTxnOperations(txn.executed, txnOps)

	// check flag stats
	graphR = scheduler.graph.Read()
	errorStats = graphR.GetFlagStats(ErrorFlagName, nil)
	Expect(errorStats.TotalCount).To(BeEquivalentTo(0))
	pendingStats = graphR.GetFlagStats(PendingFlagName, nil)
	Expect(pendingStats.TotalCount).To(BeEquivalentTo(3))
	derivedStats = graphR.GetFlagStats(DerivedFlagName, nil)
	Expect(derivedStats.TotalCount).To(BeEquivalentTo(0))
	lastUpdateStats = graphR.GetFlagStats(LastUpdateFlagName, nil)
	Expect(lastUpdateStats.TotalCount).To(BeEquivalentTo(7))
	lastChangeStats = graphR.GetFlagStats(LastChangeFlagName, nil)
	Expect(lastChangeStats.TotalCount).To(BeEquivalentTo(7))
	descriptorStats = graphR.GetFlagStats(DescriptorFlagName, nil)
	Expect(descriptorStats.TotalCount).To(BeEquivalentTo(7))
	Expect(descriptorStats.PerValueCount).To(HaveKey(descriptor1Name))
	Expect(descriptorStats.PerValueCount[descriptor1Name]).To(BeEquivalentTo(7))
	originStats = graphR.GetFlagStats(OriginFlagName, nil)
	Expect(originStats.TotalCount).To(BeEquivalentTo(7))
	Expect(originStats.PerValueCount).To(HaveKey(FromNB.String()))
	Expect(originStats.PerValueCount[FromNB.String()]).To(BeEquivalentTo(7))
	graphR.Release()

	// plan error before 3rd txn
	mockSB.PlanError(prefixA+baseValue2, errors.New("failed to remove the value"), nil)

	// run third transaction that will break the cycle even though the delete operation will fail
	startTime = time.Now()
	schedulerTxn = scheduler.StartNBTransaction()
	schedulerTxn.SetValue(prefixA+baseValue2, nil)
	kvErrors, txnError = schedulerTxn.Commit(context.Background())
	stopTime = time.Now()
	Expect(txnError).ShouldNot(HaveOccurred())
	Expect(kvErrors).To(HaveLen(1))
	Expect(kvErrors[0].Key).To(BeEquivalentTo(prefixA + baseValue2))
	Expect(kvErrors[0].Error.Error()).To(BeEquivalentTo("failed to remove the value"))

	// check the state of SB
	Expect(mockSB.GetKeysWithInvalidData()).To(BeEmpty())
	Expect(mockSB.GetValues(nil)).To(HaveLen(2))
	// -> base value 1 - pending
	value = mockSB.GetValue(prefixA + baseValue1)
	Expect(value).To(BeNil())
	// -> base value 2 - failed to remove
	value = mockSB.GetValue(prefixA + baseValue2)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("base-value2-data"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> base value 3 - pending
	value = mockSB.GetValue(prefixA + baseValue3)
	Expect(value).To(BeNil())
	// -> base value 4
	value = mockSB.GetValue(prefixA + baseValue4)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("base-value4-data"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))

	// check pending values
	pendingValues = scheduler.GetPendingValues(nil)
	checkValues(pendingValues, []KeyValuePair{
		{Key: prefixA + baseValue1, Value: test.NewStringValue("base-value1-data")},
		{Key: prefixA + baseValue3, Value: test.NewStringValue("base-value3-data")},
	})

	// check operations executed in SB
	opHistory = mockSB.PopHistoryOfOps()
	Expect(opHistory).To(HaveLen(3))
	operation = opHistory[0]
	Expect(operation.OpType).To(Equal(test.Delete))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor1Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixA + baseValue3))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[1]
	Expect(operation.OpType).To(Equal(test.Delete))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor1Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixA + baseValue1))
	Expect(operation.Err).To(BeNil())
	operation = opHistory[2]
	Expect(operation.OpType).To(Equal(test.Delete))
	Expect(operation.Descriptor).To(BeEquivalentTo(descriptor1Name))
	Expect(operation.Key).To(BeEquivalentTo(prefixA + baseValue2))
	Expect(operation.Err.Error()).To(BeEquivalentTo("failed to remove the value"))

	// check transaction operations
	txnHistory = scheduler.getTransactionHistory(time.Time{}, time.Now())
	Expect(txnHistory).To(HaveLen(3))
	txn = txnHistory[2]
	Expect(txn.preRecord).To(BeFalse())
	Expect(txn.start.After(startTime)).To(BeTrue())
	Expect(txn.start.Before(txn.stop)).To(BeTrue())
	Expect(txn.stop.Before(stopTime)).To(BeTrue())
	Expect(txn.seqNum).To(BeEquivalentTo(2))
	Expect(txn.txnType).To(BeEquivalentTo(nbTransaction))
	Expect(txn.isFullResync).To(BeFalse())
	Expect(txn.isHalfwayResync).To(BeFalse())
	checkRecordedValues(txn.values, []recordedKVPair{
		{key: prefixA + baseValue2, value: utils.ProtoToString(nil), origin: FromNB},
	})
	Expect(txn.preErrors).To(BeEmpty())

	txnOps = recordedTxnOps{
		{
			operation:  del,
			key:        prefixA + baseValue3,
			prevValue:  utils.ProtoToString(test.NewStringValue("base-value3-data")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			isPending:  true,
		},
		{
			operation:  del,
			key:        prefixA + baseValue1,
			prevValue:  utils.ProtoToString(test.NewStringValue("base-value1-data")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
			isPending:  true,
		},
		{
			operation:  del,
			key:        prefixA + baseValue2,
			prevValue:  utils.ProtoToString(test.NewStringValue("base-value2-data")),
			prevOrigin: FromNB,
			newOrigin:  FromNB,
		},
	}
	checkTxnOperations(txn.planned, txnOps)
	txnOps[2].newErr = errors.New("failed to remove the value")
	checkTxnOperations(txn.executed, txnOps)

	// check flag stats
	graphR = scheduler.graph.Read()
	errorStats = graphR.GetFlagStats(ErrorFlagName, nil)
	Expect(errorStats.TotalCount).To(BeEquivalentTo(1))
	pendingStats = graphR.GetFlagStats(PendingFlagName, nil)
	Expect(pendingStats.TotalCount).To(BeEquivalentTo(5))
	derivedStats = graphR.GetFlagStats(DerivedFlagName, nil)
	Expect(derivedStats.TotalCount).To(BeEquivalentTo(0))
	lastUpdateStats = graphR.GetFlagStats(LastUpdateFlagName, nil)
	Expect(lastUpdateStats.TotalCount).To(BeEquivalentTo(10))
	lastChangeStats = graphR.GetFlagStats(LastChangeFlagName, nil)
	Expect(lastChangeStats.TotalCount).To(BeEquivalentTo(10))
	descriptorStats = graphR.GetFlagStats(DescriptorFlagName, nil)
	Expect(descriptorStats.TotalCount).To(BeEquivalentTo(10))
	Expect(descriptorStats.PerValueCount).To(HaveKey(descriptor1Name))
	Expect(descriptorStats.PerValueCount[descriptor1Name]).To(BeEquivalentTo(10))
	originStats = graphR.GetFlagStats(OriginFlagName, nil)
	Expect(originStats.TotalCount).To(BeEquivalentTo(10))
	Expect(originStats.PerValueCount).To(HaveKey(FromNB.String()))
	Expect(originStats.PerValueCount[FromNB.String()]).To(BeEquivalentTo(10))
	graphR.Release()

	// finally, run 4th txn to get back the removed value
	schedulerTxn = scheduler.StartNBTransaction()
	schedulerTxn.SetValue(prefixA+baseValue2, test.NewLazyStringValue("base-value2-data-new"))
	kvErrors, txnError = schedulerTxn.Commit(context.Background())
	Expect(txnError).ShouldNot(HaveOccurred())
	Expect(kvErrors).To(HaveLen(0))

	// check the state of SB
	Expect(mockSB.GetKeysWithInvalidData()).To(BeEmpty())
	Expect(mockSB.GetValues(nil)).To(HaveLen(4))
	// -> base value 1
	value = mockSB.GetValue(prefixA + baseValue1)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("base-value1-data"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> base value 2
	value = mockSB.GetValue(prefixA + baseValue2)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("base-value2-data-new"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> base value 3
	value = mockSB.GetValue(prefixA + baseValue3)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("base-value3-data"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> base value 4
	value = mockSB.GetValue(prefixA + baseValue4)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("base-value4-data"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))

	// check pending values
	pendingValues = scheduler.GetPendingValues(nil)
	Expect(pendingValues).To(BeEmpty())
}

func TestSpecialCase(t *testing.T) {
	RegisterTestingT(t)

	// prepare KV Scheduler
	scheduler := NewPlugin(UseDeps(func(deps *Deps) {
		deps.HTTPHandlers = nil
	}))
	err := scheduler.Init()
	Expect(err).To(BeNil())

	// prepare mocks
	mockSB := test.NewMockSouthbound()
	// descriptor:
	descriptor := test.NewMockDescriptor(&KVDescriptor{
		Name:            descriptor1Name,
		NBKeyPrefix:     prefixA,
		KeySelector:     prefixSelector(prefixA),
		ValueTypeName:   test.ArrayValueTypeName,
		DerivedValues:   test.ArrayValueDerBuilder,
		WithMetadata:    true,
	}, mockSB, 0)
	scheduler.RegisterKVDescriptor(descriptor)

	// run non-resync transaction against empty SB
	schedulerTxn := scheduler.StartNBTransaction()
	schedulerTxn.SetValue(prefixA+baseValue1, test.NewLazyArrayValue("item1"))
	kvErrors, txnError := schedulerTxn.Commit(context.Background())
	Expect(txnError).ShouldNot(HaveOccurred())
	Expect(kvErrors).To(BeEmpty())

	// check the state of SB
	Expect(mockSB.GetKeysWithInvalidData()).To(BeEmpty())
	// -> base value 1
	value := mockSB.GetValue(prefixA + baseValue1)
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewArrayValue("item1"))).To(BeTrue())
	Expect(value.Metadata).ToNot(BeNil())
	Expect(value.Metadata.(test.MetaWithInteger).GetInteger()).To(BeEquivalentTo(0))
	Expect(value.Origin).To(BeEquivalentTo(FromNB))
	// -> item1 derived from base value 1
	value = mockSB.GetValue(prefixA + baseValue1 + "/item1")
	Expect(value).ToNot(BeNil())
	Expect(proto.Equal(value.Value, test.NewStringValue("item1"))).To(BeTrue())
	Expect(value.Metadata).To(BeNil())
	Expect(value.Origin).To(BeEquivalentTo(FromNB))

	// plan error before 2nd txn
	failedDeleteClb := func() {
		mockSB.SetValue(prefixA+baseValue1, test.NewArrayValue(),
			&test.OnlyInteger{Integer: 0}, FromNB, false)
	}
	mockSB.PlanError(prefixA+baseValue1+"/item1", errors.New("failed to delete value"), failedDeleteClb)

	// run 2nd non-resync transaction that will have errors
	schedulerTxn2 := scheduler.StartNBTransaction()
	schedulerTxn2.SetValue(prefixA+baseValue1, nil)
	kvErrors, txnError = schedulerTxn2.Commit(WithRevert(context.Background()))
	Expect(txnError).ShouldNot(HaveOccurred())
	Expect(kvErrors).To(HaveLen(1))
	Expect(kvErrors[0].Key).To(BeEquivalentTo(prefixA + baseValue1 + "/item1"))
	Expect(kvErrors[0].Error.Error()).To(BeEquivalentTo("failed to delete value"))

	//Expect(false).To(BeTrue())

	// close scheduler
	err = scheduler.Close()
	Expect(err).To(BeNil())
}
