package main

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// fillRequired sets every missing proto2 "required" field to its default value,
// recursively. Server-Infinite leaves out zero values, so the game's protobuf
// parser logs "missing required fields" (seen in the Live Monitor log). Returns
// how many fields it filled.
func fillRequired(m proto.Message) int {
	return fillRequiredRefl(m.ProtoReflect())
}

func fillRequiredRefl(m protoreflect.Message) int {
	n := 0
	fields := m.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if fd.Cardinality() == protoreflect.Required && !m.Has(fd) {
			if fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind {
				m.Set(fd, protoreflect.ValueOfMessage(m.Mutable(fd).Message()))
			} else {
				m.Set(fd, fd.Default())
			}
			n++
		}
	}
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsList() && (fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind):
			l := v.List()
			for j := 0; j < l.Len(); j++ {
				n += fillRequiredRefl(l.Get(j).Message())
			}
		case fd.IsMap():
			if fd.MapValue().Kind() == protoreflect.MessageKind {
				v.Map().Range(func(_ protoreflect.MapKey, mv protoreflect.Value) bool {
					n += fillRequiredRefl(mv.Message())
					return true
				})
			}
		case !fd.IsList() && (fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind):
			n += fillRequiredRefl(v.Message())
		}
		return true
	})
	return n
}
