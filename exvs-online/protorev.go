package main

// The game embeds its protocol description (game_server_nue.proto). Boost Link's
// copy in pb/exvs is generated from it, so comparing the two revisions tells
// whether a new game build changed the messages exvs-online reads and rewrites.

import (
	"bytes"

	"exvsonline/pb/exvs"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// embeddedProto finds a serialized FileDescriptorProto named name inside exe.
func embeddedProto(exe []byte, name string) *descriptorpb.FileDescriptorProto {
	key := append([]byte{0x0a, byte(len(name))}, name...)
	i := bytes.Index(exe, key)
	if i < 0 {
		return nil
	}
	b := exe[i:]
	n := 0
	for n < len(b) {
		num, typ, l := protowire.ConsumeTag(b[n:])
		if l < 0 || num < 1 || num > 20 { // past the end of the descriptor
			break
		}
		vl := protowire.ConsumeFieldValue(num, typ, b[n+l:])
		if vl < 0 {
			break
		}
		n += l + vl
	}
	var fd descriptorpb.FileDescriptorProto
	if proto.Unmarshal(b[:n], &fd) != nil {
		return nil
	}
	return &fd
}

// gameProtoRevision returns the exe's PROTOBUF_MESSAGE_REVISION, or 0 if it can't be read.
func gameProtoRevision(exe []byte) int32 {
	fd := embeddedProto(exe, "game_server_nue.proto")
	if fd == nil {
		return 0
	}
	for _, e := range fd.EnumType {
		for _, v := range e.Value {
			if v.GetName() == "PROTOBUF_MESSAGE_REVISION" {
				return v.GetNumber()
			}
		}
	}
	return 0
}

// ourProtoRevision is the revision pb/exvs was generated from.
const ourProtoRevision = int32(exvs.SystemConstant_PROTOBUF_MESSAGE_REVISION)
