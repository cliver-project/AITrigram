/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package adapters

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConvertArgsToVLLMConfig_FlatArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected map[string]interface{}
	}{
		{
			name: "simple key-value with equals",
			args: []string{"--dtype=float16", "--max-model-len=4096"},
			expected: map[string]interface{}{
				"dtype":         "float16",
				"max_model_len": 4096,
			},
		},
		{
			name: "key-value with spaces",
			args: []string{"--dtype", "float16", "--max-model-len", "4096"},
			expected: map[string]interface{}{
				"dtype":         "float16",
				"max_model_len": 4096,
			},
		},
		{
			name: "bool flags",
			args: []string{"--enforce-eager", "--disable-log-stats"},
			expected: map[string]interface{}{
				"enforce_eager":     true,
				"disable_log_stats": true,
			},
		},
		{
			name: "mixed types",
			args: []string{
				"--dtype=float16",
				"--max-model-len=4096",
				"--enforce-eager",
				"--gpu-memory-utilization=0.9",
			},
			expected: map[string]interface{}{
				"dtype":                  "float16",
				"max_model_len":          4096,
				"enforce_eager":          true,
				"gpu_memory_utilization": 0.9,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertArgsToVLLMConfig(tt.args)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertArgsToVLLMConfig_NestedArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected map[string]interface{}
	}{
		{
			name: "simple nested config",
			args: []string{
				"--compilation-config.mode=3",
			},
			expected: map[string]interface{}{
				"compilation_config": map[string]interface{}{
					"mode": 3,
				},
			},
		},
		{
			name: "multiple nested keys same parent",
			args: []string{
				"--compilation-config.mode=3",
				"--compilation-config.level=2",
			},
			expected: map[string]interface{}{
				"compilation_config": map[string]interface{}{
					"mode":  3,
					"level": 2,
				},
			},
		},
		{
			name: "deeply nested config",
			args: []string{
				"--a.b.c.d=value",
			},
			expected: map[string]interface{}{
				"a": map[string]interface{}{
					"b": map[string]interface{}{
						"c": map[string]interface{}{
							"d": "value",
						},
					},
				},
			},
		},
		{
			name: "mixed flat and nested",
			args: []string{
				"--dtype=float16",
				"--compilation-config.mode=3",
				"--max-model-len=4096",
			},
			expected: map[string]interface{}{
				"dtype":         "float16",
				"max_model_len": 4096,
				"compilation_config": map[string]interface{}{
					"mode": 3,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertArgsToVLLMConfig(tt.args)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertArgsToVLLMConfig_JSONValues(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected map[string]interface{}
	}{
		{
			name: "JSON object as value",
			args: []string{
				`--compilation-config={"mode": 1, "level": 2}`,
			},
			expected: map[string]interface{}{
				"compilation_config": map[string]interface{}{
					"mode":  float64(1), // JSON numbers are float64
					"level": float64(2),
				},
			},
		},
		{
			name: "JSON array as value",
			args: []string{
				`--options=["a", "b", "c"]`,
			},
			expected: map[string]interface{}{
				"options": []interface{}{"a", "b", "c"},
			},
		},
		{
			name: "JSON value overridden by dot notation",
			args: []string{
				`--compilation-config={"mode": 1, "level": 2}`,
				"--compilation-config.mode=3",
			},
			expected: map[string]interface{}{
				"compilation_config": map[string]interface{}{
					"mode":  3,          // overridden from JSON 1 to int 3 (dot notation)
					"level": float64(2), // from JSON
				},
			},
		},
		{
			name: "dot notation merged with JSON",
			args: []string{
				"--compilation-config.mode=3",
				`--compilation-config={"level": 2}`,
			},
			expected: map[string]interface{}{
				"compilation_config": map[string]interface{}{
					"mode":  3,          // from dot notation (int)
					"level": float64(2), // from JSON
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertArgsToVLLMConfig(tt.args)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertArgsToVLLMConfig_HyphenToUnderscore(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected map[string]interface{}
	}{
		{
			name: "hyphenated keys converted to underscores",
			args: []string{
				"--max-model-len=4096",
				"--gpu-memory-utilization=0.9",
			},
			expected: map[string]interface{}{
				"max_model_len":          4096,
				"gpu_memory_utilization": 0.9,
			},
		},
		{
			name: "nested hyphenated keys",
			args: []string{
				"--compilation-config.custom-mode=3",
			},
			expected: map[string]interface{}{
				"compilation_config": map[string]interface{}{
					"custom_mode": 3,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertArgsToVLLMConfig(tt.args)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCreateNestedMap(t *testing.T) {
	tests := []struct {
		name     string
		keys     []string
		value    interface{}
		expected map[string]interface{}
	}{
		{
			name:  "single key",
			keys:  []string{"a"},
			value: "value",
			expected: map[string]interface{}{
				"a": "value",
			},
		},
		{
			name:  "two keys",
			keys:  []string{"a", "b"},
			value: 123,
			expected: map[string]interface{}{
				"a": map[string]interface{}{
					"b": 123,
				},
			},
		},
		{
			name:  "three keys",
			keys:  []string{"a", "b", "c"},
			value: true,
			expected: map[string]interface{}{
				"a": map[string]interface{}{
					"b": map[string]interface{}{
						"c": true,
					},
				},
			},
		},
		{
			name:     "empty keys",
			keys:     []string{},
			value:    "value",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := createNestedMap(tt.keys, tt.value)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRecursiveMerge(t *testing.T) {
	tests := []struct {
		name     string
		dst      map[string]interface{}
		src      map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name: "merge non-overlapping keys",
			dst: map[string]interface{}{
				"a": 1,
			},
			src: map[string]interface{}{
				"b": 2,
			},
			expected: map[string]interface{}{
				"a": 1,
				"b": 2,
			},
		},
		{
			name: "override scalar value",
			dst: map[string]interface{}{
				"a": 1,
			},
			src: map[string]interface{}{
				"a": 2,
			},
			expected: map[string]interface{}{
				"a": 2,
			},
		},
		{
			name: "merge nested maps",
			dst: map[string]interface{}{
				"config": map[string]interface{}{
					"mode": 1,
				},
			},
			src: map[string]interface{}{
				"config": map[string]interface{}{
					"level": 2,
				},
			},
			expected: map[string]interface{}{
				"config": map[string]interface{}{
					"mode":  1,
					"level": 2,
				},
			},
		},
		{
			name: "override nested value",
			dst: map[string]interface{}{
				"config": map[string]interface{}{
					"mode": 1,
				},
			},
			src: map[string]interface{}{
				"config": map[string]interface{}{
					"mode": 3,
				},
			},
			expected: map[string]interface{}{
				"config": map[string]interface{}{
					"mode": 3,
				},
			},
		},
		{
			name: "replace map with scalar",
			dst: map[string]interface{}{
				"config": map[string]interface{}{
					"mode": 1,
				},
			},
			src: map[string]interface{}{
				"config": "simple",
			},
			expected: map[string]interface{}{
				"config": "simple",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recursiveMerge(tt.dst, tt.src)
			assert.Equal(t, tt.expected, tt.dst)
		})
	}
}
