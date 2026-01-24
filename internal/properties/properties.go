package properties

import (
	"fmt"

	pathway_proto "github.com/npclaudiu/pathway/internal/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// MarshalProperties converts a Go map to a serialized Properties message.
func MarshalProperties(props map[string]interface{}) ([]byte, error) {
	if props == nil {
		return nil, nil
	}

	// Convert map[string]interface{} to map[string]*structpb.Value
	pbMap := make(map[string]*structpb.Value, len(props))
	for k, v := range props {
		val, err := structpb.NewValue(v)
		if err != nil {
			return nil, fmt.Errorf("failed to convert property '%s': %w", k, err)
		}
		pbMap[k] = val
	}

	p := &pathway_proto.Properties{
		Data: pbMap,
	}

	return proto.Marshal(p)
}

// UnmarshalProperties converts generic bytes to a Go map using Protobuf.
func UnmarshalProperties(data []byte) (map[string]interface{}, error) {
	if len(data) == 0 {
		return nil, nil
	}

	p := &pathway_proto.Properties{}
	if err := proto.Unmarshal(data, p); err != nil {
		return nil, err
	}

	// Convert back to map[string]interface{}
	result := make(map[string]interface{}, len(p.Data))
	for k, v := range p.Data {
		result[k] = v.AsInterface()
	}
	return result, nil
}
