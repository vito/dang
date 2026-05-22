package dang

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

func decodeYAML(data string) (any, error) {
	decoder := yaml.NewDecoder(strings.NewReader(data))

	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, err
	}

	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("trailing document")
	}

	return yamlNodeToDeferredRaw(&doc, map[*yaml.Node]bool{})
}

func yamlNodeToDeferredRaw(node *yaml.Node, seen map[*yaml.Node]bool) (any, error) {
	if node == nil {
		return nil, nil
	}

	if seen[node] {
		return nil, fmt.Errorf("cyclic alias")
	}
	seen[node] = true
	defer delete(seen, node)

	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) == 0 {
			return nil, nil
		}
		return yamlNodeToDeferredRaw(node.Content[0], seen)

	case yaml.ScalarNode:
		switch node.Tag {
		case "!!null":
			return nil, nil
		case "!!bool":
			var b bool
			if err := node.Decode(&b); err != nil {
				return nil, err
			}
			return b, nil
		case "!!int", "!!float":
			return json.Number(normalizeYAMLNumber(node.Value)), nil
		case "!!str", "":
			return node.Value, nil
		default:
			// Keep unsupported scalar tags as strings. This keeps deferred
			// materialization in the same JSON-like domain without adding
			// YAML-specific runtime types.
			return node.Value, nil
		}

	case yaml.SequenceNode:
		items := make([]any, len(node.Content))
		for i, item := range node.Content {
			val, err := yamlNodeToDeferredRaw(item, seen)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			items[i] = val
		}
		return items, nil

	case yaml.MappingNode:
		obj := make(map[string]any, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			if keyNode.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("mapping key must be a scalar")
			}
			val, err := yamlNodeToDeferredRaw(node.Content[i+1], seen)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", keyNode.Value, err)
			}
			obj[keyNode.Value] = val
		}
		return obj, nil

	case yaml.AliasNode:
		return yamlNodeToDeferredRaw(node.Alias, seen)

	default:
		return nil, fmt.Errorf("unsupported YAML node kind %d", node.Kind)
	}
}

func normalizeYAMLNumber(value string) string {
	return strings.ReplaceAll(value, "_", "")
}
