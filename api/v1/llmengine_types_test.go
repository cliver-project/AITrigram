/*
Copyright 2025 Red Hat, Inc.

Authors: Lin Gao <lgao@redhat.com>

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

package v1

import (
	"encoding/json"
	"testing"
)

func TestModelReferenceUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
		wantRev  string
		wantErr  bool
	}{
		{
			name:     "object format",
			input:    `{"name":"my-model","revision":"v1.0"}`,
			wantName: "my-model",
			wantRev:  "v1.0",
		},
		{
			name:     "object format name only",
			input:    `{"name":"my-model"}`,
			wantName: "my-model",
		},
		{
			name:     "bare string",
			input:    `"my-model"`,
			wantName: "my-model",
		},
		{
			name:    "invalid type",
			input:   `123`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ref ModelReference
			err := json.Unmarshal([]byte(tt.input), &ref)
			if (err != nil) != tt.wantErr {
				t.Fatalf("UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if ref.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", ref.Name, tt.wantName)
			}
			if ref.Revision != tt.wantRev {
				t.Errorf("Revision = %q, want %q", ref.Revision, tt.wantRev)
			}
		})
	}
}

func TestModelReferenceUnmarshalArray(t *testing.T) {
	// Simulate what the controller sees when listing LLMEngines
	tests := []struct {
		name      string
		input     string
		wantNames []string
	}{
		{
			name:      "array of objects",
			input:     `[{"name":"model-a"},{"name":"model-b","revision":"v2"}]`,
			wantNames: []string{"model-a", "model-b"},
		},
		{
			name:      "array of bare strings",
			input:     `["model-a","model-b"]`,
			wantNames: []string{"model-a", "model-b"},
		},
		{
			name:      "mixed array",
			input:     `["model-a",{"name":"model-b"}]`,
			wantNames: []string{"model-a", "model-b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var refs []ModelReference
			if err := json.Unmarshal([]byte(tt.input), &refs); err != nil {
				t.Fatalf("UnmarshalJSON() error = %v", err)
			}
			if len(refs) != len(tt.wantNames) {
				t.Fatalf("got %d refs, want %d", len(refs), len(tt.wantNames))
			}
			for i, want := range tt.wantNames {
				if refs[i].Name != want {
					t.Errorf("refs[%d].Name = %q, want %q", i, refs[i].Name, want)
				}
			}
		})
	}
}
