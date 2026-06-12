package dang

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// encodeYAML serializes a value to a YAML document. The value is first
// marshaled through its JSON representation — the single source of truth
// for serialization — and the JSON token stream is rebuilt as a YAML node
// tree so mapping keys keep their order.
func encodeYAML(val Value) (string, error) {
	jsonBytes, err := json.Marshal(val)
	if err != nil {
		return "", err
	}

	dec := json.NewDecoder(bytes.NewReader(jsonBytes))
	dec.UseNumber()
	node, err := jsonToYAMLNode(dec)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func jsonToYAMLNode(dec *json.Decoder) (*yaml.Node, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}

	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '[':
			node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for dec.More() {
				item, err := jsonToYAMLNode(dec)
				if err != nil {
					return nil, err
				}
				node.Content = append(node.Content, item)
			}
			_, err := dec.Token() // consume ']'
			return node, err
		case '{':
			node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyTok.(string)
				if !ok {
					return nil, fmt.Errorf("unexpected JSON key token %v", keyTok)
				}
				val, err := jsonToYAMLNode(dec)
				if err != nil {
					return nil, err
				}
				node.Content = append(node.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
					val)
			}
			_, err = dec.Token() // consume '}'
			return node, err
		default:
			return nil, fmt.Errorf("unexpected JSON delimiter %v", t)
		}
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: t}, nil
	case json.Number:
		tag := "!!int"
		if strings.ContainsAny(t.String(), ".eE") {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: t.String()}, nil
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(t)}, nil
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	default:
		return nil, fmt.Errorf("unexpected JSON token %v", tok)
	}
}
