package engine

import (
	"google.golang.org/protobuf/reflect/protoreflect"
)

type GRPCMessage struct {
	Name   string
	Fields []GRPCField
}

type GRPCField struct {
	Name, ProtoName, Kind string
	Repeated              bool
	Map                   bool
	Enum                  []string
	Message               *GRPCMessage
}

func (m *GRPCMessage) Field(name string) *GRPCField {
	if m == nil {
		return nil
	}
	for i := range m.Fields {
		if m.Fields[i].Name == name || m.Fields[i].ProtoName == name {
			return &m.Fields[i]
		}
	}
	return nil
}

func grpcMessage(d protoreflect.MessageDescriptor, depth int) *GRPCMessage {
	m := &GRPCMessage{Name: string(d.FullName()), Fields: []GRPCField{}}
	if depth > 6 {
		return m
	}
	fields := d.Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		field := GRPCField{Name: f.JSONName(), ProtoName: string(f.Name()), Repeated: f.IsList(), Map: f.IsMap()}
		value := f
		if f.IsMap() {
			value = f.MapValue()
		}
		field.Kind, field.Enum, field.Message = grpcKind(value, depth)
		m.Fields = append(m.Fields, field)
	}
	return m
}

func grpcKind(f protoreflect.FieldDescriptor, depth int) (string, []string, *GRPCMessage) {
	switch f.Kind() {
	case protoreflect.BoolKind:
		return "boolean", nil, nil
	case protoreflect.StringKind:
		return "string", nil, nil
	case protoreflect.BytesKind:
		return "bytes", nil, nil
	case protoreflect.EnumKind:
		values := f.Enum().Values()
		names := make([]string, 0, values.Len())
		for i := 0; i < values.Len(); i++ {
			names = append(names, string(values.Get(i).Name()))
		}
		return "enum", names, nil
	case protoreflect.MessageKind, protoreflect.GroupKind:
		switch f.Message().FullName() {
		case "google.protobuf.Timestamp", "google.protobuf.Duration", "google.protobuf.FieldMask":
			return "string", nil, nil
		case "google.protobuf.Struct", "google.protobuf.Value", "google.protobuf.ListValue", "google.protobuf.Any", "google.protobuf.Empty":
			return "any", nil, nil
		case "google.protobuf.StringValue", "google.protobuf.BytesValue":
			return "string", nil, nil
		case "google.protobuf.BoolValue":
			return "boolean", nil, nil
		case "google.protobuf.Int32Value", "google.protobuf.UInt32Value", "google.protobuf.Int64Value", "google.protobuf.UInt64Value", "google.protobuf.FloatValue", "google.protobuf.DoubleValue":
			return "number", nil, nil
		}
		return "object", nil, grpcMessage(f.Message(), depth+1)
	}
	return "number", nil, nil
}
