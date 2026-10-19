// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package chart_inspection

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	maxYAMLNodes       = 50000
	maxYAMLDepth       = 64
	maxYAMLScalarBytes = 4 << 20
)

// Limits apply to expanded values, not only input bytes: aliases can repeat a
// small source node many times before the result is serialized.
type yamlBudget struct {
	nodes int
	bytes int
}

func (b *yamlBudget) charge(value string) error {
	if len(value) > b.bytes {
		return fmt.Errorf("expanded YAML content exceeds 4 MiB")
	}
	b.bytes -= len(value)
	return nil
}

// Decode to nodes before conversion so aliases, duplicate keys and expansion
// budgets are checked before allocating an unrestricted object graph.
func yamlObject(data []byte) (map[string]any, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if err == io.EOF {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("decode YAML: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("decode YAML: %w", err)
		}
		return nil, fmt.Errorf("multiple YAML documents are unsupported")
	}
	if len(document.Content) != 1 {
		return nil, fmt.Errorf("YAML must contain an object")
	}
	if document.Content[0].Tag == "!!null" {
		return map[string]any{}, nil
	}
	budget := &yamlBudget{nodes: maxYAMLNodes, bytes: maxYAMLScalarBytes}
	value, err := yamlValue(document.Content[0], map[*yaml.Node]bool{}, 0, budget)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("YAML must contain an object")
	}
	return object, nil
}

func yamlValue(node *yaml.Node, active map[*yaml.Node]bool, depth int, budget *yamlBudget) (any, error) {
	budget.nodes--
	if depth > maxYAMLDepth || budget.nodes < 0 || active[node] {
		return nil, fmt.Errorf("YAML is too complex or contains an alias cycle")
	}
	active[node] = true
	defer delete(active, node)
	if node.Kind == yaml.AliasNode {
		return yamlValue(node.Alias, active, depth+1, budget)
	}
	switch node.Kind {
	case yaml.MappingNode:
		out := map[string]any{}
		seen := map[string]bool{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || (key.Tag != "!!str" && key.Tag != "!!merge") {
				return nil, fmt.Errorf("YAML object keys must be strings")
			}
			if seen[key.Value] {
				return nil, fmt.Errorf("duplicate YAML key %q", key.Value)
			}
			if err := budget.charge(key.Value); err != nil {
				return nil, err
			}
			seen[key.Value] = true
			value, err := yamlValue(node.Content[i+1], active, depth+1, budget)
			if err != nil {
				return nil, err
			}
			if key.Tag == "!!merge" {
				maps := []any{value}
				if list, ok := value.([]any); ok {
					maps = list
				}
				for _, item := range maps {
					object, ok := item.(map[string]any)
					if !ok {
						return nil, fmt.Errorf("YAML merge requires objects")
					}
					for k, v := range object {
						if _, exists := out[k]; !exists {
							out[k] = v
						}
					}
				}
			} else {
				out[key.Value] = value
			}
		}
		return out, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(node.Content))
		for _, child := range node.Content {
			value, err := yamlValue(child, active, depth+1, budget)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		return out, nil
	case yaml.ScalarNode:
		// Aliases revisit scalars, so charge every occurrence before response encoding.
		if err := budget.charge(node.Value); err != nil {
			return nil, err
		}
		switch node.Tag {
		case "!!str", "!!timestamp":
			return node.Value, nil
		case "!!null":
			return nil, nil
		case "!!binary":
			value, err := base64.StdEncoding.DecodeString(node.Value)
			if err != nil {
				return nil, fmt.Errorf("decode YAML binary: %w", err)
			}
			return string(value), nil
		case "!!int", "!!float", "!!bool":
			var value any
			if err := node.Decode(&value); err != nil {
				return nil, fmt.Errorf("decode YAML scalar: %w", err)
			}
			if number, ok := value.(float64); ok && (math.IsNaN(number) || math.IsInf(number, 0)) {
				return nil, fmt.Errorf("YAML numbers must be finite")
			}
			if _, rounded := value.(float64); rounded {
				// YAML falls back to float64 for integers exceeding native integer ranges.
				if integer, ok := new(big.Int).SetString(strings.ReplaceAll(node.Value, "_", ""), 0); ok {
					return json.Number(integer.String()), nil
				}
			}
			return value, nil
		default:
			return nil, fmt.Errorf("unsupported YAML tag %s", node.Tag)
		}
	default:
		return nil, fmt.Errorf("unsupported YAML node")
	}
}
