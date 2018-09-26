package kvscheduler

import (
	"github.com/gogo/protobuf/proto"
	. "github.com/ligato/cn-infra/kvscheduler/api"
)

// descriptorHandler handles access to descriptor methods (callbacks).
// For callback not provided, a default return value is returned.
type descriptorHandler struct {
	descriptor *KVDescriptor
}

// keyLabel by default returns the key itself.
func (h *descriptorHandler) keyLabel(key string) string {
	if h.descriptor == nil || h.descriptor.KeyLabel == nil {
		return key
	}
	return h.descriptor.KeyLabel(key)
}

// equivalentValues by default uses proto.Equal().
func (h *descriptorHandler) equivalentValues(key string, v1, v2 proto.Message) bool {
	if h.descriptor == nil || h.descriptor.ValueComparator == nil {
		return proto.Equal(v1, v2)
	}
	return h.descriptor.ValueComparator(key, v1, v2)
}

// add returns ErrUnimplementedAdd is Add is not provided.
func (h *descriptorHandler) add(key string, value proto.Message) (metadata Metadata, err error) {
	if h.descriptor == nil {
		return
	}
	if h.descriptor.Add == nil {
		return nil, ErrUnimplementedAdd
	}
	return h.descriptor.Add(key, value)
}

// modify returns ErrUnimplementedModify if Modify is not provided.
func (h *descriptorHandler) modify(key string, oldValue, newValue proto.Message, oldMetadata Metadata) (newMetadata Metadata, err error) {
	if h.descriptor == nil {
		return oldMetadata, nil
	}
	if h.descriptor.Modify == nil {
		return oldMetadata, ErrUnimplementedModify
	}
	return h.descriptor.Modify(key, oldValue, newValue, oldMetadata)
}

// modifyWithRecreate by default assumes any change can be applied using Modify without
// re-creation.
func (h *descriptorHandler) modifyWithRecreate(key string, oldValue, newValue proto.Message, metadata Metadata) bool {
	if h.descriptor == nil || h.descriptor.ModifyWithRecreate == nil {
		return false
	}
	return h.descriptor.ModifyWithRecreate(key, oldValue, newValue, metadata)
}

// delete returns ErrUnimplementedDelete if Delete is not provided.
func (h *descriptorHandler) delete(key string, value proto.Message, metadata Metadata) error {
	if h.descriptor == nil {
		return nil
	}
	if h.descriptor.Delete == nil {
		return ErrUnimplementedDelete
	}
	return h.descriptor.Delete(key, value, metadata)
}

// update does nothing if Update is not provided (totally optional method).
func (h *descriptorHandler) update(key string, value proto.Message, metadata Metadata) error {
	if h.descriptor == nil || h.descriptor.Update == nil {
		return nil
	}
	return h.descriptor.Update(key, value, metadata)
}

// retriableFailure first checks for errors returned by the handler itself.
// If descriptor does not define RetriableFailure, it is assumed any failure
// can be potentially fixed by retry.
func (h *descriptorHandler) retriableFailure(err error) bool {
	// first check for errors returned by the handler itself
	handlerErrs := []error{ErrUnimplementedAdd, ErrUnimplementedModify, ErrUnimplementedDelete}
	retriableFailure := NonRetriableIfInTheList(handlerErrs)
	if !retriableFailure(err) {
		return false
	}
	if h.descriptor == nil || h.descriptor.RetriableFailure == nil {
		return true
	}
	return h.descriptor.RetriableFailure(err)
}

// dependencies returns empty list if descriptor does not define any.
func (h *descriptorHandler) dependencies(key string, value proto.Message) (deps []Dependency) {
	if h.descriptor == nil || h.descriptor.Dependencies == nil {
		return
	}
	return h.descriptor.Dependencies(key, value)
}

// derivedValues returns empty list if descriptor does not define any.
func (h *descriptorHandler) derivedValues(key string, value proto.Message) (derives []KeyValuePair) {
	if h.descriptor == nil || h.descriptor.DerivedValues == nil {
		return
	}
	return h.descriptor.DerivedValues(key, value)
}

// dump returns <ableToDump> as false if descriptor does not implement Dump.
func (h *descriptorHandler) dump(correlate []KVWithMetadata) (dump []KVWithMetadata, ableToDump bool, err error) {
	if h.descriptor == nil || h.descriptor.Dump == nil {
		return dump, false, nil
	}
	dump, err = h.descriptor.Dump(correlate)
	return dump, true, err
}
